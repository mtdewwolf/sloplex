package server

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mtdewwolf/sloplex/internal/accounts"
	"github.com/mtdewwolf/sloplex/internal/artwork"
	"github.com/mtdewwolf/sloplex/internal/library"
	"github.com/mtdewwolf/sloplex/internal/playstate"
	"github.com/mtdewwolf/sloplex/internal/probe"
	"github.com/mtdewwolf/sloplex/internal/transcode"
)

func mustPlay(t *testing.T) *playstate.Store {
	t.Helper()
	p, err := playstate.Open("")
	if err != nil {
		t.Fatal(err)
	}
	return p
}

const testToken = "secret-token"

type fixture struct {
	srv     *httptest.Server
	store   *library.Store
	section int
	root    string
}

type fakeProber struct{}

func (fakeProber) Probe(_ context.Context, path string) (probe.Info, error) {
	return probe.Info{Container: probe.ContainerForPath(path), Duration: 7_200_000,
		Width: 1920, Height: 1080, VideoCodec: "h264", AudioCodec: "aac", AudioChannels: 2, Bitrate: 8000}, nil
}

func newFixture(t *testing.T, opts Options) *fixture {
	t.Helper()
	root := t.TempDir()
	for _, name := range []string{"The Matrix (1999).mkv", "Alien (1979).mp4", "Heat (1995).mkv"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("0123456789abcdef"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	store, err := library.Open("")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SyncSections([]library.SectionDef{{Title: "Movies", Type: library.TypeMovie, Paths: []string{root}}}, time.Now()); err != nil {
		t.Fatal(err)
	}
	sec := store.Sections()[0].ID
	sc := library.NewScanner(store, fakeProber{}, nil)
	if _, err := sc.Scan(context.Background(), sec); err != nil {
		t.Fatal(err)
	}
	if opts.MachineID == "" {
		opts = Options{FriendlyName: "test", MachineID: "abc123", Version: "0.0.1", Tokens: []string{testToken}, AllowedNetworks: opts.AllowedNetworks}
	}
	return &fixture{srv: startServer(t, opts, store, sc, ""), store: store, section: sec, root: root}
}

// startServer serves a Server over httptest. ffmpeg enables frame grabs.
func startServer(t *testing.T, opts Options, store *library.Store, sc *library.Scanner, ffmpeg string) *httptest.Server {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() { cancel(); sc.Wait() })
	art := artwork.New(filepath.Join(t.TempDir(), "cache"), ffmpeg)
	accts, err := accounts.Open("")
	if err != nil {
		t.Fatal(err)
	}
	tc, err := transcode.New(ffmpeg, filepath.Join(t.TempDir(), "transcode"), 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(tc.Close)
	srv := httptest.NewServer(New(ctx, opts, Deps{Store: store, Scanner: sc, Play: mustPlay(t), Art: art, Accounts: accts, Transcoder: tc}).Handler())
	t.Cleanup(srv.Close)
	return srv
}

func (f *fixture) get(t *testing.T, path string, hdr map[string]string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, f.srv.URL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := hdr[tokenHeader]; !ok {
		req.Header.Set(tokenHeader, testToken)
	}
	for k, v := range hdr {
		if v != "" {
			req.Header.Set(k, v)
		}
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func (f *fixture) getJSON(t *testing.T, path string) MediaContainer {
	t.Helper()
	resp := f.get(t, path, map[string]string{"Accept": "application/json"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s: %s", path, resp.Status)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/json" {
		t.Fatalf("GET %s: Content-Type %q", path, ct)
	}
	var body struct{ MediaContainer MediaContainer }
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	return body.MediaContainer
}

func TestAuth(t *testing.T) {
	f := newFixture(t, Options{})
	if resp := f.get(t, "/", map[string]string{tokenHeader: ""}); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("no token: %s", resp.Status)
	}
	if resp := f.get(t, "/", map[string]string{tokenHeader: "wrong"}); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("bad token: %s", resp.Status)
	}
	if resp := f.get(t, "/?X-Plex-Token="+testToken, map[string]string{tokenHeader: ""}); resp.StatusCode != http.StatusOK {
		t.Errorf("query token: %s", resp.Status)
	}
	if resp := f.get(t, "/identity", map[string]string{tokenHeader: ""}); resp.StatusCode != http.StatusOK {
		t.Errorf("identity without token: %s", resp.Status)
	}
	req, _ := http.NewRequest(http.MethodOptions, f.srv.URL+"/library/sections", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent || resp.Header.Get("Access-Control-Allow-Origin") != "*" {
		t.Errorf("preflight: %s %v", resp.Status, resp.Header)
	}
}

func TestAllowedNetworks(t *testing.T) {
	f := newFixture(t, Options{AllowedNetworks: []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8")}})
	if resp := f.get(t, "/library/sections", map[string]string{tokenHeader: ""}); resp.StatusCode != http.StatusOK {
		t.Errorf("trusted network without token: %s", resp.Status)
	}
}

func TestRootXML(t *testing.T) {
	f := newFixture(t, Options{})
	resp := f.get(t, "/", nil)
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/xml") {
		t.Fatalf("Content-Type = %q", ct)
	}
	b, _ := io.ReadAll(resp.Body)
	if !strings.HasPrefix(string(b), "<?xml") {
		t.Errorf("missing XML declaration: %s", b)
	}
	var mc MediaContainer
	if err := xml.Unmarshal(b, &mc); err != nil {
		t.Fatal(err)
	}
	if mc.MachineIdentifier != "abc123" || mc.FriendlyName != "test" || mc.Size != 1 {
		t.Errorf("root = %+v", mc)
	}
}

func TestSectionsAndBrowse(t *testing.T) {
	f := newFixture(t, Options{})
	secs := f.getJSON(t, "/library/sections")
	if secs.Size != 1 || secs.Directory[0].Type != "movie" || secs.Directory[0].Location[0].Path != f.root {
		t.Fatalf("sections = %+v", secs)
	}

	all := f.getJSON(t, "/library/sections/"+secs.Directory[0].Key+"/all")
	var titles []string
	for _, m := range all.Metadata {
		titles = append(titles, m.Title)
	}
	if got := strings.Join(titles, ","); got != "Alien,Heat,The Matrix" {
		t.Errorf("default sort = %s", got)
	}
	m := all.Metadata[0]
	if m.Type != "movie" || m.Year != 1979 || m.Duration != 7_200_000 || m.LibrarySectionTitle != "Movies" {
		t.Errorf("metadata = %+v", m)
	}
	if m.Media[0].VideoResolution != "1080" || m.Media[0].Part[0].Size != 16 {
		t.Errorf("media = %+v", m.Media[0])
	}

	desc := f.getJSON(t, "/library/sections/1/all?sort=year:desc")
	if desc.Metadata[0].Title != "The Matrix" {
		t.Errorf("year:desc first = %s", desc.Metadata[0].Title)
	}

	paged := f.getJSON(t, "/library/sections/1/all?X-Plex-Container-Start=1&X-Plex-Container-Size=1")
	if paged.Size != 1 || paged.TotalSize == nil || *paged.TotalSize != 3 || paged.Offset == nil || *paged.Offset != 1 || paged.Metadata[0].Title != "Heat" {
		t.Errorf("paged = %+v", paged)
	}

	one := f.getJSON(t, m.Key)
	if one.Size != 1 || one.Metadata[0].RatingKey != m.RatingKey {
		t.Errorf("metadata endpoint = %+v", one)
	}
	if resp := f.get(t, "/library/metadata/9999", nil); resp.StatusCode != http.StatusNotFound {
		t.Errorf("unknown metadata: %s", resp.Status)
	}
	if resp := f.get(t, "/library/sections/42/all", nil); resp.StatusCode != http.StatusNotFound {
		t.Errorf("unknown section: %s", resp.Status)
	}
}

func TestVideoElementInXML(t *testing.T) {
	f := newFixture(t, Options{})
	b, _ := io.ReadAll(f.get(t, "/library/sections/1/all", nil).Body)
	s := string(b)
	if !strings.Contains(s, "<Video ") || !strings.Contains(s, "<Media ") || !strings.Contains(s, "<Part ") {
		t.Errorf("unexpected XML:\n%s", s)
	}
	b, _ = io.ReadAll(f.get(t, "/library/sections", nil).Body)
	if !strings.Contains(string(b), `refreshing="0"`) {
		t.Errorf("refreshing flag not encoded as 0/1:\n%s", b)
	}
}

func TestPartStreaming(t *testing.T) {
	f := newFixture(t, Options{})
	all := f.getJSON(t, "/library/sections/1/all")
	part := all.Metadata[0].Media[0].Part[0]

	resp := f.get(t, part.Key, map[string]string{"Range": "bytes=4-7"})
	if resp.StatusCode != http.StatusPartialContent {
		t.Fatalf("range: %s", resp.Status)
	}
	b, _ := io.ReadAll(resp.Body)
	if string(b) != "4567" {
		t.Errorf("range body = %q", b)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "video/mp4" {
		t.Errorf("Content-Type = %q", ct)
	}

	resp = f.get(t, part.Key+"?download=1", nil)
	if cd := resp.Header.Get("Content-Disposition"); !strings.Contains(cd, "attachment") {
		t.Errorf("Content-Disposition = %q", cd)
	}
	if resp := f.get(t, "/library/parts/9999/0/file.mkv", nil); resp.StatusCode != http.StatusNotFound {
		t.Errorf("unknown part: %s", resp.Status)
	}
}

func TestRefresh(t *testing.T) {
	f := newFixture(t, Options{})
	if err := os.WriteFile(filepath.Join(f.root, "Ronin (1998).mkv"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if resp := f.get(t, "/library/sections/1/refresh", nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("refresh: %s", resp.Status)
	}
	deadline := time.Now().Add(5 * time.Second)
	for len(f.store.Items(f.section)) != 4 {
		if time.Now().After(deadline) {
			t.Fatal("refresh did not pick up new file")
		}
		time.Sleep(10 * time.Millisecond)
	}
	ra := f.getJSON(t, "/library/recentlyAdded")
	if ra.Size != 4 {
		t.Errorf("recentlyAdded size = %d", ra.Size)
	}
}

func TestVideoResolution(t *testing.T) {
	for _, tt := range []struct {
		w, h int
		want string
	}{
		{3840, 2160, "4k"}, {1920, 1080, "1080"}, {1920, 800, "1080"}, {1280, 720, "720"},
		{720, 480, "480"}, {320, 240, "sd"}, {0, 0, ""},
	} {
		if got := videoResolution(tt.w, tt.h); got != tt.want {
			t.Errorf("videoResolution(%d, %d) = %q, want %q", tt.w, tt.h, got, tt.want)
		}
	}
}
