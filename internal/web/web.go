// Package web serves the built-in web app. See docs/spec/web.md.
package web

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed static
var files embed.FS

const csp = "default-src 'self'; img-src 'self' data:; media-src 'self'; script-src 'self'; " +
	"style-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'"

// Handler serves the app under /web/.
func Handler() http.Handler {
	static, err := fs.Sub(files, "static")
	if err != nil {
		panic(err)
	}
	fileServer := http.StripPrefix("/web", http.FileServerFS(static))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", csp)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Cache-Control", "no-cache")
		fileServer.ServeHTTP(w, r)
	})
}
