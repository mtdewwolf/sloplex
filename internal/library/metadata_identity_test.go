package library

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestScanShowMetadataPreservesHierarchyIDs(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		t.Run(map[bool]string{false: "current-store", true: "legacy-store"}[legacy], func(t *testing.T) {
			testScanShowMetadataPreservesHierarchyIDs(t, legacy)
		})
	}
}

func testScanShowMetadataPreservesHierarchyIDs(t *testing.T, legacy bool) {
	root := t.TempDir()
	showDir := filepath.Join(root, "Folder Show (2001)")
	writeFile(t, filepath.Join(showDir, "Season 01", "Show - S01E01.mkv"), "x")
	writeFile(t, filepath.Join(showDir, "Season 02", "Show - S02E01.mkv"), "x")
	nfo := filepath.Join(showDir, "tvshow.nfo")
	writeFile(t, nfo, `<tvshow><title>Display Title</title><year>2007</year></tvshow>`)
	path := filepath.Join(t.TempDir(), "library.json")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SyncSections([]SectionDef{{Title: "TV", Type: TypeShow, Paths: []string{root}}}, t0); err != nil {
		t.Fatal(err)
	}
	sec := s.Sections()[0].ID
	sc := NewScanner(s, nil, nil)
	sc.now = func() time.Time { return t0 }
	if _, err := sc.Scan(context.Background(), sec); err != nil {
		t.Fatal(err)
	}
	before := s.Items(sec)
	show := s.Items(sec, TypeShow)[0]
	collection, err := s.CreateCollection(sec, "Favorite Shows", []int{show.ID}, t0)
	if err != nil {
		t.Fatal(err)
	}
	if legacy {
		// Version-1 stores predate the optional scanner key. Recover them from
		// existing episode paths, not their overridden show title/year.
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var data map[string]json.RawMessage
		if err := json.Unmarshal(b, &data); err != nil {
			t.Fatal(err)
		}
		var items []map[string]json.RawMessage
		if err := json.Unmarshal(data["items"], &items); err != nil {
			t.Fatal(err)
		}
		for _, it := range items {
			delete(it, "scanKey")
		}
		data["items"], err = json.Marshal(items)
		if err != nil {
			t.Fatal(err)
		}
		b, err = json.Marshal(data)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, b, 0o600); err != nil {
			t.Fatal(err)
		}
		s, err = Open(path)
		if err != nil {
			t.Fatal(err)
		}
		sc = NewScanner(s, nil, nil)
	}
	assertIDs := func(t *testing.T, title string, year int, unchanged bool) {
		t.Helper()
		got := s.Items(sec)
		if len(got) != len(before) {
			t.Fatalf("hierarchy length = %d, want %d", len(got), len(before))
		}
		for _, old := range before {
			it, err := s.Item(old.ID)
			if err != nil || it.ParentID != old.ParentID || it.AddedAt != old.AddedAt {
				t.Errorf("item %d identity changed: %+v, %v", old.ID, it, err)
			}
			if unchanged && it.UpdatedAt != old.UpdatedAt {
				t.Errorf("unchanged item %d timestamp changed", old.ID)
			}
		}
		if got := s.Items(sec, TypeShow); len(got) != 1 || got[0].ID != show.ID || got[0].Title != title || got[0].Year != year {
			t.Errorf("show = %+v, want ID %d, %q (%d)", got, show.ID, title, year)
		}
		if got, err := s.Collection(collection.ID); err != nil || !reflect.DeepEqual(got.ItemIDs, []int{show.ID}) {
			t.Errorf("show collection lost its reference: %+v, %v", got, err)
		}
	}

	sc.now = func() time.Time { return t0.Add(time.Hour) }
	if _, err := sc.Scan(context.Background(), sec); err != nil {
		t.Fatal(err)
	}
	assertIDs(t, "Display Title", 2007, true)

	// Load persisted identities, then change metadata without changing files.
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	sc = NewScanner(s, nil, nil)
	sc.now = func() time.Time { return t0.Add(2 * time.Hour) }
	writeFile(t, nfo, `<tvshow><title>New Display Title</title><year>2013</year></tvshow>`)
	if _, err := sc.Scan(context.Background(), sec); err != nil {
		t.Fatal(err)
	}
	assertIDs(t, "New Display Title", 2013, false)
	if got, _ := s.Item(show.ID); got.UpdatedAt != t0.Add(2*time.Hour).Unix() {
		t.Error("show metadata edit did not update timestamp")
	}

	// Removing the override also changes only presentation, not identity.
	if err := os.Remove(nfo); err != nil {
		t.Fatal(err)
	}
	sc.now = func() time.Time { return t0.Add(3 * time.Hour) }
	if _, err := sc.Scan(context.Background(), sec); err != nil {
		t.Fatal(err)
	}
	assertIDs(t, "Folder Show", 2001, false)

	// The persisted scanner key also survives replacing every episode path.
	// Deliberately absent episodes are removed, but existing containers remain.
	for _, season := range []string{"Season 01", "Season 02"} {
		if err := os.RemoveAll(filepath.Join(showDir, season)); err != nil {
			t.Fatal(err)
		}
	}
	writeFile(t, filepath.Join(showDir, "Season 01", "Show - S01E02.mkv"), "x")
	writeFile(t, filepath.Join(showDir, "Season 02", "Show - S02E02.mkv"), "x")
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	sc = NewScanner(s, nil, nil)
	if _, err := sc.Scan(context.Background(), sec); err != nil {
		t.Fatal(err)
	}
	for _, old := range before {
		if old.Type == TypeShow || old.Type == TypeSeason {
			if got, err := s.Item(old.ID); err != nil || got.ParentID != old.ParentID {
				t.Errorf("container changed after replacing episodes: %+v, %v", got, err)
			}
		}
	}
	if got, err := s.Collection(collection.ID); err != nil || !reflect.DeepEqual(got.ItemIDs, []int{show.ID}) {
		t.Errorf("replaced episodes lost show collection: %+v, %v", got, err)
	}
}
