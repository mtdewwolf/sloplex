package server

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestWebApp(t *testing.T) {
	f := newFixture(t, Options{})
	noRedirect := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	get := func(path string) *http.Response {
		t.Helper()
		resp, err := noRedirect.Get(f.srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { resp.Body.Close() })
		return resp
	}

	resp := get("/web/")
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || !strings.Contains(string(body), `<script src="app.js" defer>`) {
		t.Fatalf("/web/ = %s\n%s", resp.Status, body)
	}
	for k, want := range map[string]string{
		"Content-Security-Policy": "script-src 'self'",
		"X-Content-Type-Options":  "nosniff",
		"Referrer-Policy":         "no-referrer",
	} {
		if !strings.Contains(resp.Header.Get(k), want) {
			t.Errorf("%s = %q, want %q", k, resp.Header.Get(k), want)
		}
	}
	if resp := get("/web"); resp.StatusCode != 301 || resp.Header.Get("Location") != "/web/" {
		t.Errorf("/web = %s -> %q", resp.Status, resp.Header.Get("Location"))
	}
	if resp := get("/web/app.js"); resp.StatusCode != 200 || !strings.Contains(resp.Header.Get("Content-Type"), "javascript") {
		t.Errorf("app.js = %s %q", resp.Status, resp.Header.Get("Content-Type"))
	}
	if resp := get("/web/missing.js"); resp.StatusCode != 404 {
		t.Errorf("missing file = %s", resp.Status)
	}
	// The public prefix must not open a path to authenticated data.
	for _, p := range []string{"/web/../library/sections", "/web/%2e%2e/library/sections", "/web/..%2flibrary/sections"} {
		if resp := get(p); resp.StatusCode == 200 {
			t.Errorf("%s = 200 without a token", p)
		}
	}
}
