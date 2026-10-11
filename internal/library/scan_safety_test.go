package library

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/mtdewwolf/sloplex/internal/probe"
)

// These fixtures are Sloplex's own safety contract, not captured PMS behavior.
func safetyFixture(t *testing.T, roots ...string) (*Store, *Scanner, int) {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "library.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SyncSections([]SectionDef{{Title: "Movies", Type: TypeMovie, Paths: roots}}, t0); err != nil {
		t.Fatal(err)
	}
	sec := s.Sections()[0].ID
	sc := NewScanner(s, &countingProber{}, nil)
	sc.now = func() time.Time { return t0 }
	if _, err := sc.Scan(context.Background(), sec); err != nil {
		t.Fatal(err)
	}
	return s, sc, sec
}

type infoErrorEntry struct{ fs.DirEntry }

func (infoErrorEntry) Info() (fs.FileInfo, error) { return nil, fs.ErrPermission }

func TestScanIncompleteTraversalPreservesSection(t *testing.T) {
	for _, fault := range []string{"nested-directory", "file-info"} {
		t.Run(fault, func(t *testing.T) {
			root := t.TempDir()
			file := filepath.Join(root, "nested", "Heat (1995).mkv")
			writeFile(t, file, "x")
			writeFile(t, filepath.Join(root, "nested", "poster.jpg"), "image")
			s, sc, sec := safetyFixture(t, root)
			before := s.Items(sec)
			section, _ := s.Section(sec)
			collection, err := s.CreateCollection(sec, "Keep", []int{before[0].ID}, t0)
			if err != nil {
				t.Fatal(err)
			}
			// A readable new item must not make the incomplete scan safe to apply.
			writeFile(t, filepath.Join(root, "Alien (1979).mkv"), "x")
			sc.walkDir = func(root string, fn fs.WalkDirFunc) error {
				return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
					if fault == "nested-directory" && path == filepath.Dir(file) {
						return fn(path, d, fs.ErrPermission)
					}
					if fault == "file-info" && path == file {
						d = infoErrorEntry{d}
					}
					return fn(path, d, err)
				})
			}
			sc.now = func() time.Time { return t0.Add(time.Hour) }
			if _, err := sc.Scan(context.Background(), sec); !errors.Is(err, ErrScanIncomplete) || !errors.Is(err, fs.ErrPermission) {
				t.Errorf("incomplete scan = %v, want incomplete/permission error", err)
			}
			assertSectionUnchanged(t, s, sec, before, section, collection)
		})
	}
}

func assertSectionUnchanged(t *testing.T, s *Store, sec int, items []Item, section Section, collection Collection) {
	t.Helper()
	if got := s.Items(sec); !reflect.DeepEqual(got, items) {
		t.Errorf("catalog changed after failed scan: got %+v, want %+v", got, items)
	}
	if got, _ := s.Section(sec); !reflect.DeepEqual(got, section) {
		t.Errorf("section changed after failed scan: got %+v, want %+v", got, section)
	}
	if got, err := s.Collection(collection.ID); err != nil || !reflect.DeepEqual(got, collection) {
		t.Errorf("collection changed after failed scan: %+v, %v", got, err)
	}
	reloaded, err := Open(s.path)
	if err != nil {
		t.Fatal(err)
	}
	want, err := json.Marshal(items)
	if err != nil {
		t.Fatal(err)
	}
	got, err := json.Marshal(reloaded.Items(sec))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Error("persisted catalog changed after failed scan")
	}
	if got, _ := reloaded.Section(sec); !reflect.DeepEqual(got, section) {
		t.Error("persisted section changed after failed scan")
	}
	if got, err := reloaded.Collection(collection.ID); err != nil || !reflect.DeepEqual(got, collection) {
		t.Errorf("persisted collection changed after failed scan: %+v, %v", got, err)
	}
}

