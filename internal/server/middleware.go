package server

import (
	"crypto/subtle"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"time"
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

// authenticator checks local access tokens. See docs/spec/auth.md.
type authenticator struct {
	tokens   [][]byte
	networks []netip.Prefix
	public   map[string]bool
}

func newAuthenticator(tokens []string, networks []netip.Prefix) *authenticator {
	a := &authenticator{networks: networks, public: map[string]bool{"/identity": true}}
	for _, t := range tokens {
		if t != "" {
			a.tokens = append(a.tokens, []byte(t))
		}
	}
	return a
}

func (a *authenticator) allowed(r *http.Request) bool {
	if a.public[r.URL.Path] {
		return true
	}
	if a.trustedPeer(r.RemoteAddr) {
		return true
	}
	tok := r.Header.Get(tokenHeader)
	if tok == "" {
		tok = r.URL.Query().Get(tokenHeader)
	}
	if tok == "" {
		return false
	}
	ok := 0
	for _, t := range a.tokens {
		ok |= subtle.ConstantTimeCompare([]byte(tok), t)
	}
	return ok == 1
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
		if !a.allowed(r) {
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
