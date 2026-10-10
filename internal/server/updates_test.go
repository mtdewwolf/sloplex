package server

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/mtdewwolf/sloplex/internal/accounts"
	"github.com/mtdewwolf/sloplex/internal/library"
	"github.com/mtdewwolf/sloplex/internal/truenas"
	"github.com/mtdewwolf/sloplex/internal/updates"
)

// fakeTrueNAS records what the server asks TrueNAS to do.
type fakeTrueNAS struct {
	mu      sync.Mutex
	tested  []truenas.Settings
	updated []truenas.Settings
	err     error
}

func (f *fakeTrueNAS) Test(_ context.Context, s truenas.Settings) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tested = append(f.tested, s)
	return "RUNNING", f.err
}

func (f *fakeTrueNAS) Update(_ context.Context, s truenas.Settings) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.updated = append(f.updated, s)
	return 42, f.err
}

// newUpdateFixture serves a server whose release lookup goes to a fake
// GitHub announcing latest, and whose TrueNAS is fake.
func newUpdateFixture(t *testing.T, current, latest string) (*fixture, *fakeTrueNAS) {
	t.Helper()
	gh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"tag_name":"v`+latest+`","name":"Sloplex `+latest+`","html_url":"https://example.com/r","published_at":"2026-10-11T12:00:00Z"}`)
	}))
	t.Cleanup(gh.Close)
	store, _ := library.Open("")
	accts, _ := accounts.Open("")
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	s := New(ctx, Options{Version: current, Tokens: []string{testToken}, AutoUpdateCheck: true},
		Deps{Store: store, Scanner: library.NewScanner(store, fakeProber{}, nil), Accounts: accts, Play: mustPlay(t),
			Updates: updates.New(current, gh.URL, nil)})
	tn := &fakeTrueNAS{}
	s.tn = tn
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)
	return &fixture{srv: srv, store: store}, tn
}

type statusView struct {
	Current     string `json:"current"`
	Comparable  bool   `json:"comparable"`
	Latest      string `json:"latest"`
	Available   bool   `json:"available"`
	ReleaseName string `json:"releaseName"`
	ReleaseURL  string `json:"releaseUrl"`
	CheckedAt   int64  `json:"checkedAt"`
	Error       string `json:"error"`
	AutoCheck   bool   `json:"autoCheck"`
	OneClick    bool   `json:"oneClick"`
}

func TestUpdateCheckEndpoints(t *testing.T) {
	f, _ := newUpdateFixture(t, "0.2.0", "0.3.0")
	f.createUser(t, "admin2", "password1", true)
	f.createUser(t, "kid", "password2", false)
	admin, kid := f.login(t, "admin2", "password1"), f.login(t, "kid", "password2")

	var st statusView
	if resp := f.api(t, "GET", "/sloplex/updates", admin, nil, &st); resp.StatusCode != 200 ||
		st.Current != "0.2.0" || !st.Comparable || st.Latest != "" || st.CheckedAt != 0 || !st.AutoCheck || st.OneClick {
		t.Errorf("status before checking: %s %+v", resp.Status, st)
	}
	if resp := f.api(t, "POST", "/sloplex/updates/check", admin, nil, &st); resp.StatusCode != 200 ||
		st.Latest != "0.3.0" || !st.Available || st.ReleaseName != "Sloplex 0.3.0" || st.ReleaseURL != "https://example.com/r" || st.CheckedAt == 0 {
		t.Errorf("check: %s %+v", resp.Status, st)
	}
	for _, c := range []struct{ method, path string }{
		{"GET", "/sloplex/updates"}, {"POST", "/sloplex/updates/check"}, {"POST", "/sloplex/updates/install"},
	} {
		if resp := f.api(t, c.method, c.path, kid, nil, nil); resp.StatusCode != 403 {
			t.Errorf("%s %s as a user: %s", c.method, c.path, resp.Status)
		}
	}
	var e struct{ Error string }
	if resp := f.api(t, "POST", "/sloplex/updates/install", admin, nil, &e); resp.StatusCode != 409 || !strings.Contains(e.Error, "not set up") {
		t.Errorf("install without TrueNAS: %s %q", resp.Status, e.Error)
	}
}

func TestTrueNASEndpoints(t *testing.T) {
	f, tn := newUpdateFixture(t, "0.2.0", "0.3.0")
	f.createUser(t, "admin2", "password1", true)
	admin := f.login(t, "admin2", "password1")
	fp := strings.Repeat("ab", 32)
	settings := map[string]string{"url": "https://10.0.0.38", "app": "sloplex", "apiKey": "the-secret-key", "certSHA256": fp}

	// Only the owner, not other admins.
	for _, c := range []struct {
		method, path string
		body         any
	}{
		{"GET", "/sloplex/truenas", nil}, {"PUT", "/sloplex/truenas", settings},
		{"DELETE", "/sloplex/truenas", nil}, {"POST", "/sloplex/truenas/test", settings},
	} {
		if resp := f.api(t, c.method, c.path, admin, c.body, nil); resp.StatusCode != 403 {
			t.Errorf("%s %s as an admin: %s", c.method, c.path, resp.Status)
		}
	}

	var view trueNASView
	if resp := f.api(t, "GET", "/sloplex/truenas", testToken, nil, &view); resp.StatusCode != 200 || view != (trueNASView{}) {
		t.Errorf("before saving: %s %+v", resp.Status, view)
	}
	var e struct{ Error string }
	for _, bad := range []any{
		map[string]string{"url": "http://10.0.0.38", "app": "sloplex", "apiKey": "k"},
		map[string]string{"url": "https://10.0.0.38", "app": "sloplex"},
		map[string]string{"url": "https://10.0.0.38", "app": "sloplex", "apiKey": "k", "extra": "x"},
		"not json",
	} {
		if resp := f.api(t, "PUT", "/sloplex/truenas", testToken, bad, &e); resp.StatusCode != 400 || e.Error == "" {
			t.Errorf("PUT %v: %s %q", bad, resp.Status, e.Error)
		}
	}

	// Saving, without the key ever coming back.
	resp := f.api(t, "PUT", "/sloplex/truenas", testToken, settings, &view)
	if want := (trueNASView{URL: "https://10.0.0.38", App: "sloplex", CertSHA256: fp, KeySet: true}); resp.StatusCode != 200 || view != want {
		t.Errorf("PUT: %s %+v", resp.Status, view)
	}
	for _, path := range []string{"/sloplex/truenas", "/sloplex/updates"} {
		r := f.get(t, path, map[string]string{"Accept": "application/json"})
		if b, _ := io.ReadAll(r.Body); strings.Contains(string(b), "the-secret-key") {
			t.Errorf("%s returned the key: %s", path, b)
		}
	}
	var st statusView
	if f.api(t, "GET", "/sloplex/updates", admin, nil, &st); !st.OneClick {
		t.Errorf("oneClick after saving: %+v", st)
	}

	// A new address needs the key again, so the stored one cannot be sent
	// elsewhere, neither by saving nor by testing.
	moved := map[string]string{"url": "https://attacker.example", "app": "sloplex", "certSHA256": fp}
	if resp := f.api(t, "PUT", "/sloplex/truenas", testToken, moved, &e); resp.StatusCode != 400 || !strings.Contains(e.Error, "API key again") {
		t.Errorf("moving without the key: %s %q", resp.Status, e.Error)
	}
	if resp := f.api(t, "POST", "/sloplex/truenas/test", testToken, moved, &e); resp.StatusCode != 400 {
		t.Errorf("testing elsewhere without the key: %s", resp.Status)
	}
	if f.api(t, "GET", "/sloplex/truenas", testToken, nil, &view); view.URL != "https://10.0.0.38" {
		t.Errorf("settings changed by a refused request: %+v", view)
	}

	// Testing with the stored key, then a certificate problem.
	var result struct {
		OK          bool   `json:"ok"`
		State       string `json:"state"`
		Error       string `json:"error"`
		Fingerprint string `json:"fingerprint"`
	}
	same := map[string]string{"url": "https://10.0.0.38", "app": "sloplex", "certSHA256": fp}
	if resp := f.api(t, "POST", "/sloplex/truenas/test", testToken, same, &result); resp.StatusCode != 200 || !result.OK || result.State != "RUNNING" {
		t.Errorf("test: %s %+v", resp.Status, result)
	}
	if got := tn.tested[len(tn.tested)-1]; got.APIKey != "the-secret-key" || got.URL != "https://10.0.0.38" {
		t.Errorf("tested with %+v", got)
	}
	tn.err = &truenas.UntrustedError{Fingerprint: strings.Repeat("cd", 32)}
	fresh := map[string]string{"url": "https://nas.lan", "app": "sloplex", "apiKey": "k2"}
	result.OK = true
	if f.api(t, "POST", "/sloplex/truenas/test", testToken, fresh, &result); result.OK || result.Error != "certificate not trusted" || result.Fingerprint != strings.Repeat("cd", 32) {
		t.Errorf("untrusted: %+v", result)
	}
	if f.api(t, "GET", "/sloplex/truenas", testToken, nil, &view); view.URL != "https://10.0.0.38" {
		t.Errorf("testing saved the settings: %+v", view)
	}

	// Installing: refused until a newer release is known, then handed to
	// TrueNAS with the stored settings.
	tn.err = nil
	if resp := f.api(t, "POST", "/sloplex/updates/install", admin, nil, &e); resp.StatusCode != 409 || !strings.Contains(e.Error, "check for updates first") {
		t.Errorf("install before checking: %s %q", resp.Status, e.Error)
	}
	f.api(t, "POST", "/sloplex/updates/check", admin, nil, nil)
	var job struct{ Job int }
	if resp := f.api(t, "POST", "/sloplex/updates/install", admin, nil, &job); resp.StatusCode != 202 || job.Job != 42 {
		t.Errorf("install: %s %+v", resp.Status, job)
	}
	if len(tn.updated) != 1 || tn.updated[0].APIKey != "the-secret-key" || tn.updated[0].App != "sloplex" {
		t.Errorf("TrueNAS asked with %+v", tn.updated)
	}
	tn.err = errors.New("TrueNAS refused the API key")
	if resp := f.api(t, "POST", "/sloplex/updates/install", admin, nil, &e); resp.StatusCode != 502 || e.Error != "TrueNAS refused the API key" {
		t.Errorf("failing install: %s %q", resp.Status, e.Error)
	}

	if resp := f.api(t, "DELETE", "/sloplex/truenas", testToken, nil, nil); resp.StatusCode != 204 {
		t.Errorf("DELETE: %s", resp.Status)
	}
	if f.api(t, "GET", "/sloplex/truenas", testToken, nil, &view); view != (trueNASView{}) {
		t.Errorf("after deleting: %+v", view)
	}
	if resp := f.api(t, "POST", "/sloplex/updates/install", admin, nil, &e); resp.StatusCode != 409 {
		t.Errorf("install after deleting: %s", resp.Status)
	}
}

func TestUpdateInstallUpToDate(t *testing.T) {
	f, tn := newUpdateFixture(t, "0.3.0", "0.3.0")
	f.api(t, "PUT", "/sloplex/truenas", testToken, map[string]string{"url": "https://nas.lan", "app": "sloplex", "apiKey": "k"}, nil)
	f.api(t, "POST", "/sloplex/updates/check", testToken, nil, nil)
	if resp := f.api(t, "POST", "/sloplex/updates/install", testToken, nil, nil); resp.StatusCode != 409 || len(tn.updated) != 0 {
		t.Errorf("install when up to date: %s, TrueNAS asked %d times", resp.Status, len(tn.updated))
	}
}
