package server

import (
	"context"
	"crypto/subtle"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"

	"github.com/mtdewwolf/sloplex/internal/accounts"
	"github.com/mtdewwolf/sloplex/internal/transcode"
)

const tokenHeader = "X-Plex-Token"

var corsAllowHeaders = "Accept, Content-Type, Range, " +
	"X-Plex-Token, X-Plex-Client-Identifier, X-Plex-Product, X-Plex-Version, " +
	"X-Plex-Platform, X-Plex-Platform-Version, X-Plex-Device, X-Plex-Device-Name, " +
	"X-Plex-Container-Start, X-Plex-Container-Size"

// cors allows browser clients from any origin. Credentials travel in
// headers or query parameters, never cookies. Tokenless trusted-network
// access, the one ambient credential, is refused to other origins by
// authenticator.wrap, so the wildcard exposes nothing a token does not
// already grant; see "Browser origins" in docs/spec/auth.md.
func cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Access-Control-Allow-Origin", "*")
		h.Set("Access-Control-Expose-Headers", "Content-Range, Content-Length, Accept-Ranges")
		if r.Method == http.MethodOptions {
			h.Set("Access-Control-Allow-Methods", "GET, HEAD, PUT, POST, DELETE, OPTIONS")
			h.Set("Access-Control-Allow-Headers", corsAllowHeaders)
			h.Set("Access-Control-Max-Age", "86400")
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// browserHome sends a browser that opens the server's bare address to the
// web app instead of a 401. Apps ask for XML or JSON, or send a token, and
// get the server root as before.
func browserHome(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" && (r.Method == http.MethodGet || r.Method == http.MethodHead) &&
			requestToken(r) == "" && strings.Contains(r.Header.Get("Accept"), "text/html") {
			http.Redirect(w, r, "/web/", http.StatusFound)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// principal is who a request is authenticated as.
type principal struct {
	AccountID int
	// TokenID is the issued token used, or 0 for an owner master token or a
	// trusted network.
	TokenID int
}

// owner is the authority a transcode started by p runs under.
func (p principal) owner() transcode.Owner {
	return transcode.Owner{Account: p.AccountID, Token: p.TokenID}
}

type principalKey struct{}

func principalOf(r *http.Request) (principal, bool) {
	p, ok := r.Context().Value(principalKey{}).(principal)
	return p, ok
}

// authenticator resolves tokens to accounts. See docs/spec/auth.md.
type authenticator struct {
	master   [][]byte
	accounts *accounts.Store
	networks []netip.Prefix
	public   map[string]bool
}

func newAuthenticator(master []string, networks []netip.Prefix, accts *accounts.Store) *authenticator {
	a := &authenticator{accounts: accts, networks: networks, public: map[string]bool{
		"/identity":           true,
		"/sloplex/auth/login": true,
	}}
	for _, t := range master {
		if t != "" {
			a.master = append(a.master, []byte(t))
		}
	}
	return a
}

// isPublic reports whether a path needs no token: a few fixed endpoints and
// the static web app.
func (a *authenticator) isPublic(path string) bool {
	if a.public[path] {
		return true
	}
	// Refuse dot segments outright rather than relying on the router to
	// clean "/web/../library" before it reaches a handler.
	if strings.Contains(path, "..") {
		return false
	}
	// Transcode session URLs are authorised by their random session ID.
	return path == "/web" || strings.HasPrefix(path, "/web/") ||
		strings.HasPrefix(path, "/video/:/transcode/universal/session/")
}

// requestToken returns the token a request supplies in its header or,
// failing that, its query string, or "" if it supplies none.
func requestToken(r *http.Request) string {
	if tok := r.Header.Get(tokenHeader); tok != "" {
		return tok
	}
	return r.URL.Query().Get(tokenHeader)
}

// resolve returns the principal for a token, if valid.
func (a *authenticator) resolve(tok string) (principal, bool) {
	if tok == "" {
		return principal{}, false
	}
	ok := 0
	for _, t := range a.master {
		ok |= subtle.ConstantTimeCompare([]byte(tok), t)
	}
	if ok == 1 {
		return principal{AccountID: accounts.OwnerID}, true
	}
	if acct, t, found := a.accounts.Lookup(tok); found {
		return principal{AccountID: acct.ID, TokenID: t.ID}, true
	}
	return principal{}, false
}

func (a *authenticator) trustedPeer(remote string) bool {
	if len(a.networks) == 0 {
		return false
	}
	host, _, err := net.SplitHostPort(remote)
	if err != nil {
		return false
	}
	ip, err := netip.ParseAddr(host)
	if err != nil {
		return false
	}
	ip = ip.Unmap()
	for _, p := range a.networks {
		if p.Contains(ip) {
			return true
		}
	}
	return false
}

// crossOrigin reports whether a browser marked r as sent by a page of
// another origin; see "Browser origins" in docs/spec/auth.md. Only headers
// a page cannot set itself are used, and forwarding headers are ignored.
func crossOrigin(r *http.Request) bool {
	switch r.Header.Get("Sec-Fetch-Site") {
	case "same-origin", "none":
		return false
	case "":
	default:
		return true
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		// Not a browser, or an older browser's same-origin or no-cors
		// request.
		return false
	}
	u, err := url.Parse(origin)
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return err != nil || !strings.EqualFold(u.Scheme, scheme) || u.Host == "" ||
		u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" ||
		!strings.EqualFold(u.Host, r.Host)
}

func (a *authenticator) wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tok := requestToken(r)
		p, ok := a.resolve(tok)
		switch {
		case ok:
		case tok != "":
			// A supplied credential that does not resolve is never replaced
			// by network trust, so a revoked device cannot become the owner.
			if !a.isPublic(r.URL.Path) {
				http.Error(w, "401 Unauthorized", http.StatusUnauthorized)
				return
			}
		case a.trustedPeer(r.RemoteAddr):
			w.Header().Add("Vary", "Origin, Sec-Fetch-Site")
			if !crossOrigin(r) {
				p, ok = principal{AccountID: accounts.OwnerID}, true
			}
		}
		if ok {
			r = r.WithContext(context.WithValue(r.Context(), principalKey{}, p))
		} else if !a.isPublic(r.URL.Path) {
			http.Error(w, "401 Unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
	bytes  int64
}

func (s *statusRecorder) WriteHeader(code int) {
	if s.status == 0 {
		s.status = code
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	if s.status == 0 {
		s.status = http.StatusOK
	}
	n, err := s.ResponseWriter.Write(b)
	s.bytes += int64(n)
	return n, err
}

// Unwrap lets http.ResponseController reach the underlying writer.
func (s *statusRecorder) Unwrap() http.ResponseWriter { return s.ResponseWriter }

func logRequests(log *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w}
		next.ServeHTTP(rec, r)
		if rec.status == 0 {
			rec.status = http.StatusOK
		}
		// Log the path only: query strings may carry access tokens.
		log.Debug("http", "method", r.Method, "path", r.URL.Path, "status", rec.status,
			"bytes", rec.bytes, "remote", r.RemoteAddr, "took", time.Since(start).Round(time.Microsecond))
	})
}
