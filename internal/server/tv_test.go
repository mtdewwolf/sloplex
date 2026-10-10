package server

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mtdewwolf/sloplex/internal/library"
)

func newTVFixture(t *testing.T) *fixture {
	t.Helper()
	root := t.TempDir()
	for _, rel := range []string{
		"Lost (2004)/Season 01/Lost - S01E01 - Pilot.mkv",
		"Lost (2004)/Season 01/Lost - S01E02.mkv",
		"Lost (2004)/Season 02/Lost - S02E01 - Man of Science.mkv",
		"Lost (2004)/Specials/Lost - S00E01 - Recap.mkv",
		"The Wire/Season 1/The.Wire.S01E01.The.Target.720p.mkv",
	} {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("0123456789"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	store, err := library.Open("")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SyncSections([]library.SectionDef{{Title: "TV", Type: library.TypeShow, Paths: []string{root}}}, time.Now()); err != nil {
		t.Fatal(err)
	}
	sec := store.Sections()[0].ID
	sc := library.NewScanner(store, fakeProber{}, nil)
	if _, err := sc.Scan(context.Background(), sec); err != nil {
		t.Fatal(err)
	}
	opts := Options{FriendlyName: "test", MachineID: "abc123", Version: "0.0.1", Tokens: []string{testToken}}
	return &fixture{srv: startServer(t, opts, store, sc, ""), store: store, section: sec, root: root}
}

func titles(mc MediaContainer) string {
	var out []string
	for _, m := range mc.Metadata {
		out = append(out, m.Title)
	}
	return strings.Join(out, ",")
}

func TestTVBrowse(t *testing.T) {
	f := newTVFixture(t)

	secs := f.getJSON(t, "/library/sections")
	if d := secs.Directory[0]; d.Type != "show" || d.Scanner != "Sloplex TV Scanner" {
		t.Fatalf("section = %+v", d)
	}

	shows := f.getJSON(t, "/library/sections/1/all")
	if shows.ViewGroup != "show" || titles(shows) != "Lost,The Wire" {
		t.Fatalf("shows = %s (viewGroup %q)", titles(shows), shows.ViewGroup)
	}
	lost := shows.Metadata[0]
	if lost.Type != "show" || lost.Year != 2004 || lost.ChildCount != 3 || lost.LeafCount != 4 ||
		lost.Key != "/library/metadata/"+lost.RatingKey+"/children" || len(lost.Media) != 0 {
		t.Errorf("show = %+v", lost)
	}

	seasons := f.getJSON(t, lost.Key)
	if seasons.ViewGroup != "season" || seasons.Title1 != "TV" || seasons.Title2 != "Lost" ||
		titles(seasons) != "Specials,Season 1,Season 2" {
		t.Fatalf("seasons = %+v", seasons)
	}
	s0, s1 := seasons.Metadata[0], seasons.Metadata[1]
	if s0.Index == nil || *s0.Index != 0 || s1.LeafCount != 2 || s1.ParentRatingKey != lost.RatingKey || s1.ParentTitle != "Lost" {
		t.Errorf("season = %+v", s1)
	}

	eps := f.getJSON(t, s1.Key)
	if eps.ViewGroup != "episode" || eps.Title1 != "Lost" || eps.Title2 != "Season 1" || titles(eps) != "Pilot,Episode 2" {
		t.Fatalf("episodes = %+v", eps)
	}
	e := eps.Metadata[0]
	if e.Type != "episode" || *e.Index != 1 || *e.ParentIndex != 1 || e.ParentRatingKey != s1.RatingKey ||
		e.GrandparentRatingKey != lost.RatingKey || e.GrandparentTitle != "Lost" || e.GrandparentKey != lost.Key[:len(lost.Key)-len("/children")] ||
		e.Duration != 7_200_000 || len(e.Media) != 1 {
		t.Errorf("episode = %+v", e)
	}

	leaves := f.getJSON(t, "/library/metadata/"+lost.RatingKey+"/allLeaves")
	if titles(leaves) != "Recap,Pilot,Episode 2,Man of Science" {
		t.Errorf("allLeaves = %s", titles(leaves))
	}
	paged := f.getJSON(t, "/library/metadata/"+lost.RatingKey+"/allLeaves?X-Plex-Container-Start=1&X-Plex-Container-Size=2")
	if titles(paged) != "Pilot,Episode 2" || *paged.TotalSize != 4 {
		t.Errorf("paged allLeaves = %s", titles(paged))
	}

	if empty := f.getJSON(t, "/library/metadata/"+e.RatingKey+"/children"); empty.Size != 0 {
		t.Errorf("episode children size = %d", empty.Size)
	}
	if resp := f.get(t, "/library/metadata/9999/children", nil); resp.StatusCode != http.StatusNotFound {
		t.Errorf("unknown children: %s", resp.Status)
	}
}

func TestTVTypeFilter(t *testing.T) {
	f := newTVFixture(t)
	eps := f.getJSON(t, "/library/sections/1/all?type=4")
	if eps.ViewGroup != "episode" || titles(eps) != "Recap,Pilot,Episode 2,Man of Science,The Target" {
		t.Errorf("type=4 = %s", titles(eps))
	}
	if s := f.getJSON(t, "/library/sections/1/all?type=season"); s.Size != 4 {
		t.Errorf("type=season size = %d", s.Size)
	}
	if m := f.getJSON(t, "/library/sections/1/all?type=1"); m.Size != 0 {
		t.Errorf("type=1 in show section size = %d", m.Size)
	}
	if ra := f.getJSON(t, "/library/recentlyAdded"); ra.Size != 5 || ra.Metadata[0].Type != "episode" {
		t.Errorf("recentlyAdded = %+v", ra)
	}
}

func TestTVXMLElements(t *testing.T) {
	f := newTVFixture(t)
	b, _ := io.ReadAll(f.get(t, "/library/sections/1/all", nil).Body)
	if !strings.Contains(string(b), `<Directory ratingKey=`) || !strings.Contains(string(b), `type="show"`) {
		t.Errorf("shows not rendered as Directory:\n%s", b)
	}
	b, _ = io.ReadAll(f.get(t, "/library/sections/1/all?type=3", nil).Body)
	if !strings.Contains(string(b), `index="0"`) {
		t.Errorf("season 0 index omitted:\n%s", b)
	}
	b, _ = io.ReadAll(f.get(t, "/library/sections/1/all?type=4", nil).Body)
	if !strings.Contains(string(b), `<Video ratingKey=`) || !strings.Contains(string(b), `parentIndex="0"`) {
		t.Errorf("episodes not rendered as Video:\n%s", b)
	}
}

func TestDateBasedEpisodes(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "The Daily Show", "The Daily Show - 2024-03-15 - Guest.mkv")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	store, _ := library.Open("")
	if err := store.SyncSections([]library.SectionDef{{Title: "TV", Type: library.TypeShow, Paths: []string{root}}}, time.Now()); err != nil {
		t.Fatal(err)
	}
	sc := library.NewScanner(store, fakeProber{}, nil)
	if _, err := sc.Scan(context.Background(), store.Sections()[0].ID); err != nil {
		t.Fatal(err)
	}
	opts := Options{FriendlyName: "test", MachineID: "abc123", Version: "0.0.1", Tokens: []string{testToken}}
	f := &fixture{srv: startServer(t, opts, store, sc, ""), store: store, root: root}

	show := f.getJSON(t, "/library/sections/1/all").Metadata[0]
	seasons := f.getJSON(t, show.Key)
	if titles(seasons) != "2024" || *seasons.Metadata[0].Index != 2024 {
		t.Fatalf("seasons = %+v", seasons.Metadata)
	}
	ep := f.getJSON(t, seasons.Metadata[0].Key).Metadata[0]
	if ep.Title != "Guest" || *ep.Index != 315 || *ep.ParentIndex != 2024 || ep.OriginallyAvailableAt != "2024-03-15" {
		t.Errorf("episode = %+v", ep)
	}
	code, _, body := f.raw(t, ep.Key, testToken)
	if code != 200 || !strings.Contains(string(body), `originallyAvailableAt="2024-03-15"`) {
		t.Errorf("XML episode = %d\n%s", code, body)
	}
}
