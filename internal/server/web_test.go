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
	// A browser opening the bare address lands on the app; apps, and anyone
	// sending a token, still get the server root.
	const browser = "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8"
	for _, c := range []struct {
		method, accept string
		token          bool
		status         int
	}{
		{"GET", browser, false, 302},
		{"HEAD", browser, false, 302},
		{"GET", "application/xml", false, 401},
		{"GET", "", false, 401},
		{"POST", browser, false, 401},
		{"GET", browser, true, 200},
	} {
		req, _ := http.NewRequest(c.method, f.srv.URL+"/", nil)
		if c.accept != "" {
			req.Header.Set("Accept", c.accept)
		}
		if c.token {
			req.Header.Set(tokenHeader, testToken)
		}
		resp, err := noRedirect.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != c.status || (c.status == 302) != (resp.Header.Get("Location") == "/web/") {
			t.Errorf("%s / Accept=%q token=%v: %s -> %q, want %d", c.method, c.accept, c.token, resp.Status, resp.Header.Get("Location"), c.status)
		}
	}
	req, _ := http.NewRequest("GET", f.srv.URL+"/?X-Plex-Token="+testToken, nil)
	req.Header.Set("Accept", browser)
	if resp, err := noRedirect.Do(req); err != nil || resp.StatusCode != 200 {
		t.Errorf("/ from a browser with a query token: %v %v", resp, err)
	} else {
		resp.Body.Close()
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
