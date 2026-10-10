package server

import (
	"context"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mtdewwolf/sloplex/internal/library"
)

// newSearchFixture: section 1 Movies, section 2 TV.
func newSearchFixture(t *testing.T) *fixture {
	t.Helper()
	root := t.TempDir()
	movies, tv := filepath.Join(root, "movies"), filepath.Join(root, "tv")
	for _, f := range []string{
		"movies/The Matrix (1999).mkv",
		"movies/The Matrix Reloaded (2003).mkv",
		"movies/Amélie (2001).mkv",
		"movies/Heat (1995).mkv",
		"tv/Lost/Season 01/Lost - S01E01 - Pilot.mkv",
		"tv/The Wire/Season 1/The Wire - S01E01 - The Target.mkv",
	} {
		p := filepath.Join(root, filepath.FromSlash(f))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
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
	return &fixture{srv: startServer(t, opts, store, sc, ""), store: store, root: root}
}

func hubSummary(mc MediaContainer) string {
	var parts []string
	for _, h := range mc.Hub {
		var titles []string
		for _, m := range h.Metadata {
			titles = append(titles, m.Title)
		}
		more := ""
		if h.More {
			more = "+"
		}
		parts = append(parts, h.HubIdentifier+":"+strings.Join(titles, ",")+more)
	}
	return strings.Join(parts, " | ")
}

func TestHubSearch(t *testing.T) {
	f := newSearchFixture(t)
	hubs := func(params string) string {
		t.Helper()
		return hubSummary(f.getJSON(t, "/hubs/search?"+params))
	}
	for params, want := range map[string]string{
		"query=matrix":                  "movie:The Matrix,The Matrix Reloaded",
		"query=MATR":                    "movie:The Matrix,The Matrix Reloaded",
		"query=reloaded+matrix":         "movie:The Matrix Reloaded",
		"query=matrix+2003":             "movie:The Matrix Reloaded",
		"query=amelie":                  "movie:Amélie",
		"query=lost":                    "show:Lost | episode:Pilot",
		"query=lost+pilot":              "episode:Pilot",
		"query=the":                     "movie:The Matrix,The Matrix Reloaded | show:The Wire | episode:The Target",
		"query=matrix&limit=1":          "movie:The Matrix+",
		"query=matrix&sectionId=2":      "",
		"query=target&sectionId=2":      "episode:The Target",
		"query=" + url.QueryEscape("é"): "",
	} {
		if got := hubs(params); got != want {
			t.Errorf("/hubs/search?%s = %q, want %q", params, got, want)
		}
	}

	b, _ := io.ReadAll(f.get(t, "/hubs/search?query=matrix&limit=1", nil).Body)
	if s := string(b); !strings.Contains(s, `<Hub hubIdentifier="movie" type="movie" title="Movies" size="1" more="1">`) || !strings.Contains(s, "<Video ") {
		t.Errorf("hub XML:\n%s", s)
	}

	for params, want := range map[string]int{
		"":                                  400,
		"query=" + strings.Repeat("a", 201): 400,
		"query=%3F%3F":                      400,
		"query=x&limit=0":                   400,
		"query=x&limit=101":                 400,
		"query=x&sectionId=42":              404,
	} {
		if resp := f.get(t, "/hubs/search?"+params, nil); resp.StatusCode != want {
			t.Errorf("/hubs/search?%s = %d, want %d", params, resp.StatusCode, want)
		}
	}
}

func TestFlatAndSectionSearch(t *testing.T) {
	f := newSearchFixture(t)
	for path, want := range map[string]string{
		"/search?query=the":                              "The Matrix,The Matrix Reloaded,The Target,The Wire",
		"/search?query=pilot&type=4":                     "Pilot",
		"/search?query=pilot&type=movie":                 "",
		"/search?query=season&type=3":                    "Season 1,Season 1",
		"/search?query=the&X-Plex-Container-Size=2":      "The Matrix,The Matrix Reloaded",
		"/library/sections/1/search?query=heat":          "Heat",
		"/library/sections/2/search?query=heat":          "",
		"/library/sections/2/search?query=wire":          "The Wire,The Target",
		"/library/sections/2/search?query=wire&type=1":   "",
		"/library/sections/2/search?query=season&type=3": "Season 1,Season 1",
	} {
		if got := titles(f.getJSON(t, path)); got != want {
			t.Errorf("%s = %q, want %q", path, got, want)
		}
	}
	if mc := f.getJSON(t, "/search?query=the&X-Plex-Container-Size=2"); mc.TotalSize == nil || *mc.TotalSize != 4 {
		t.Errorf("paged search totalSize = %v", mc.TotalSize)
	}
	if resp := f.get(t, "/library/sections/9/search?query=x", nil); resp.StatusCode != 404 {
		t.Errorf("unknown section search = %d", resp.StatusCode)
	}
}

func TestSearchRespectsRestrictions(t *testing.T) {
	f := newSearchFixture(t)
	if resp := f.api(t, "POST", "/sloplex/accounts", testToken,
		map[string]any{"name": "kid", "password": "cartoons1", "libraries": []int{2}}, nil); resp.StatusCode != 201 {
		t.Fatalf("create kid: %s", resp.Status)
	}
	tok := f.login(t, "kid", "cartoons1")
	var mc struct{ MediaContainer MediaContainer }
	f.api(t, "GET", "/hubs/search?query=the", tok, nil, &mc)
	if got := hubSummary(mc.MediaContainer); got != "show:The Wire | episode:The Target" {
		t.Errorf("kid hubs = %q", got)
	}
	f.api(t, "GET", "/search?query=matrix", tok, nil, &mc)
	if mc.MediaContainer.Size != 0 {
		t.Errorf("kid flat search found %d hidden items", mc.MediaContainer.Size)
	}
	for _, path := range []string{"/hubs/search?query=matrix&sectionId=1", "/library/sections/1/search?query=matrix"} {
		if resp := f.api(t, "GET", path, tok, nil, nil); resp.StatusCode != 404 {
			t.Errorf("kid %s = %s", path, resp.Status)
		}
	}
}