func TestScanEmptyPreviouslyPopulatedRootRetainsUnavailable(t *testing.T) {
	for _, multipleRoots := range []bool{false, true} {
		t.Run(map[bool]string{false: "single-root", true: "one-of-two-roots"}[multipleRoots], func(t *testing.T) {
			root := t.TempDir()
			file := filepath.Join(root, "Heat (1995).mkv")
			writeFile(t, file, "x")
			roots := []string{root}
			if multipleRoots {
				other := t.TempDir()
				writeFile(t, filepath.Join(other, "Alien (1979).mkv"), "x")
				roots = append(roots, other)
			}
			s, sc, sec := safetyFixture(t, roots...)
			before := s.Items(sec)
			collection, err := s.CreateCollection(sec, "Keep", []int{before[0].ID}, t0)
			if err != nil {
				t.Fatal(err)
			}
			// Model an unmounted volume: its configured mountpoint still exists.
			if err := os.Remove(file); err != nil {
				t.Fatal(err)
			}
			sc.now = func() time.Time { return t0.Add(time.Hour) }
			if stats, err := sc.Scan(context.Background(), sec); err != nil || stats.Unavailable != 1 || stats.Removed != 0 {
				t.Fatalf("empty root scan: %+v, %v", stats, err)
			}
			for _, old := range before {
				got, err := s.Item(old.ID)
				wantMissing := old.Media[0].Parts[0].File == file
				if err != nil || got.Unavailable != wantMissing || got.Media[0].ID != old.Media[0].ID || got.Media[0].Parts[0].ID != old.Media[0].Parts[0].ID {
					t.Errorf("retained identity/availability: %+v, %v", got, err)
				}
			}
			if got, err := s.Collection(collection.ID); err != nil || !reflect.DeepEqual(got.ItemIDs, collection.ItemIDs) {
				t.Errorf("collection after empty root: %+v, %v", got, err)
			}
			reopened, err := Open(s.path)
			if err != nil {
				t.Fatal(err)
			}
			persisted, _ := json.Marshal(reopened.Items(sec))
			live, _ := json.Marshal(s.Items(sec))
			if !bytes.Equal(persisted, live) {
				t.Error("unavailable state lost on reopen")
			}
			// Restoring the file unblocks scanning without replacing its ID.
			writeFile(t, file, "x")
			if _, err := sc.Scan(context.Background(), sec); err != nil {
				t.Fatal(err)
			}
			for _, old := range before {
				if got, err := s.Item(old.ID); err != nil || got.Title != old.Title || got.Unavailable {
					t.Errorf("restored item identity changed: %+v, %v", got, err)
				}
			}
		})
	}
}

func TestScanEmptyNewAndSiblingRootsAllowed(t *testing.T) {
	base := t.TempDir()
	empty := filepath.Join(base, "movies")
	populated := filepath.Join(base, "movies-old")
	if err := os.Mkdir(empty, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(populated, "Heat (1995).mkv"), "x")
	s, sc, sec := safetyFixture(t, empty, populated)
	// The previously catalogued /movies-old file is not beneath /movies.
	if _, err := sc.Scan(context.Background(), sec); err != nil || titles(s, sec) != "Heat" {
		t.Fatalf("scan with a new empty sibling root: %v, titles %q", err, titles(s, sec))
	}
	// A section's very first scan can also be completely empty.
	safetyFixture(t, t.TempDir())
}

func TestScanCompletePopulatedRootAllowsExplicitPurge(t *testing.T) {
	root := t.TempDir()
	removed := filepath.Join(root, "Heat (1995).mkv")
	writeFile(t, removed, "x")
	writeFile(t, filepath.Join(root, "Alien (1979).mkv"), "x")
	s, sc, sec := safetyFixture(t, root)
	if err := os.Remove(removed); err != nil {
		t.Fatal(err)
	}
	stats, err := sc.Scan(context.Background(), sec)
	if err != nil || stats.Removed != 0 || stats.Unavailable != 1 || titles(s, sec) != "Alien,Heat" {
		t.Fatalf("complete, still-populated scan = %+v, %v, titles %q", stats, err, titles(s, sec))
	}
	if stats, err := s.EmptyTrash(sec, t0); err != nil || stats.Removed != 1 || titles(s, sec) != "Alien" {
		t.Fatalf("explicit purge: %+v, %v, titles %q", stats, err, titles(s, sec))
	}
}

type proberFunc func(context.Context, string) (probe.Info, error)

func (fn proberFunc) Probe(ctx context.Context, path string) (probe.Info, error) {
	return fn(ctx, path)
}

