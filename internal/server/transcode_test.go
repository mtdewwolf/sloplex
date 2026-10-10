package server

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/mtdewwolf/sloplex/internal/library"
	"github.com/mtdewwolf/sloplex/internal/probe"
)

// newTranscodeFixture: section 1 Movies with a real MPEG-4/MP2 clip that
// needs transcoding, section 2 TV.
func newTranscodeFixture(t *testing.T) (*fixture, Metadata) {
	t.Helper()
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not installed")
	}
	prober, err := probe.NewFFprobe("")
	if err != nil {
		t.Skip("ffprobe not installed")
	}
	root := t.TempDir()
	movies, tv := filepath.Join(root, "movies"), filepath.Join(root, "tv", "Lost", "Season 1")
	for _, d := range []string{movies, tv} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	clip := filepath.Join(movies, "Clip (2020).mkv")
	if out, err := exec.Command(ffmpeg, "-v", "error", "-f", "lavfi", "-i", "testsrc=size=640x360:rate=24:duration=9",
		"-f", "lavfi", "-i", "sine=duration=9", "-shortest", "-c:v", "mpeg4", "-c:a", "mp2", clip).CombinedOutput(); err != nil {
		t.Skipf("cannot make clip: %v %s", err, out)
	}
	if err := os.WriteFile(filepath.Join(tv, "Lost - S01E01.mkv"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	store, _ := library.Open("")
	if err := store.SyncSections([]library.SectionDef{
		{Title: "Movies", Type: library.TypeMovie, Paths: []string{movies}},
		{Title: "TV", Type: library.TypeShow, Paths: []string{filepath.Join(root, "tv")}},
	}, time.Now()); err != nil {
		t.Fatal(err)
	}
	sc := library.NewScanner(store, prober, nil)
	for _, sec := range store.Sections() {
		if _, err := sc.Scan(context.Background(), sec.ID); err != nil {
			t.Fatal(err)
		}
	}
	opts := Options{FriendlyName: "test", MachineID: "abc123", Version: "0.0.1", Tokens: []string{testToken}}
	f := &fixture{srv: startServer(t, opts, store, sc, ffmpeg), store: store, root: root}
	return f, f.getJSON(t, "/library/sections/1/all").Metadata[0]
}

// raw GETs a path with an optional token and returns status and body.
func (f *fixture) raw(t *testing.T, path, token string) (int, http.Header, []byte) {
	t.Helper()
	req, _ := http.NewRequest("GET", f.srv.URL+path, nil)
	if token != "" {
		req.Header.Set(tokenHeader, token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, resp.Header, b
}

var variantRE = regexp.MustCompile(`(?m)^session/([0-9a-f]{32})/base/index\.m3u8$`)

func TestTranscodeHLS(t *testing.T) {
	f, clip := newTranscodeFixture(t)
	start := "/video/:/transcode/universal/start.m3u8?session=web-1&videoResolution=320x180&path=" + clip.Key

	code, hdr, master := f.raw(t, start, testToken)
	if code != 200 || hdr.Get("Content-Type") != "application/vnd.apple.mpegurl" {
		t.Fatalf("start = %d %q\n%s", code, hdr.Get("Content-Type"), master)
	}
	if !strings.Contains(string(master), "RESOLUTION=320x180") {
		t.Errorf("master playlist:\n%s", master)
	}
	m := variantRE.FindStringSubmatch(string(master))
	if m == nil {
		t.Fatalf("no variant URL in:\n%s", master)
	}
	base := "/video/:/transcode/universal/session/" + m[1] + "/base/"

	// Session URLs work without a token.
	code, _, index := f.raw(t, base+"index.m3u8", "")
	if code != 200 || !strings.Contains(string(index), "00000.ts") {
		t.Fatalf("index = %d\n%s", code, index)
	}
	code, hdr, seg := f.raw(t, base+"00000.ts", "")
	if code != 200 || hdr.Get("Content-Type") != "video/mp2t" || len(seg) < 188 || seg[0] != 0x47 {
		t.Errorf("segment = %d %q, %d bytes", code, hdr.Get("Content-Type"), len(seg))
	}
	for _, p := range []string{
		"/video/:/transcode/universal/session/" + strings.Repeat("0", 32) + "/base/index.m3u8",
		base + "nope.ts",
		base + "..%2findex.m3u8",
	} {
		if code, _, _ := f.raw(t, p, ""); code != 404 && code != 401 {
			t.Errorf("GET %s = %d, want 404 or 401", p, code)
		}
	}

	// Admins can list sessions; others cannot.
	var list struct{ MediaContainer MediaContainer }
	f.api(t, "GET", "/transcode/sessions", testToken, nil, &list)
	if n := len(list.MediaContainer.TranscodeSession); n != 1 {
		t.Fatalf("sessions = %d", n)
	}
	ts := list.MediaContainer.TranscodeSession[0]
	if ts.Key != m[1] || ts.RatingKey != clip.RatingKey || ts.VideoDecision != "transcode" || ts.AudioDecision != "transcode" || ts.Protocol != "hls" {
		t.Errorf("session = %+v", ts)
	}
	f.createUser(t, "alice", "wonderland", false)
	alice := f.login(t, "alice", "wonderland")
	if code, _, _ := f.raw(t, "/transcode/sessions", alice); code != 403 {
		t.Errorf("non-admin sessions = %d", code)
	}

	// Stop removes the session.
	if code, _, _ := f.raw(t, "/video/:/transcode/universal/stop?session=web-1", testToken); code != 200 {
		t.Errorf("stop = %d", code)
	}
	if code, _, _ := f.raw(t, base+"index.m3u8", ""); code != 404 {
		t.Errorf("index after stop = %d", code)
	}
}

func TestTranscodeMP4AndValidation(t *testing.T) {
	f, clip := newTranscodeFixture(t)
	code, hdr, body := f.raw(t, "/video/:/transcode/universal/start.mp4?offset=3&path="+clip.Key, testToken)
	if code != 200 || hdr.Get("Content-Type") != "video/mp4" || len(body) < 1000 || !bytes.Equal(body[4:8], []byte("ftyp")) {
		t.Fatalf("mp4 = %d %q, %d bytes", code, hdr.Get("Content-Type"), len(body))
	}

	show := f.getJSON(t, "/library/sections/2/all").Metadata[0]
	for params, want := range map[string]int{
		"":                             400,
		"path=/library/metadata/abc":   400,
		"path=/library/metadata/99999": 404,
		"path=" + show.Key[:len(show.Key)-len("/children")]: 400,
		"path=" + clip.Key + "&mediaIndex=3":                404,
		"path=" + clip.Key + "&offset=-1":                   400,
		"path=" + clip.Key + "&videoResolution=big":         400,
		"path=" + clip.Key + "&maxVideoBitrate=1":           400,
	} {
		if code, _, _ := f.raw(t, "/video/:/transcode/universal/start.m3u8?"+params, testToken); code != want {
			t.Errorf("start.m3u8?%s = %d, want %d", params, code, want)
		}
	}
	if code, _, _ := f.raw(t, "/video/:/transcode/universal/start.m3u8?path="+clip.Key, ""); code != 401 {
		t.Errorf("start without token = %d", code)
	}

	// Restricted accounts cannot transcode hidden items.
	f.api(t, "POST", "/sloplex/accounts", testToken, map[string]any{"name": "kid", "password": "cartoons1", "libraries": []int{2}}, nil)
	kid := f.login(t, "kid", "cartoons1")
	for _, ext := range []string{"m3u8", "mp4"} {
		if code, _, _ := f.raw(t, "/video/:/transcode/universal/start."+ext+"?path="+clip.Key, kid); code != 404 {
			t.Errorf("kid start.%s = %d", ext, code)
		}
	}
}

func TestTranscodeUnavailable(t *testing.T) {
	f := newFixture(t, Options{}) // no ffmpeg
	heat := f.find(t, "/library/sections/1/all", "Heat")
	if code, _, _ := f.raw(t, "/video/:/transcode/universal/start.m3u8?path="+heat.Key, testToken); code != 503 {
		t.Errorf("without ffmpeg = %d, want 503", code)
	}
}
