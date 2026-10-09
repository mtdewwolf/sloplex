package library

import (
	"path/filepath"
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

	// Rescan: Beta unchanged, Alpha gone, Gamma new.
	b := file("/m/b.mkv", "Beta", 2001)
	b.Changed = false
	stats, err = s.ApplyScan(sec, []ScannedFile{b, file("/m/c.mkv", "Gamma", 2002)}, t0.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if stats.Added != 1 || stats.Removed != 1 || stats.Updated != 0 {
		t.Fatalf("stats = %+v, want 1 added, 1 removed, 0 updated", stats)
	}
	items := s.Items(sec)
	if len(items) != 2 {
		t.Fatalf("got %d items, want 2", len(items))
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
	p, err := s2.Part(want.Media[0].Parts[0].ID)
	if err != nil || p.File != "/m/a.mkv" {
		t.Errorf("Part = %+v, %v", p, err)
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
