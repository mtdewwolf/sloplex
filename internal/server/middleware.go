package server

import (
	"context"
	"crypto/subtle"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"time"

	"github.com/mtdewwolf/sloplex/internal/accounts"
)

const tokenHeader = "X-Plex-Token"

var corsAllowHeaders = "Accept, Content-Type, Range, " +
	"X-Plex-Token, X-Plex-Client-Identifier, X-Plex-Product, X-Plex-Version, " +
	"X-Plex-Platform, X-Plex-Platform-Version, X-Plex-Device, X-Plex-Device-Name, " +
	"X-Plex-Container-Start, X-Plex-Container-Size"

// cors allows browser clients from any origin. Credentials travel in
// headers or query parameters, never cookies, so a wildcard is safe.
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

// principal is who a request is authenticated as.
type principal struct {
	AccountID int
	// TokenID is the issued token used, or 0 for an owner master token or a
	// trusted network.
	TokenID int
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

// resolve returns the principal for a request's token, if valid.
func (a *authenticator) resolve(r *http.Request) (principal, bool) {
	tok := r.Header.Get(tokenHeader)
	if tok == "" {
		tok = r.URL.Query().Get(tokenHeader)
	}
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

func (a *authenticator) wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, ok := a.resolve(r)
		if !ok && a.trustedPeer(r.RemoteAddr) {
			p, ok = principal{AccountID: accounts.OwnerID}, true
		}
		if ok {
			r = r.WithContext(context.WithValue(r.Context(), principalKey{}, p))
		} else if !a.public[r.URL.Path] {
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
