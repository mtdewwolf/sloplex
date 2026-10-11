package library

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/mtdewwolf/sloplex/internal/accounts"
	"github.com/mtdewwolf/sloplex/internal/playlists"
	"github.com/mtdewwolf/sloplex/internal/playstate"
)

func TestSyncSectionsLegacyRenameNeedsIdentity(t *testing.T) {
	s, sec := newStore(t, filepath.Join(t.TempDir(), "library.json"))
	if _, err := s.ApplyScan(sec, []ScannedFile{file("/m/a.mkv", "Alpha", 2000)}, t0); err != nil {
		t.Fatal(err)
	}
	before := sectionSnapshot(t, s)
	if err := s.SyncSections([]SectionDef{{Title: "Films", Type: TypeMovie, Paths: []string{"/m"}}}, t0); err == nil {
		t.Error("a legacy title change sharing the existing root silently succeeded")
	}
	assertSectionSnapshot(t, s, before)
}

func sectionSnapshot(t *testing.T, s *Store) []byte {
	t.Helper()
	b, err := json.Marshal(s.d)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func assertSectionSnapshot(t *testing.T, s *Store, before []byte) {
	t.Helper()
	if after := sectionSnapshot(t, s); !bytes.Equal(after, before) {
		t.Error("invalid section synchronization changed in-memory state or counters")
	}
	if s.path != "" {
		b, err := os.ReadFile(s.path)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(b, before) {
			t.Error("invalid section synchronization changed persisted state")
		}
	}
}

// The reference stores are separate fixtures; synchronization must preserve
// the numeric IDs they point at without rewriting any of those stores.
func TestSyncSectionsStableIdentityPreservesReferences(t *testing.T) {
	root := t.TempDir()
	s, err := Open(filepath.Join(root, "library.json"))
	if err != nil {
		t.Fatal(err)
	}
	def := SectionDef{ConfigID: "movies-main", Title: "Movies", Type: TypeMovie, Paths: []string{"/m"}}
	if err := s.SyncSections([]SectionDef{def}, t0); err != nil {
		t.Fatal(err)
	}
	sec := s.Sections()[0]
	if _, err := s.ApplyScan(sec.ID, []ScannedFile{file("/m/a.mkv", "Alpha", 2000)}, t0); err != nil {
		t.Fatal(err)
	}
	item := s.Items(sec.ID)[0]
	c, err := s.CreateCollection(sec.ID, "Favorites", []int{item.ID}, t0)
	if err != nil {
		t.Fatal(err)
	}
	accts, err := accounts.Open(filepath.Join(root, "accounts.json"))
	if err != nil {
		t.Fatal(err)
	}
	a, err := accts.Create("viewer", "", false)
	if err != nil {
		t.Fatal(err)
	}
	allowed := []int{sec.ID}
	if _, err := accts.Update(a.ID, accounts.Update{SetLibraries: true, Libraries: &allowed}); err != nil {
		t.Fatal(err)
	}
	play, err := playstate.Open(filepath.Join(root, "playstate.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := play.Update(a.ID, []int{item.ID}, func(st *playstate.State) { st.ViewOffset = 1234 }); err != nil {
		t.Fatal(err)
	}
	lists, err := playlists.Open(filepath.Join(root, "playlists.json"))
	if err != nil {
		t.Fatal(err)
	}
	list, err := lists.Create(a.ID, "Watch", []int{item.ID}, t0)
	if err != nil {
		t.Fatal(err)
	}

	def.Title = "Cinema"
	if err := s.SyncSections([]SectionDef{def}, t0.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	s, err = Open(s.path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SyncSections([]SectionDef{def}, t0); err != nil {
		t.Fatal(err)
	}
	got := s.Sections()[0]
	if got.ID != sec.ID || got.UUID != sec.UUID || got.ConfigID != def.ConfigID || got.Title != "Cinema" || got.CreatedAt != sec.CreatedAt || got.UpdatedAt != t0.Add(time.Hour).Unix() {
		t.Errorf("renamed section identity = %+v, originally %+v", got, sec)
	}
	if got, err := s.Item(item.ID); err != nil || !sameJSON(t, got, item) {
		t.Errorf("rename changed item identity/data: %+v, %v", got, err)
	}
	if got, err := s.Collection(c.ID); err != nil || !reflect.DeepEqual(got, c) {
		t.Errorf("rename changed collection: %+v, %v", got, err)
	}
	accts, err = accounts.Open(filepath.Join(root, "accounts.json"))
	if err != nil {
		t.Fatal(err)
	}
	if got, err := accts.Account(a.ID); err != nil || !got.CanSee(s.Sections()[0].ID) || got.CanSee(sec.ID+1) {
		t.Errorf("restricted account lost section access: %+v, %v", got, err)
	}
	play, err = playstate.Open(filepath.Join(root, "playstate.json"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Item(item.ID); err != nil || play.Get(a.ID, item.ID).ViewOffset != 1234 {
		t.Error("playstate no longer points at the same library item")
	}
	lists, err = playlists.Open(filepath.Join(root, "playlists.json"))
	if err != nil {
		t.Fatal(err)
	}
	if got, err := lists.Get(a.ID, list.ID); err != nil || !reflect.DeepEqual(got, list) {
		t.Errorf("playlist reference changed: %+v, %v", got, err)
	}
}

func sameJSON(t *testing.T, x, y any) bool {
	t.Helper()
	a, err := json.Marshal(x)
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(y)
	if err != nil {
		t.Fatal(err)
	}
	return bytes.Equal(a, b)
}

func TestSyncSectionsLegacyAdoptionAndReorder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "library.json")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defs := []SectionDef{
		{Title: "Movies", Type: TypeMovie, Paths: []string{"/m"}},
		{Title: "Movies 4K", Type: TypeMovie, Paths: []string{"/4k"}},
	}
	if err := s.SyncSections(defs, t0); err != nil {
		t.Fatal(err)
	}
	before := s.Sections()
	defs[0], defs[1] = defs[1], defs[0]
	if err := s.SyncSections(defs, t0.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if got := s.Sections(); got[0].ID != before[1].ID || got[1].ID != before[0].ID {
		t.Error("legacy reorder matched by order instead of exact title/type")
	}
	defs[1].ConfigID = "movies-main"
	if err := s.SyncSections(defs, t0); err != nil {
		t.Fatal(err)
	}
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defs[1].Title = "Cinema"
	defs[1].Paths = []string{"/m", "/new"}
	if err := s.SyncSections(defs, t0.Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	got := s.Sections()[1]
	if got.ID != before[0].ID || got.UUID != before[0].UUID || got.ConfigID != "movies-main" || got.Title != "Cinema" || !reflect.DeepEqual(got.Paths, defs[1].Paths) {
		t.Errorf("adopted/renamed identity = %+v, originally %+v", got, before[0])
	}
	// An old configuration can still match the now-bound, unique title/type;
	// it must not erase the binding persisted by the staged migration.
	defs[1].ConfigID = ""
	if err := s.SyncSections(defs, t0); err != nil || s.Sections()[1].ConfigID != "movies-main" {
		t.Errorf("ID omission dropped stored identity: %v", err)
	}
}

func TestSyncSectionsSimilarExplicitLibrariesAndRemoval(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "library.json"))
	if err != nil {
		t.Fatal(err)
	}
	defs := []SectionDef{
		{ConfigID: "a", Title: "Movies", Type: TypeMovie, Paths: []string{"/a"}},
		{ConfigID: "b", Title: "Movies", Type: TypeMovie, Paths: []string{"/b"}},
	}
	if err := s.SyncSections(defs, t0); err != nil {
		t.Fatal(err)
	}
	before := s.Sections()
	for i, sec := range before {
		if _, err := s.ApplyScan(sec.ID, []ScannedFile{file(defs[i].Paths[0]+"/a.mkv", "Alpha", 2000)}, t0); err != nil {
			t.Fatal(err)
		}
		if _, err := s.CreateCollection(sec.ID, "Favorites", []int{s.Items(sec.ID)[0].ID}, t0); err != nil {
			t.Fatal(err)
		}
	}
	defs[0], defs[1] = defs[1], defs[0]
	defs[0].Title = "Cinema"
	if err := s.SyncSections(defs, t0); err != nil {
		t.Fatal(err)
	}
	if got := s.Sections(); got[0].ID != before[1].ID || got[1].ID != before[0].ID {
		t.Error("explicit identities mixed similar libraries")
	}
	// A distinct addition and an explicit removal are deliberate operations.
	defs = []SectionDef{defs[0], {ConfigID: "c", Title: "TV", Type: TypeShow, Paths: []string{"/tv"}}}
	if err := s.SyncSections(defs, t0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Section(before[0].ID); err != ErrNotFound || len(s.Items(before[0].ID)) != 0 || len(s.Collections(before[0].ID)) != 0 {
		t.Error("deliberately removed section/catalog/collections survived")
	}
	if len(s.Items(before[1].ID)) != 1 || len(s.Collections(before[1].ID)) != 1 {
		t.Error("removing one section damaged its retained neighbor")
	}
	if err := s.SyncSections(nil, t0); err != nil || len(s.Sections()) != 0 || len(s.Items(0)) != 0 {
		t.Errorf("deliberate remove-all failed: %v", err)
	}
}

func TestSyncSectionsInvalidDefinitionsPreserveState(t *testing.T) {
	for name, defs := range map[string][]SectionDef{
		"duplicate identity": {
			{ConfigID: "a", Title: "Movies", Type: TypeMovie, Paths: []string{"/a"}},
			{ConfigID: "a", Title: "TV", Type: TypeShow, Paths: []string{"/tv"}},
		},
		"type change":                     {{ConfigID: "a", Title: "Movies", Type: TypeShow, Paths: []string{"/a"}}},
		"new id type change":              {{ConfigID: "other", Title: "Movies", Type: TypeShow, Paths: []string{"/tv"}}},
		"identity replacement":            {{ConfigID: "other", Title: "Movies", Type: TypeMovie, Paths: []string{"/a"}}},
		"identity replacement and rename": {{ConfigID: "other", Title: "Cinema", Type: TypeMovie, Paths: []string{"/a"}}},
		"idless type change":              {{Title: "Movies", Type: TypeShow, Paths: []string{"/a"}}},
		"invalid identity":                {{ConfigID: "has spaces", Title: "Movies", Type: TypeMovie, Paths: []string{"/a"}}},
		"ambiguous legacy definition": {
			{ConfigID: "a", Title: "Movies", Type: TypeMovie, Paths: []string{"/a"}},
			{Title: "Movies", Type: TypeMovie, Paths: []string{"/b"}},
		},
		"same section referenced twice": {
			{ConfigID: "a", Title: "Cinema", Type: TypeMovie, Paths: []string{"/a"}},
			{Title: "Movies", Type: TypeMovie, Paths: []string{"/a"}},
		},
	} {
		t.Run(name, func(t *testing.T) {
			s, err := Open(filepath.Join(t.TempDir(), "library.json"))
			if err != nil {
				t.Fatal(err)
			}
			if err := s.SyncSections([]SectionDef{{ConfigID: "a", Title: "Movies", Type: TypeMovie, Paths: []string{"/a"}}}, t0); err != nil {
				t.Fatal(err)
			}
			sec := s.Sections()[0].ID
			if _, err := s.ApplyScan(sec, []ScannedFile{file("/a/a.mkv", "Alpha", 2000)}, t0); err != nil {
				t.Fatal(err)
			}
			before := sectionSnapshot(t, s)
			if err := s.SyncSections(defs, t0.Add(time.Hour)); !errors.Is(err, ErrInvalidSections) {
				t.Errorf("invalid section definitions = %v, want ErrInvalidSections", err)
			}
			assertSectionSnapshot(t, s, before)
		})
	}
}

func TestSyncSectionsStoredIdentityAmbiguityRejected(t *testing.T) {
	for _, duplicateStoredID := range []bool{false, true} {
		t.Run(map[bool]string{false: "idless-match", true: "duplicate-stored-id"}[duplicateStoredID], func(t *testing.T) {
			s, err := Open(filepath.Join(t.TempDir(), "library.json"))
			if err != nil {
				t.Fatal(err)
			}
			defs := []SectionDef{
				{ConfigID: "a", Title: "Movies", Type: TypeMovie, Paths: []string{"/a"}},
				{ConfigID: "b", Title: "Movies", Type: TypeMovie, Paths: []string{"/b"}},
			}
			if err := s.SyncSections(defs, t0); err != nil {
				t.Fatal(err)
			}
			if duplicateStoredID {
				// A malformed persisted store must be rejected, not matched in
				// whichever order its sections happen to be decoded.
				s.d.Sections[1].ConfigID = "a"
				if err := s.saveLocked(); err != nil {
					t.Fatal(err)
				}
				s, err = Open(s.path)
				if err != nil {
					t.Fatal(err)
				}
			} else {
				defs = []SectionDef{{Title: "Movies", Type: TypeMovie, Paths: []string{"/a"}}}
			}
			before := sectionSnapshot(t, s)
			if err := s.SyncSections(defs, t0); !errors.Is(err, ErrInvalidSections) {
				t.Errorf("ambiguous stored identity = %v", err)
			}
			assertSectionSnapshot(t, s, before)
		})
	}
}

func TestSyncSectionsIdentityPathEditPreservesCatalog(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "library.json"))
	if err != nil {
		t.Fatal(err)
	}
	def := SectionDef{ConfigID: "movies", Title: "Movies", Type: TypeMovie, Paths: []string{"/m"}}
	if err := s.SyncSections([]SectionDef{def}, t0); err != nil {
		t.Fatal(err)
	}
	sec := s.Sections()[0]
	if _, err := s.ApplyScan(sec.ID, []ScannedFile{file("/m/a.mkv", "Alpha", 2000)}, t0); err != nil {
		t.Fatal(err)
	}
	items := s.Items(sec.ID)
	def.Paths = []string{"/different", "/m"}
	if err := s.SyncSections([]SectionDef{def}, t0.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	got := s.Sections()[0]
	if got.ID != sec.ID || got.UUID != sec.UUID || got.CreatedAt != sec.CreatedAt || got.UpdatedAt != t0.Add(time.Hour).Unix() || !sameJSON(t, s.Items(sec.ID), items) {
		t.Errorf("path edit damaged section/catalog: %+v", got)
	}
	def.Paths[0] = "/mutated-caller"
	if got := s.Sections()[0]; got.Paths[0] != "/different" {
		t.Error("stored paths alias the caller's configuration")
	}
}

func TestSyncSectionsSaveFailureRollsBack(t *testing.T) {
	root := t.TempDir()
	s, err := Open(filepath.Join(root, "library.json"))
	if err != nil {
		t.Fatal(err)
	}
	def := SectionDef{ConfigID: "movies", Title: "Movies", Type: TypeMovie, Paths: []string{"/m"}}
	if err := s.SyncSections([]SectionDef{def}, t0); err != nil {
		t.Fatal(err)
	}
	sec := s.Sections()[0].ID
	if _, err := s.ApplyScan(sec, []ScannedFile{file("/m/a.mkv", "Alpha", 2000)}, t0); err != nil {
		t.Fatal(err)
	}
	before := sectionSnapshot(t, s)
	brokenPath := filepath.Join(root, "directory")
	if err := os.Mkdir(brokenPath, 0o755); err != nil {
		t.Fatal(err)
	}
	path := s.path
	s.path = brokenPath // Rename of a regular temporary file onto a directory fails.
	def.Title = "Cinema"
	if err := s.SyncSections([]SectionDef{def, {ConfigID: "tv", Title: "TV", Type: TypeShow, Paths: []string{"/tv"}}}, t0.Add(time.Hour)); err == nil {
		t.Error("section save unexpectedly succeeded")
	}
	s.path = path
	assertSectionSnapshot(t, s, before)
	if got, err := s.Section(sec); err != nil || got.Title != "Movies" || len(s.Items(sec)) != 1 {
		t.Errorf("failed sync did not restore indexes: %+v, %v", got, err)
	}
}

func TestSyncSectionsLegacyAdoptionAmbiguityRejected(t *testing.T) {
	for name, defs := range map[string][]SectionDef{
		"renamed before adoption": {{ConfigID: "new", Title: "Cinema", Type: TypeMovie, Paths: []string{"/new"}}},
		"two candidates to adopt one legacy section": {
			{ConfigID: "a", Title: "Movies", Type: TypeMovie, Paths: []string{"/a"}},
			{ConfigID: "b", Title: "Movies", Type: TypeMovie, Paths: []string{"/b"}},
		},
	} {
		t.Run(name, func(t *testing.T) {
			s, _ := newStore(t, filepath.Join(t.TempDir(), "library.json"))
			before := sectionSnapshot(t, s)
			if err := s.SyncSections(defs, t0); err == nil {
				t.Error("ambiguous legacy adoption succeeded")
			}
			assertSectionSnapshot(t, s, before)
		})
	}
}
