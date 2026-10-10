package server

import (
	"context"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mtdewwolf/sloplex/internal/artwork"
	"github.com/mtdewwolf/sloplex/internal/library"
	"github.com/mtdewwolf/sloplex/internal/probe"
)

func writePNG(t *testing.T, path string, w, h int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for i := range img.Pix {
		img.Pix[i] = 0x80
	}
	img.Set(0, 0, color.White)
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
}

// newArtFixture builds a movie and a TV section with artwork. When ffmpeg is
// installed, the second episode and a movie without images, Ronin, are real
// clips so images can be generated.
func newArtFixture(t *testing.T) (*fixture, bool) {
	t.Helper()
	root := t.TempDir()
	movies, tv := filepath.Join(root, "movies"), filepath.Join(root, "tv")
	writePNG(t, filepath.Join(movies, "Heat (1995)", "poster.png"), 300, 450)
	writePNG(t, filepath.Join(tv, "Lost", "poster.png"), 300, 450)
	writePNG(t, filepath.Join(tv, "Lost", "fanart.png"), 64, 36)
	writePNG(t, filepath.Join(tv, "Lost", "banner.png"), 100, 20)
	writePNG(t, filepath.Join(tv, "Lost", "Season 01", "Lost - S01E01-thumb.png"), 64, 36)
	for _, f := range []string{
		filepath.Join(movies, "Heat (1995)", "Heat (1995).mkv"),
		filepath.Join(tv, "Lost", "Season 01", "Lost - S01E01.mkv"),
	} {
		if err := os.MkdirAll(filepath.Dir(f), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	clips := []string{filepath.Join(tv, "Lost", "Season 01", "Lost - S01E02.mkv"), filepath.Join(movies, "Ronin (1998).mkv")}
	ffmpeg, _ := exec.LookPath("ffmpeg")
	for _, c := range clips {
		if ffmpeg == "" {
			break
		}
		if out, err := exec.Command(ffmpeg, "-v", "error", "-f", "lavfi", "-i", "testsrc=size=320x180:duration=1:rate=5",
			"-c:v", "mpeg4", c).CombinedOutput(); err != nil {
			t.Logf("ffmpeg clip failed, disabling generated images: %v %s", err, out)
			ffmpeg = ""
		}
	}
	if ffmpeg == "" {
		for _, c := range clips {
			if err := os.WriteFile(c, []byte("x"), 0o644); err != nil {
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
		{Title: "TV", Type: library.TypeShow, Paths: []string{tv}},
	}, time.Now()); err != nil {
		t.Fatal(err)
	}
	sc := library.NewScanner(store, fakeProber{}, nil)
	for _, sec := range store.Sections() {
		if _, err := sc.Scan(context.Background(), sec.ID); err != nil {
			t.Fatal(err)
		}
	}
	opts := Options{FriendlyName: "test", MachineID: "abc123", Version: "0.0.1", Tokens: []string{testToken}}
	return &fixture{srv: startServer(t, opts, store, sc, ffmpeg), store: store, root: root}, ffmpeg != ""
}

func TestArtworkAttributes(t *testing.T) {
	f, canGrab := newArtFixture(t)
	movies := f.getJSON(t, "/library/sections/1/all").Metadata
	heat, ronin := movies[0], movies[1]
	// Heat has a local poster and, with ffmpeg, a generated background.
	if !strings.HasPrefix(heat.Thumb, "/library/metadata/"+heat.RatingKey+"/thumb/") || heat.Banner != "" ||
		canGrab != strings.HasPrefix(heat.Art, "/library/metadata/"+heat.RatingKey+"/art/") || !canGrab && heat.Art != "" {
		t.Errorf("movie images = %q %q %q with ffmpeg=%v", heat.Thumb, heat.Art, heat.Banner, canGrab)
	}
	if canGrab != (ronin.Thumb != "" && ronin.Art != "") || !canGrab && (ronin.Thumb != "" || ronin.Art != "") {
		t.Errorf("generated movie images = %q %q with ffmpeg=%v", ronin.Thumb, ronin.Art, canGrab)
	}

	lost := f.getJSON(t, "/library/sections/2/all").Metadata[0]
	showThumb, showArt := lost.Thumb, lost.Art
	if showThumb == "" || showArt == "" || lost.Banner == "" {
		t.Fatalf("show images = %+v", lost)
	}
	season := f.getJSON(t, lost.Key).Metadata[0]
	if season.Thumb != showThumb || season.ParentThumb != showThumb || season.Art != showArt {
		t.Errorf("season fallbacks = thumb %q parentThumb %q art %q", season.Thumb, season.ParentThumb, season.Art)
	}

	eps := f.getJSON(t, season.Key).Metadata
	e1, e2 := eps[0], eps[1]
	if !strings.HasPrefix(e1.Thumb, "/library/metadata/"+e1.RatingKey+"/thumb/") {
		t.Errorf("episode own thumb = %q", e1.Thumb)
	}
	if e1.Art != showArt || e1.GrandparentArt != showArt || e1.GrandparentThumb != showThumb || e1.ParentThumb != showThumb {
		t.Errorf("episode inherited images = %+v", e1)
	}
	if canGrab != (e2.Thumb != "") {
		t.Errorf("generated thumb = %q with ffmpeg=%v", e2.Thumb, canGrab)
	}
}

func TestImageEndpoints(t *testing.T) {
	f, canGrab := newArtFixture(t)
	lost := f.getJSON(t, "/library/sections/2/all").Metadata[0]

	resp := f.get(t, lost.Thumb, nil)
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "image/png" || resp.Header.Get("Cache-Control") == "" {
		t.Fatalf("show thumb: %s %v", resp.Status, resp.Header)
	}
	if cfg, err := png.DecodeConfig(resp.Body); err != nil || cfg.Width != 300 {
		t.Errorf("show thumb body: %+v %v", cfg, err)
	}
	// The timestamp segment is optional.
	if resp := f.get(t, "/library/metadata/"+lost.RatingKey+"/banner", nil); resp.StatusCode != 200 {
		t.Errorf("banner without ts: %s", resp.Status)
	}
	// Endpoints never fall back to ancestors.
	season := f.getJSON(t, lost.Key).Metadata[0]
	if resp := f.get(t, "/library/metadata/"+season.RatingKey+"/thumb", nil); resp.StatusCode != 404 {
		t.Errorf("season thumb endpoint: %s", resp.Status)
	}
	if resp := f.get(t, "/library/metadata/9999/thumb", nil); resp.StatusCode != 404 {
		t.Errorf("unknown item thumb: %s", resp.Status)
	}

	if canGrab {
		movies := f.getJSON(t, "/library/sections/1/all").Metadata
		e2 := f.getJSON(t, season.Key).Metadata[1]
		for _, c := range []struct {
			name, url string
			w, h      int
		}{
			{"episode thumb", e2.Thumb, 640, 360},
			{"movie poster", movies[1].Thumb, 600, 900},
			{"movie background", movies[1].Art, 1280, 720},
		} {
			resp := f.get(t, c.url, nil)
			if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "image/jpeg" {
				t.Fatalf("generated %s: %s %v", c.name, resp.Status, resp.Header)
			}
			if cfg, err := jpeg.DecodeConfig(resp.Body); err != nil || cfg.Width != c.w || cfg.Height != c.h {
				t.Errorf("generated %s: %+v %v, want %dx%d", c.name, cfg, err, c.w, c.h)
			}
		}
		// A local poster wins over a generated one; a video ffmpeg cannot
		// read has no generated image.
		if resp := f.get(t, movies[0].Thumb, nil); resp.Header.Get("Content-Type") != "image/png" {
			t.Errorf("Heat poster: %s %v", resp.Status, resp.Header)
		}
		if resp := f.get(t, movies[0].Art, nil); resp.StatusCode != 404 {
			t.Errorf("background of an unreadable video: %s", resp.Status)
		}
	}
}

func TestPhotoTranscode(t *testing.T) {
	f, _ := newArtFixture(t)
	lost := f.getJSON(t, "/library/sections/2/all").Metadata[0]
	transcode := func(params string) *http.Response {
		return f.get(t, "/photo/:/transcode?"+params, nil)
	}
	size := func(resp *http.Response) (int, int) {
		t.Helper()
		if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "image/jpeg" {
			t.Fatalf("transcode: %s %v", resp.Status, resp.Header)
		}
		cfg, err := jpeg.DecodeConfig(resp.Body)
		if err != nil {
			t.Fatal(err)
		}
		return cfg.Width, cfg.Height
	}
	u := url.QueryEscape(lost.Thumb)

	if w, h := size(transcode("width=100&height=100&url=" + u)); w != 67 || h != 100 {
		t.Errorf("fit = %dx%d, want 67x100", w, h)
	}
	if w, h := size(transcode("width=100&height=100&minSize=1&url=" + u)); w != 100 || h != 100 {
		t.Errorf("cover = %dx%d, want 100x100", w, h)
	}
	if w, h := size(transcode("width=600&height=900&url=" + u)); w != 300 || h != 450 {
		t.Errorf("no upscale = %dx%d", w, h)
	}
	// Only the path of an absolute URL is used; the host is never contacted.
	abs := url.QueryEscape("http://192.0.2.1:32400" + lost.Thumb + "?X-Plex-Token=x")
	if w, _ := size(transcode("width=50&height=50&url=" + abs)); w != 33 {
		t.Errorf("absolute url width = %d", w)
	}

	for params, want := range map[string]int{
		"width=100&height=100&url=" + url.QueryEscape("/etc/passwd"):                   400,
		"width=100&height=100&url=" + url.QueryEscape("http://example.com/poster.jpg"): 400,
		"width=0&height=100&url=" + u:                                                  400,
		"width=5000&height=100&url=" + u:                                               400,
		"width=100&height=100":                                                         400,
		"width=100&height=100&url=" + url.QueryEscape("/library/metadata/9999/thumb"):  404,
		"width=100&height=100&url=" + url.QueryEscape("/library/metadata/1/banner/1"):  404,
	} {
		if resp := transcode(params); resp.StatusCode != want {
			t.Errorf("transcode?%s = %d, want %d", params, resp.StatusCode, want)
		}
	}
}

func TestGeneratedSource(t *testing.T) {
	s := &Server{art: artwork.New(t.TempDir(), "ffmpeg")}
	media := []library.Media{{Info: probe.Info{Duration: 7200000, HDR: probe.HDRPQ},
		Parts: []library.Part{{ID: 9, File: "/m/a.mkv", ModTime: 42}}}}
	want := artwork.Source{PartID: 9, ModTime: 42, File: "/m/a.mkv", Duration: 7200000, HDR: probe.HDRPQ}
	for _, c := range []struct {
		typ, kind string
		still     artwork.Still
		ok        bool
	}{
		{library.TypeMovie, library.ImageThumb, artwork.MoviePoster, true},
		{library.TypeMovie, library.ImageArt, artwork.MovieArt, true},
		{library.TypeEpisode, library.ImageThumb, artwork.EpisodeThumb, true},
		{library.TypeEpisode, library.ImageArt, 0, false},
		{library.TypeMovie, library.ImageBanner, 0, false},
		{library.TypeShow, library.ImageThumb, 0, false},
	} {
		still, src, ok := s.generated(library.Item{Type: c.typ, Media: media}, c.kind)
		if ok != c.ok || ok && (still != c.still || src != want) {
			t.Errorf("%s %s: %v %+v %v", c.typ, c.kind, still, src, ok)
		}
	}
	if _, _, ok := s.generated(library.Item{Type: library.TypeMovie}, library.ImageThumb); ok {
		t.Error("a movie without media has a generated image")
	}
	s.art = artwork.New(t.TempDir(), "")
	if _, _, ok := s.generated(library.Item{Type: library.TypeMovie, Media: media}, library.ImageThumb); ok {
		t.Error("a generated image without ffmpeg")
	}
}