func TestScanProbeFailurePreservesKnownInfo(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "Heat (1995).mkv")
	writeFile(t, file, "x")
	s, sc, sec := safetyFixture(t, root)
	sc.prober = proberFunc(func(context.Context, string) (probe.Info, error) {
		return probe.Info{Container: "mkv", Duration: 5000, Width: 1920, ProbeVersion: probe.Version,
			Streams: []probe.Stream{{Index: 0, Type: probe.StreamAudio, Codec: "aac", Language: "eng"}}}, nil
	})
	writeFile(t, file, "first known streams")
	if _, err := sc.Scan(context.Background(), sec); err != nil {
		t.Fatal(err)
	}
	before := s.Items(sec)[0]
	writeFile(t, file, "changed content")
	sc.prober = proberFunc(func(context.Context, string) (probe.Info, error) {
		return probe.Info{}, errors.New("unsupported metadata")
	})
	if _, err := sc.Scan(context.Background(), sec); err != nil {
		t.Fatal(err)
	}
	got := s.Items(sec)[0]
	if got.ID != before.ID || got.Media[0].Duration != before.Media[0].Duration || got.Media[0].Width != before.Media[0].Width {
		t.Errorf("probe failure replaced identity/known metadata: %+v", got)
	}
	if !reflect.DeepEqual(got.Media[0].Parts[0].Streams, before.Media[0].Parts[0].Streams) {
		t.Error("probe failure discarded known streams or their IDs")
	}
	// A failed probe must be retried even though size/time now match the store.
	pr := &countingProber{}
	sc.prober = pr
	if _, err := sc.Scan(context.Background(), sec); err != nil {
		t.Fatal(err)
	}
	if pr.calls[file] != 1 {
		t.Error("failed probe was cached as successful")
	}
}

func TestScanProbeFailureNewFileRetried(t *testing.T) {
	root := t.TempDir()
	s, sc, sec := safetyFixture(t, root)
	file := filepath.Join(root, "Heat (1995).mkv")
	writeFile(t, file, "x")
	sc.prober = proberFunc(func(context.Context, string) (probe.Info, error) {
		return probe.Info{Width: 999}, errors.New("unsupported media")
	})
	if _, err := sc.Scan(context.Background(), sec); err != nil {
		t.Fatal(err)
	}
	it := s.Items(sec)[0]
	if it.Media[0].Container != "mkv" || it.Media[0].Width != 0 {
		t.Errorf("new probe failure accepted partial output: %+v", it.Media[0])
	}
	pr := &countingProber{}
	sc.prober = pr
	if _, err := sc.Scan(context.Background(), sec); err != nil || pr.calls[file] != 1 {
		t.Fatalf("retry of new file: %v, calls %d", err, pr.calls[file])
	}
}

func TestScanProbeFailureRacedFilePreservesSection(t *testing.T) {
	for _, fault := range []string{"missing", "changed", "not-regular"} {
		t.Run(fault, func(t *testing.T) {
			root := t.TempDir()
			file := filepath.Join(root, "Heat (1995).mkv")
			writeFile(t, file, "x")
			s, sc, sec := safetyFixture(t, root)
			before := s.Items(sec)
			section, _ := s.Section(sec)
			collection, err := s.CreateCollection(sec, "Keep", []int{before[0].ID}, t0)
			if err != nil {
				t.Fatal(err)
			}
			writeFile(t, file, "changed to force probing")
			sc.prober = proberFunc(func(context.Context, string) (probe.Info, error) {
				switch fault {
				case "missing", "not-regular":
					if err := os.Remove(file); err != nil {
						t.Fatal(err)
					}
					if fault == "not-regular" {
						if err := os.Mkdir(file, 0o755); err != nil {
							t.Fatal(err)
						}
					}
				case "changed":
					writeFile(t, file, "another size")
				}
				return probe.Info{}, errors.New("probe failed")
			})
			if _, err := sc.Scan(context.Background(), sec); !errors.Is(err, ErrScanIncomplete) {
				t.Errorf("raced file scan = %v, want incomplete scan", err)
			}
			assertSectionUnchanged(t, s, sec, before, section, collection)
		})
	}
}

func TestScanCancellationAfterLastProbePreservesSection(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "Heat (1995).mkv"), "x")
	s, sc, sec := safetyFixture(t, root)
	before := s.Items(sec)
	section, _ := s.Section(sec)
	collection, err := s.CreateCollection(sec, "Keep", []int{before[0].ID}, t0)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(root, "Zodiac (2007).mkv"), "x")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sc.prober = proberFunc(func(context.Context, string) (probe.Info, error) {
		cancel()
		return probe.Info{Container: "mkv"}, nil
	})
	if _, err := sc.Scan(ctx, sec); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled scan = %v, want context.Canceled", err)
	}
	assertSectionUnchanged(t, s, sec, before, section, collection)
}
