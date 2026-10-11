package server

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"testing"
	"time"

	"github.com/mtdewwolf/sloplex/internal/accounts"
	"github.com/mtdewwolf/sloplex/internal/artwork"
	"github.com/mtdewwolf/sloplex/internal/library"
	"github.com/mtdewwolf/sloplex/internal/subtitles"
	"github.com/mtdewwolf/sloplex/internal/transcode"
)

// fakeFFmpeg writes a script that stands in for a long-running ffmpeg: an
// HLS run writes one segment and a playlist listing it, a progressive run
// writes enough to fill the response buffers, and both then wait to be
// killed.
func fakeFFmpeg(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("needs a POSIX shell")
	}
	path := filepath.Join(t.TempDir(), "ffmpeg")
	script := `#!/bin/sh
for a; do last=$a; done
case "$last" in
*/index.m3u8)
	dir=${last%/index.m3u8}
	printf 'G' > "$dir/00000.ts"
	printf '#EXTM3U\n#EXT-X-TARGETDURATION:4\n#EXTINF:4.0,\n00000.ts\n' > "$dir/tmp.m3u8"
	mv "$dir/tmp.m3u8" "$last" ;;
*) head -c 131072 /dev/zero ;;
esac
exec sleep 60
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

type sessionFixture struct {
	*fixture
	accts *accounts.Store
	tc    *transcode.Manager
	clip  Metadata
	other int // a second section
}

// newSessionFixture serves two movie sections, the first holding a clip
// with a sidecar subtitle, with a fake ffmpeg and room for several
// transcodes.
func newSessionFixture(t *testing.T) *sessionFixture {
	t.Helper()
	ffmpeg := fakeFFmpeg(t)
	root := t.TempDir()
	movies, other := filepath.Join(root, "movies"), filepath.Join(root, "other")
	for dir, files := range map[string][]string{
		movies: {"Clip (2020).mkv", "Clip (2020).en.srt"},
		other:  {"Other (2021).mkv"},
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		for _, name := range files {
			body := "0123456789abcdef"
			if filepath.Ext(name) == ".srt" {
				body = "1\n00:00:01,000 --> 00:00:02,000\nHello\n"
			}
			if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	store, err := library.Open("")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SyncSections([]library.SectionDef{
		{Title: "Movies", Type: library.TypeMovie, Paths: []string{movies}},
		{Title: "Other", Type: library.TypeMovie, Paths: []string{other}},
	}, time.Now()); err != nil {
		t.Fatal(err)
	}
	sc := library.NewScanner(store, fakeProber{}, nil)
	for _, sec := range store.Sections() {
		if _, err := sc.Scan(context.Background(), sec.ID); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() { cancel(); sc.Wait() })
	accts, err := accounts.Open("")
	if err != nil {
		t.Fatal(err)
	}
	tc, err := transcode.New(transcode.Options{FFmpeg: ffmpeg, Dir: filepath.Join(t.TempDir(), "transcode"), MaxTranscodes: 4})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(tc.Close)
	opts := Options{FriendlyName: "test", MachineID: "abc123", Version: "0.0.1", Tokens: []string{testToken}, AllowedNetworks: loopback}
	srv := New(ctx, opts, Deps{Store: store, Scanner: sc, Play: mustPlay(t), Accounts: accts, Transcoder: tc,
		Art: artwork.New(filepath.Join(t.TempDir(), "cache"), ""), Subtitles: subtitles.New("", filepath.Join(t.TempDir(), "subs"))})
	hs := httptest.NewServer(srv.Handler())
	t.Cleanup(hs.Close)
	f := &sessionFixture{fixture: &fixture{srv: hs, store: store, root: root}, accts: accts, tc: tc}
	secs := store.Sections()
	f.section, f.other = secs[0].ID, secs[1].ID
	f.clip = f.getJSON(t, "/library/sections/"+strconv.Itoa(f.section)+"/all").Metadata[0]
	return f
}

var subsURLRE = regexp.MustCompile(`URI="(session/[0-9a-f]{32}/subs/\d+/index\.m3u8)"`)

// playback is the session URLs of one HLS start.
type playback struct{ index, segment, subs string }

// startHLS starts an HLS session with token and returns its URLs.
func (f *sessionFixture) startHLS(t *testing.T, token, label string) playback {
	t.Helper()
	code, _, master := f.raw(t, transcodeBase+"/start.m3u8?session="+label+"&path="+f.clip.Key, token)
	if code != 200 {
		t.Fatalf("start %s = %d %s", label, code, master)
	}
	m := variantRE.FindStringSubmatch(string(master))
	s := subsURLRE.FindStringSubmatch(string(master))
	if m == nil || s == nil {
		t.Fatalf("master playlist lacks variant or subtitles:\n%s", master)
	}
	base := transcodeBase + "/session/" + m[1] + "/base/"
	return playback{index: base + "index.m3u8", segment: base + "00000.ts", subs: transcodeBase + "/" + s[1]}
}

// check asserts the status of every URL of a playback, fetched without a
// token as players do.
func (f *sessionFixture) check(t *testing.T, what string, p playback, want int) {
	t.Helper()
	for _, u := range []string{p.index, p.segment, p.subs} {
		if code, _, _ := f.raw(t, u, ""); code != want {
			t.Errorf("%s: GET %s = %d, want %d", what, u, code, want)
		}
	}
}

// running is the number of transcodes the manager still tracks.
func (f *sessionFixture) running() int { return len(f.tc.List()) }

func (f *sessionFixture) tokenID(t *testing.T, plain string) int {
	t.Helper()
	_, tok, ok := f.accts.Lookup(plain)
	if !ok {
		t.Fatal("token not found")
	}
	return tok.ID
}

// HLS session URLs keep working without a token only while the authority
// they were started with holds; see docs/spec/session-authorization.md.
func TestSessionRevocation(t *testing.T) {
	f := newSessionFixture(t)
	alice := f.createUser(t, "alice", "wonderland", false)
	aliceURL := "/sloplex/accounts/" + strconv.Itoa(alice.ID)

	// Master-token, tokenless trusted and other users' sessions survive
	// every revocation below.
	owner := f.startHLS(t, testToken, "owner")
	trusted := f.startHLS(t, "", "trusted")
	f.check(t, "owner session", owner, 200)
	f.check(t, "trusted session", trusted, 200)
	// Session URLs are public: other origins can play them.
	if code := status(f.send(t, "GET", owner.index, "", map[string]string{"Sec-Fetch-Site": "cross-site"})); code != 200 {
		t.Errorf("cross-site session URL = %d", code)
	}

	t.Run("token revoked", func(t *testing.T) {
		tok := f.login(t, "alice", "wonderland")
		p := f.startHLS(t, tok, "a")
		f.check(t, "before", p, 200)
		before := f.running()
		if code := status(f.api(t, "DELETE", "/sloplex/tokens/"+strconv.Itoa(f.tokenID(t, tok)), testToken, nil, nil)); code != 204 {
			t.Fatalf("revoke = %d", code)
		}
		if f.running() != before-1 {
			t.Errorf("transcodes after revocation = %d, want %d", f.running(), before-1)
		}
		f.check(t, "after", p, 404)
		// Another of alice's credentials cannot revive it by label.
		other := f.login(t, "alice", "wonderland")
		f.raw(t, transcodeBase+"/ping?session=a", other)
		f.check(t, "after ping", p, 404)
		f.api(t, "POST", "/sloplex/auth/logout", other, nil, nil)
	})

	t.Run("logout", func(t *testing.T) {
		tok := f.login(t, "alice", "wonderland")
		p := f.startHLS(t, tok, "a")
		f.api(t, "POST", "/sloplex/auth/logout", tok, nil, nil)
		f.check(t, "after logout", p, 404)
	})

	t.Run("password change", func(t *testing.T) {
		keep := f.login(t, "alice", "wonderland")
		lose := f.login(t, "alice", "wonderland")
		kept := f.startHLS(t, keep, "kept")
		lost := f.startHLS(t, lose, "lost")
		if code := status(f.api(t, "PUT", "/sloplex/account/password", keep,
			map[string]string{"current": "wonderland", "new": "looking-glass"}, nil)); code != 204 {
			t.Fatalf("password change = %d", code)
		}
		f.check(t, "session of the kept token", kept, 200)
		f.check(t, "session of a revoked token", lost, 404)
		f.raw(t, transcodeBase+"/stop?session=kept", keep)
		f.api(t, "POST", "/sloplex/auth/logout", keep, nil, nil)
		f.api(t, "PATCH", aliceURL, testToken, map[string]string{"password": "wonderland"}, nil)
	})

	t.Run("library access removed", func(t *testing.T) {
		tok := f.login(t, "alice", "wonderland")
		p := f.startHLS(t, tok, "a")
		f.api(t, "PATCH", aliceURL, testToken, map[string]any{"libraries": []int{f.section, f.other}}, nil)
		f.check(t, "still allowed", p, 200)
		before := f.running()
		f.api(t, "PATCH", aliceURL, testToken, map[string]any{"libraries": []int{f.other}}, nil)
		if f.running() != before-1 {
			t.Errorf("transcodes after ACL change = %d, want %d", f.running(), before-1)
		}
		f.check(t, "after ACL change", p, 404)
		// Restoring access does not revive the stopped session.
		f.api(t, "PATCH", aliceURL, testToken, map[string]any{"libraries": nil}, nil)
		f.check(t, "after restoring access", p, 404)
		f.api(t, "POST", "/sloplex/auth/logout", tok, nil, nil)
	})

	t.Run("checked on every request", func(t *testing.T) {
		// Change the store behind the API's back: the session URL itself
		// rechecks, and stops the session.
		tok := f.login(t, "alice", "wonderland")
		p := f.startHLS(t, tok, "a")
		if err := f.accts.RevokeToken(0, f.tokenID(t, tok)); err != nil {
			t.Fatal(err)
		}
		before := f.running()
		if code, _, _ := f.raw(t, p.segment, ""); code != 404 {
			t.Errorf("segment after silent revocation = %d", code)
		}
		if f.running() != before-1 {
			t.Errorf("transcodes after a refused request = %d, want %d", f.running(), before-1)
		}
		f.check(t, "after silent revocation", p, 404)
	})

	t.Run("account deleted", func(t *testing.T) {
		bob := f.createUser(t, "bob", "wonderland", false)
		tok := f.login(t, "bob", "wonderland")
		p := f.startHLS(t, tok, "b")
		if code := status(f.api(t, "DELETE", "/sloplex/accounts/"+strconv.Itoa(bob.ID), testToken, nil, nil)); code != 204 {
			t.Fatalf("delete = %d", code)
		}
		f.check(t, "after deletion", p, 404)
	})

	f.check(t, "owner session at the end", owner, 200)
	f.check(t, "trusted session at the end", trusted, 200)
}

// A progressive stream ends when its authority is withdrawn mid-play.
func TestProgressiveRevocation(t *testing.T) {
	f := newSessionFixture(t)
	f.createUser(t, "alice", "wonderland", false)
	tok := f.login(t, "alice", "wonderland")
	start := time.Now()
	req, _ := http.NewRequest("GET", f.srv.URL+transcodeBase+"/start.mp4?path="+f.clip.Key, nil)
	req.Header.Set(tokenHeader, tok)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	first := make([]byte, 4)
	if _, err := io.ReadFull(resp.Body, first); resp.StatusCode != 200 || err != nil {
		t.Fatalf("start.mp4 = %d %v", resp.StatusCode, err)
	}
	f.api(t, "DELETE", "/sloplex/tokens/"+strconv.Itoa(f.tokenID(t, tok)), testToken, nil, nil)
	done := make(chan error, 1)
	go func() { _, err := io.Copy(io.Discard, resp.Body); done <- err }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("revoked progressive stream kept running")
	}
	// The fake ffmpeg would otherwise run for a minute.
	if d := time.Since(start); d > 30*time.Second {
		t.Errorf("stream ended after %v, not at revocation", d)
	}
	if n := f.running(); n != 0 {
		t.Errorf("transcodes after revocation = %d", n)
	}
}
