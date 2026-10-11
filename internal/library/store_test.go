package library

import (
	"fmt"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/mtdewwolf/sloplex/internal/probe"
)

var t0 = time.Unix(1_700_000_000, 0)

func newStore(t *testing.T, path string) (*Store, int) {
	t.Helper()
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SyncSections([]SectionDef{{Title: "Movies", Type: TypeMovie, Paths: []string{"/m"}}}, t0); err != nil {
		t.Fatal(err)
	}
	return s, s.Sections()[0].ID
}

func file(path, title string, year int) ScannedFile {
	return ScannedFile{Path: path, Size: 100, ModTime: 1, Title: title, Year: year,
		Info: probe.Info{Container: "mkv", Duration: 1000}, Changed: true}
}

func TestApplyScanKeepsIDs(t *testing.T) {
	s, sec := newStore(t, "")
	stats, err := s.ApplyScan(sec, []ScannedFile{
		file("/m/a.mkv", "Alpha", 2000),
		file("/m/b.mkv", "Beta", 2001),
	}, t0)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Added != 2 {
		t.Fatalf("added = %d, want 2", stats.Added)
	}
	before := map[string]Item{}
	for _, it := range s.Items(sec) {
		before[it.Title] = it
	}

	// Apply an explicitly authoritative list: Beta unchanged, Alpha gone,
	// Gamma new. Filesystem scans must establish completeness before calling
	// ApplyScan; see scan-safety.md.
	b := file("/m/b.mkv", "Beta", 2001)
	b.Changed = false
	stats, err = s.ApplyScan(sec, []ScannedFile{b, file("/m/c.mkv", "Gamma", 2002)}, t0.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if stats.Added != 1 || stats.Removed != 0 || stats.Unavailable != 1 || stats.Updated != 0 {
		t.Fatalf("stats = %+v, want 1 added, 1 unavailable, 0 updated", stats)
	}
	items := s.Items(sec)
	if len(items) != 3 {
		t.Fatalf("got %d items, want 3 including trash", len(items))
	}
	for _, it := range items {
		if it.Title != "Beta" {
			continue
		}
		old := before["Beta"]
		if it.ID != old.ID || it.Media[0].ID != old.Media[0].ID || it.Media[0].Parts[0].ID != old.Media[0].Parts[0].ID {
			t.Errorf("Beta IDs changed: %+v -> %+v", old, it)
		}
		if it.UpdatedAt != old.UpdatedAt || it.AddedAt != old.AddedAt {
			t.Errorf("Beta timestamps changed for unchanged file")
		}
	}
	if alpha, err := s.Item(before["Alpha"].ID); err != nil || !alpha.Unavailable {
		t.Fatalf("Alpha not retained unavailable: %+v, %v", alpha, err)
	}
	if _, err := s.EmptyTrash(sec, t0.Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Item(before["Alpha"].ID); err != ErrNotFound {
		t.Errorf("Alpha still present: %v", err)
	}
}

func TestApplyScanGroupsVersions(t *testing.T) {
	s, sec := newStore(t, "")
	if _, err := s.ApplyScan(sec, []ScannedFile{
		file("/m/Heat (1995) 1080p.mkv", "Heat", 1995),
		file("/m/Heat (1995) 4k.mkv", "heat", 1995),
		file("/m/Heat (1986).mkv", "Heat", 1986),
	}, t0); err != nil {
		t.Fatal(err)
	}
	items := s.Items(sec)
	if len(items) != 2 {
		t.Fatalf("got %d items, want 2", len(items))
	}
	for _, it := range items {
		want := 1
		if it.Year == 1995 {
			want = 2
		}
		if len(it.Media) != want {
			t.Errorf("%s (%d): %d media, want %d", it.Title, it.Year, len(it.Media), want)
		}
	}
}

func TestStorePersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "library.json")
	s, sec := newStore(t, path)
	if _, err := s.ApplyScan(sec, []ScannedFile{file("/m/a.mkv", "Alpha", 2000)}, t0); err != nil {
		t.Fatal(err)
	}
	want := s.Items(sec)[0]

	s2, sec2 := newStore(t, path)
	if sec2 != sec {
		t.Fatalf("section ID changed: %d -> %d", sec, sec2)
	}
	got, err := s2.Item(want.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != want.Title || got.Media[0].Parts[0].File != "/m/a.mkv" || got.Media[0].Duration != 1000 {
		t.Errorf("reloaded item = %+v, want %+v", got, want)
	}
	p, secID, err := s2.Part(want.Media[0].Parts[0].ID)
	if err != nil || p.File != "/m/a.mkv" || secID != sec {
		t.Errorf("Part = %+v, section %d, %v", p, secID, err)
	}

	// New IDs must not collide with persisted ones.
	if _, err := s2.ApplyScan(sec, []ScannedFile{file("/m/a.mkv", "Alpha", 2000), file("/m/b.mkv", "Beta", 2001)}, t0); err != nil {
		t.Fatal(err)
	}
	seen := map[int]bool{}
	for _, it := range s2.Items(sec) {
		if seen[it.ID] {
			t.Fatalf("duplicate item ID %d", it.ID)
		}
		seen[it.ID] = true
	}
}

func TestSyncSectionsRemovesItems(t *testing.T) {
	s, sec := newStore(t, "")
	if _, err := s.ApplyScan(sec, []ScannedFile{file("/m/a.mkv", "Alpha", 2000)}, t0); err != nil {
		t.Fatal(err)
	}
	if err := s.SyncSections([]SectionDef{{Title: "Films", Type: TypeMovie, Paths: []string{"/f"}}}, t0); err != nil {
		t.Fatal(err)
	}
	secs := s.Sections()
	if len(secs) != 1 || secs[0].Title != "Films" || secs[0].ID == sec {
		t.Fatalf("sections = %+v", secs)
	}
	if n := len(s.Items(0)); n != 0 {
		t.Errorf("%d orphaned items remain", n)
	}
}

func episodeFile(path, show string, season, ep int, title string) ScannedFile {
	return ScannedFile{Path: path, Size: 100, ModTime: 1, Show: show, Season: season, Episode: ep, Title: title,
		Info: probe.Info{Container: "mkv", Duration: 1000}, Changed: true}
}

func newShowStore(t *testing.T) (*Store, int) {
	t.Helper()
	s, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SyncSections([]SectionDef{{Title: "TV", Type: TypeShow, Paths: []string{"/tv"}}}, t0); err != nil {
		t.Fatal(err)
	}
	return s, s.Sections()[0].ID
}

func TestApplyScanShows(t *testing.T) {
	s, sec := newShowStore(t)
	files := []ScannedFile{
		episodeFile("/tv/Lost/S01E02.mkv", "Lost", 1, 2, "Pilot Part 2"),
		episodeFile("/tv/Lost/S01E01.mkv", "Lost", 1, 1, "Pilot Part 1"),
		episodeFile("/tv/Lost/S01E01.720p.mkv", "Lost", 1, 1, ""),
		episodeFile("/tv/Lost/S02E01.mkv", "Lost", 2, 1, ""),
		episodeFile("/tv/Lost/S00E01.mkv", "Lost", 0, 1, "Special"),
		episodeFile("/tv/Fargo/S01E01.mkv", "Fargo", 1, 1, ""),
	}
	stats, err := s.ApplyScan(sec, files, t0)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Added != 5 || stats.Files != 6 {
		t.Fatalf("stats = %+v, want 5 episodes added from 6 files", stats)
	}
	shows := s.Items(sec, TypeShow)
	if len(shows) != 2 {
		t.Fatalf("shows = %+v", shows)
	}
	var lost Item
	for _, sh := range shows {
		if sh.Title == "Lost" {
			lost = sh
		}
	}
	seasons := s.Children(lost.ID)
	if len(seasons) != 3 || seasons[0].Index != 0 || seasons[0].Title != "Specials" || seasons[2].Title != "Season 2" {
		t.Fatalf("seasons = %+v", seasons)
	}
	if c, l := s.Counts(lost.ID); c != 3 || l != 4 {
		t.Errorf("Counts(show) = %d, %d; want 3, 4", c, l)
	}
	s1 := s.Children(seasons[1].ID)
	if len(s1) != 2 || s1[0].Index != 1 || s1[0].Title != "Pilot Part 1" || len(s1[0].Media) != 2 {
		t.Fatalf("season 1 episodes = %+v", s1)
	}
	if s2 := s.Children(seasons[2].ID); s2[0].Title != "Episode 1" {
		t.Errorf("default episode title = %q", s2[0].Title)
	}
	leaves := s.Leaves(lost.ID)
	var order []int
	for _, l := range leaves {
		p, _ := s.Item(l.ParentID)
		order = append(order, p.Index*100+l.Index)
	}
	if !slices.Equal(order, []int{1, 101, 102, 201}) {
		t.Errorf("leaf order = %v", order)
	}

	// Rescan with S01E02 moved to season 2 and S02E01 removed. Show and
	// season IDs must survive.
	ids := map[string]int{}
	for _, it := range s.Items(sec) {
		ids[fmt.Sprintf("%s/%d/%s", it.Type, it.Index, it.Title)] = it.ID
	}
	unchanged := func(f ScannedFile) ScannedFile { f.Changed = false; return f }
	stats, err = s.ApplyScan(sec, []ScannedFile{
		unchanged(files[1]), unchanged(files[2]), unchanged(files[4]), unchanged(files[5]),
		episodeFile("/tv/Lost/S01E02.mkv", "Lost", 2, 2, "Pilot Part 2"),
	}, t0.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if stats.Added != 0 || stats.Removed != 0 || stats.Unavailable != 1 || stats.Updated != 1 {
		t.Errorf("rescan stats = %+v, want 0 added, 1 unavailable, 1 updated", stats)
	}
	for key, id := range map[string]int{"show/0/Lost": ids["show/0/Lost"], "season/1/Season 1": ids["season/1/Season 1"],
		"season/2/Season 2": ids["season/2/Season 2"], "episode/1/Pilot Part 1": ids["episode/1/Pilot Part 1"]} {
		if _, err := s.Item(id); err != nil {
			t.Errorf("%s (id %d) lost on rescan", key, id)
		}
	}
	moved, err := s.Item(ids["episode/2/Pilot Part 2"])
	if err != nil || moved.ParentID != ids["season/2/Season 2"] {
		t.Errorf("moved episode = %+v, %v", moved, err)
	}
	fargo, _ := s.Item(ids["show/0/Fargo"])
	if fargo.UpdatedAt != t0.Unix() {
		t.Errorf("untouched show updatedAt changed")
	}
}
