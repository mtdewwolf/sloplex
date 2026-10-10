package library

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

// collectionFixture: a Movies section where Heat and Thief share the .nfo
// set "Michael Mann" (spelled in two cases), Thief is also in "Neo-noir",
// and Ronin has no .nfo; plus a TV section with one show.
func collectionFixture(t *testing.T, storePath string) (s *Store, sc *Scanner, movies, tv string) {
	t.Helper()
	root := t.TempDir()
	movies, tv = filepath.Join(root, "movies"), filepath.Join(root, "tv")
	writeFile(t, filepath.Join(movies, "Heat (1995).mkv"), "x")
	writeFile(t, filepath.Join(movies, "Heat (1995).nfo"), `<movie><title>Heat</title><set>Michael Mann</set></movie>`)
	writeFile(t, filepath.Join(movies, "Thief (1981).mkv"), "x")
	writeFile(t, filepath.Join(movies, "Thief (1981).nfo"), `<movie><title>Thief</title><set><name>MICHAEL MANN</name></set><set>Neo-noir</set></movie>`)
	writeFile(t, filepath.Join(movies, "Ronin (1998).mkv"), "x")
	writeFile(t, filepath.Join(tv, "Lost", "Lost - S01E01.mkv"), "x")
	s, err := Open(storePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SyncSections([]SectionDef{
		{Title: "Movies", Type: TypeMovie, Paths: []string{movies}},
		{Title: "TV", Type: TypeShow, Paths: []string{tv}},
	}, t0); err != nil {
		t.Fatal(err)
	}
	sc = NewScanner(s, &countingProber{}, nil)
	for _, sec := range s.Sections() {
		if _, err := sc.Scan(context.Background(), sec.ID); err != nil {
			t.Fatal(err)
		}
	}
	return s, sc, movies, tv
}

func movieIDs(s *Store, sec int) map[string]int {
	out := map[string]int{}
	for _, it := range s.Items(sec, TypeMovie) {
		out[it.Title] = it.ID
	}
	return out
}

func TestNFOCollections(t *testing.T) {
	s, sc, movies, _ := collectionFixture(t, "")
	sec := s.Sections()[0].ID
	ids := movieIDs(s, sec)

	cols := s.Collections(sec)
	if len(cols) != 2 || cols[0].Title != "Michael Mann" || cols[1].Title != "Neo-noir" {
		t.Fatalf("collections = %+v", cols)
	}
	mann := cols[0]
	if mann.Source != CollectionNFO || !slices.Equal(mann.ItemIDs, []int{ids["Heat"], ids["Thief"]}) {
		t.Errorf("Michael Mann = %+v (ids %v)", mann, ids)
	}
	for _, id := range ids {
		if id == mann.ID || id == cols[1].ID {
			t.Errorf("collection ID %d collides with an item", id)
		}
	}
	if got := s.CollectionTitles(ids["Thief"]); !slices.Equal(got, []string{"Michael Mann", "Neo-noir"}) {
		t.Errorf("Thief's collections = %v", got)
	}
	if got := s.CollectionTitles(ids["Ronin"]); len(got) != 0 {
		t.Errorf("Ronin's collections = %v", got)
	}

	// Automatic collections are read-only.
	if _, err := s.AddToCollection(mann.ID, []int{ids["Ronin"]}, t0); !errors.Is(err, ErrReadOnly) {
		t.Errorf("add to nfo collection: %v", err)
	}
	if err := s.DeleteCollection(mann.ID); !errors.Is(err, ErrReadOnly) {
		t.Errorf("delete nfo collection: %v", err)
	}

	// A rescan keeps the ID; Ronin joins in different case; Neo-noir's only
	// member leaves its set, so it goes away.
	writeFile(t, filepath.Join(movies, "Ronin (1998).nfo"), `<movie><title>Ronin</title><set>michael mann</set></movie>`)
	writeFile(t, filepath.Join(movies, "Thief (1981).nfo"), `<movie><title>Thief</title><set>Michael Mann</set></movie>`)
	sc.now = func() time.Time { return t0.Add(time.Hour) }
	if _, err := sc.Scan(context.Background(), sec); err != nil {
		t.Fatal(err)
	}
	cols = s.Collections(sec)
	if len(cols) != 1 || cols[0].ID != mann.ID || cols[0].Title != "Michael Mann" || len(cols[0].ItemIDs) != 3 {
		t.Fatalf("after rescan = %+v", cols)
	}
	if cols[0].UpdatedAt == mann.UpdatedAt {
		t.Error("membership changed but updatedAt did not")
	}
	// An unchanged rescan does not touch updatedAt.
	before := cols[0].UpdatedAt
	sc.now = func() time.Time { return t0.Add(2 * time.Hour) }
	if _, err := sc.Scan(context.Background(), sec); err != nil {
		t.Fatal(err)
	}
	if s.Collections(sec)[0].UpdatedAt != before {
		t.Error("unchanged collection got a new updatedAt")
	}
}

func TestManualCollections(t *testing.T) {
	path := filepath.Join(t.TempDir(), "library.json")
	s, sc, movies, _ := collectionFixture(t, path)
	moviesSec, tvSec := s.Sections()[0].ID, s.Sections()[1].ID
	ids := movieIDs(s, moviesSec)
	show := s.Items(tvSec, TypeShow)[0]
	episode := s.Leaves(show.ID)[0]

	// Members must be movies of a movie section, shows of a show section.
	for name, c := range map[string]struct {
		sec int
		ids []int
	}{
		"show in movies":    {moviesSec, []int{show.ID}},
		"movie in tv":       {tvSec, []int{ids["Heat"]}},
		"episode in tv":     {tvSec, []int{episode.ID}},
		"unknown item":      {moviesSec, []int{999999}},
		"collection member": {moviesSec, []int{s.Collections(moviesSec)[0].ID}},
	} {
		if _, err := s.CreateCollection(c.sec, "X", c.ids, t0); !errors.Is(err, ErrBadMember) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	if _, err := s.CreateCollection(424242, "X", nil, t0); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown section: %v", err)
	}

	c, err := s.CreateCollection(moviesSec, "The Best", []int{ids["Ronin"], ids["Heat"], ids["Ronin"]}, t0)
	if err != nil {
		t.Fatal(err)
	}
	if c.Source != CollectionManual || !slices.Equal(c.ItemIDs, []int{ids["Ronin"], ids["Heat"]}) {
		t.Errorf("created = %+v", c)
	}
	if shows, err := s.CreateCollection(tvSec, "Shows", []int{show.ID}, t0); err != nil || len(shows.ItemIDs) != 1 {
		t.Errorf("show collection = %+v, %v", shows, err)
	}

	later := t0.Add(time.Minute)
	if c, err = s.AddToCollection(c.ID, []int{ids["Heat"], ids["Thief"]}, later); err != nil || len(c.ItemIDs) != 3 || c.UpdatedAt != later.Unix() {
		t.Errorf("add = %+v, %v", c, err)
	}
	if _, err := s.RemoveFromCollection(c.ID, ids["Heat"], later); err != nil {
		t.Error(err)
	}
	if _, err := s.RemoveFromCollection(c.ID, ids["Heat"], later); !errors.Is(err, ErrNotFound) {
		t.Errorf("remove a non-member: %v", err)
	}
	if c, err = s.RenameCollection(c.ID, "Favourites", later); err != nil || c.Title != "Favourites" {
		t.Errorf("rename = %+v, %v", c, err)
	}
	if got := s.CollectionTitles(ids["Thief"]); !slices.Equal(got, []string{"Favourites", "Michael Mann", "Neo-noir"}) {
		t.Errorf("Thief's collections = %v", got)
	}

	// The member cap counts distinct members.
	old := MaxCollectionItems
	MaxCollectionItems = 3
	if _, err := s.AddToCollection(c.ID, []int{ids["Heat"], ids["Heat"], ids["Thief"]}, later); err != nil {
		t.Errorf("3 distinct members within a cap of 3: %v", err)
	}
	MaxCollectionItems = 2
	if _, err := s.AddToCollection(c.ID, nil, later); !errors.Is(err, ErrBadMember) {
		t.Errorf("over the cap: %v", err)
	}
	if _, err := s.CreateCollection(moviesSec, "Big", []int{ids["Heat"], ids["Thief"], ids["Ronin"]}, later); !errors.Is(err, ErrBadMember) {
		t.Errorf("create over the cap: %v", err)
	}
	MaxCollectionItems = old

	// Members that leave the library are dropped at the next scan; the
	// collection survives, and so does everything across a reload.
	if err := os.Remove(filepath.Join(movies, "Ronin (1998).mkv")); err != nil {
		t.Fatal(err)
	}
	if _, err := sc.Scan(context.Background(), moviesSec); err != nil {
		t.Fatal(err)
	}
	reloaded, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	got, err := reloaded.Collection(c.ID)
	if err != nil || got.Title != "Favourites" || !slices.Equal(got.ItemIDs, []int{ids["Thief"], ids["Heat"]}) {
		t.Errorf("after rescan and reload = %+v, %v", got, err)
	}
	if titles := reloaded.CollectionTitles(ids["Heat"]); !slices.Contains(titles, "Favourites") {
		t.Errorf("reloaded index = %v", titles)
	}

	if err := s.DeleteCollection(c.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Collection(c.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("deleted collection: %v", err)
	}
	if got := s.CollectionTitles(ids["Thief"]); slices.Contains(got, "Favourites") {
		t.Errorf("deleted collection still indexed: %v", got)
	}

	// Removing a section from the config removes its collections.
	if err := s.SyncSections([]SectionDef{{Title: "TV", Type: TypeShow, Paths: s.Sections()[1].Paths}}, t0); err != nil {
		t.Fatal(err)
	}
	if n := len(s.Collections(moviesSec)); n != 0 {
		t.Errorf("collections of a removed section: %d", n)
	}
	if n := len(s.Collections(tvSec)); n != 1 {
		t.Errorf("TV collections = %d", n)
	}
}
