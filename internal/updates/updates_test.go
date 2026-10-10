package updates

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestNewer(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{
		{"0.3.0", "0.2.0", true},
		{"0.10.0", "0.9.9", true},
		{"1.0.0", "0.99.99", true},
		{"0.2.1", "0.2.0", true},
		{"0.2.0", "0.2.0", false},
		{"0.1.9", "0.2.0", false},
		{"0.3.0", "0.3.0-rc.1", true},
		{"0.3.0-rc.1", "0.3.0", false},
		{"0.3.0-rc.2", "0.3.0-rc.1", true},
		{"0.3.0-rc.10", "0.3.0-rc.9", true},
		{"0.3.0-rc.1", "0.3.0-beta.9", true},
		{"0.3.0-rc.1.1", "0.3.0-rc.1", true},
		{"0.3.0-1", "0.3.0-alpha", false},
		{"0.3.0", "0.1.0-dev", true},
		{"0.3.0", "container-dev", false},
		{"0.3.0", "8699d6e027cdaa677e9207ff37f35f00a7d5d5c5", false},
		{"v0.3.0", "0.2.0", false}, // callers strip the v
		{"01.0.0", "0.2.0", false},
	} {
		if got := newer(c.a, c.b); got != c.want {
			t.Errorf("newer(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

// fakeGitHub serves a release lookup and counts requests.
func fakeGitHub(t *testing.T, status int, body *string) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n.Add(1)
		if r.Header.Get("Accept") != "application/vnd.github+json" || !strings.HasPrefix(r.Header.Get("User-Agent"), "sloplex/") {
			t.Errorf("headers: %v", r.Header)
		}
		w.WriteHeader(status)
		w.Write([]byte(*body))
	}))
	t.Cleanup(srv.Close)
	return srv, &n
}

const releaseJSON = `{"tag_name":"v0.3.0","name":"Sloplex 0.3.0","html_url":"https://example.com/v0.3.0",` +
	`"published_at":"2026-10-11T12:00:00Z","body":"notes","prerelease":false}`

func TestCheck(t *testing.T) {
	body := releaseJSON
	srv, calls := fakeGitHub(t, 200, &body)
	c := New("0.2.0", srv.URL, nil)
	now := time.Unix(1_800_000_000, 0)
	c.now = func() time.Time { return now }

	if s := c.Status(); s.Current != "0.2.0" || !s.Comparable || s.CheckedAt != 0 || s.Latest != "" || s.Available {
		t.Errorf("before checking: %+v", s)
	}
	s := c.Check(context.Background())
	want := Status{Current: "0.2.0", Comparable: true, Latest: "0.3.0", Available: true, ReleaseName: "Sloplex 0.3.0",
		ReleaseURL: "https://example.com/v0.3.0", PublishedAt: 1791720000, CheckedAt: now.Unix()}
	if s != want {
		t.Errorf("check:\n got %+v\nwant %+v", s, want)
	}

	// Within a minute, the last result is returned without asking again.
	body = `{"tag_name":"v0.4.0"}`
	now = now.Add(59 * time.Second)
	if s := c.Check(context.Background()); s.Latest != "0.3.0" || calls.Load() != 1 {
		t.Errorf("within a minute: %+v after %d calls", s, calls.Load())
	}
	now = now.Add(time.Second)
	if s := c.Check(context.Background()); s.Latest != "0.4.0" || calls.Load() != 2 {
		t.Errorf("after a minute: %+v after %d calls", s, calls.Load())
	}
}

func TestCheckErrors(t *testing.T) {
	body := releaseJSON
	status := 200
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n.Add(1)
		w.WriteHeader(status)
		w.Write([]byte(body))
	}))
	defer srv.Close()
	c := New("0.3.0", srv.URL, nil)
	now := time.Unix(1_800_000_000, 0)
	c.now = func() time.Time { return now }
	if s := c.Check(context.Background()); s.Available || s.Latest != "0.3.0" || s.Error != "" {
		t.Errorf("same version: %+v", s)
	}
	for _, e := range []struct {
		status int
		body   string
		want   string
	}{
		{404, `{}`, "no release has been published"},
		{500, ``, "GitHub answered 500"},
		{200, `{"tag_name":`, "reading the release"},
		{200, `{"tag_name":"nightly"}`, "unexpected tag"},
	} {
		status, body = e.status, e.body
		now = now.Add(time.Minute)
		s := c.Check(context.Background())
		// A failed check keeps the last good result.
		if !strings.Contains(s.Error, e.want) || s.Latest != "0.3.0" || s.CheckedAt != now.Unix() {
			t.Errorf("%d %q: %+v, want error %q", e.status, e.body, s, e.want)
		}
	}
	srv.Close()
	now = now.Add(time.Minute)
	if s := c.Check(context.Background()); !strings.Contains(s.Error, "could not reach GitHub") {
		t.Errorf("unreachable: %+v", s)
	}

}

// TestCheckNotNewer covers servers that are ahead of the latest release or
// are not releases at all: neither is offered the release.
func TestCheckNotNewer(t *testing.T) {
	body := releaseJSON // 0.3.0
	srv, _ := fakeGitHub(t, 200, &body)
	for _, current := range []string{"0.4.0", "0.3.1-rc.1", "0.1.0-dev+abc", "container-dev"} {
		c := New(current, srv.URL, nil)
		if s := c.Check(context.Background()); s.Available || s.Latest != "0.3.0" || s.Error != "" {
			t.Errorf("%s: %+v", current, s)
		}
	}
	if s := New("0.1.0-dev+abc", srv.URL, nil).Status(); s.Comparable {
		t.Errorf("a development build is comparable: %+v", s)
	}
}

func TestRun(t *testing.T) {
	body := releaseJSON
	srv, calls := fakeGitHub(t, 200, &body)
	c := New("0.2.0", srv.URL, nil)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { c.Run(ctx, 10*time.Millisecond, time.Hour); close(done) }()
	deadline := time.Now().Add(5 * time.Second)
	for calls.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	<-done
	if calls.Load() != 1 || !c.Status().Available {
		t.Errorf("Run made %d calls; status %+v", calls.Load(), c.Status())
	}
}
