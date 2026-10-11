package library

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/mtdewwolf/sloplex/internal/atomicfile"
	"github.com/mtdewwolf/sloplex/internal/probe"
)

func TestStagedTrashPersistenceOutcomes(t *testing.T) {
	for _, operation := range []string{"scan", "purge"} {
		for _, installed := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/installed=%t", operation, installed), func(t *testing.T) {
				t.Parallel()
				path := filepath.Join(t.TempDir(), "library.json")
				s, err := Open(path)
				if err != nil {
					t.Fatal(err)
				}
				if err := s.SyncSections([]SectionDef{{Title: "TV", Type: TypeShow, Paths: []string{"/tv"}}}, t0); err != nil {
					t.Fatal(err)
				}
				sectionID := s.Sections()[0].ID
				missing := episodeFile("/tv/Example/S01E01.mkv", "Example", 1, 1, "Missing")
				missing.Info.Streams = []probe.Stream{{Index: 0, Type: probe.StreamAudio, Codec: "aac"}}
				healthy := episodeFile("/tv/Example/S02E01.mkv", "Example", 2, 1, "Healthy")
				healthy.Info.Streams = missing.Info.Streams
				if _, err := s.ApplyScan(sectionID, []ScannedFile{missing, healthy}, t0); err != nil {
					t.Fatal(err)
				}
				episodes := s.Items(sectionID, TypeEpisode)
				missingItem, healthyItem := episodes[0], episodes[1]
				missingPart, healthyPart := missingItem.Media[0].Parts[0], healthyItem.Media[0].Parts[0]
				show := s.Items(sectionID, TypeShow)[0]
				collection, err := s.CreateCollection(sectionID, "Keep", []int{show.ID}, t0)
				if err != nil {
					t.Fatal(err)
				}
				healthy.Changed = false
				if operation == "purge" {
					if _, err := s.ApplyScan(sectionID, []ScannedFile{healthy}, t0.Add(time.Hour)); err != nil {
						t.Fatal(err)
					}
				}
				before, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				cause := errors.New("injected persistence failure")
				var outcome error = cause
				if installed {
					outcome = &atomicfile.CommittedError{Path: path, Err: cause}
				}
				outcome = fmt.Errorf("save staged catalog: %w", outcome)
				writes := 0
				s.writeFile = func(path string, data []byte, mode os.FileMode) error {
					writes++
					if installed {
						if err := atomicfile.Write(path, data, mode); err != nil {
							return err
						}
					}
					return outcome
				}
				if operation == "scan" {
					_, err = s.ApplyScan(sectionID, []ScannedFile{healthy}, t0.Add(2*time.Hour))
				} else {
					_, err = s.EmptyTrash(sectionID, t0.Add(2*time.Hour))
				}
				if writes != 1 || err != outcome || !errors.Is(err, cause) {
					t.Fatalf("writer calls=%d error=%v; want 1 and original %v", writes, err, outcome)
				}
				var committed *atomicfile.CommittedError
				if errors.As(err, &committed) != installed || installed && committed.Path != path {
					t.Fatalf("committed outcome = %v; want installed=%t path=%q", err, installed, path)
				}
				after, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				if bytes.Equal(before, after) == installed {
					t.Fatalf("file changed=%t; want %t", !bytes.Equal(before, after), installed)
				}
				reopened, err := Open(path)
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(after, sectionSnapshot(t, s)) || !bytes.Equal(sectionSnapshot(t, s), sectionSnapshot(t, reopened)) {
					t.Fatal("running catalog, installed file and reopened catalog disagree")
				}
				purged := operation == "purge" && installed
				unavailable := operation == "purge" || installed
				for _, store := range []*Store{s, reopened} {
					item, itemErr := store.Item(missingItem.ID)
					season, seasonErr := store.Item(missingItem.ParentID)
					part, partSection, partErr := store.Part(missingPart.ID)
					_, streamPart, streamSection, streamErr := store.Stream(missingPart.Streams[0].ID)
					if purged {
						for _, lookupErr := range []error{itemErr, seasonErr, partErr, streamErr} {
							if !errors.Is(lookupErr, ErrNotFound) {
								t.Fatalf("purged record still resolves: %v", lookupErr)
							}
						}
						if len(store.Children(missingItem.ParentID)) != 0 {
							t.Fatal("purged season retains a child index")
						}
					} else if itemErr != nil || seasonErr != nil || partErr != nil || streamErr != nil ||
						item.Unavailable != unavailable || season.Unavailable != unavailable || part.Unavailable != unavailable ||
						streamPart.Unavailable != unavailable || partSection != sectionID || streamSection != sectionID {
						t.Fatalf("retained availability/indexes disagree: item=%+v season=%+v part=%+v streamPart=%+v errors=%v/%v/%v/%v", item, season, part, streamPart, itemErr, seasonErr, partErr, streamErr)
					}
					if store.PartAvailable(missingPart.ID) != (!purged && !unavailable) || !store.PartAvailable(healthyPart.ID) {
						t.Fatal("playback availability disagrees with installed snapshot")
					}
					wantChildren := 2
					if purged {
						wantChildren = 1
					}
					currentShow, showErr := store.Item(show.ID)
					currentHealthy, healthyErr := store.Item(healthyItem.ID)
					if showErr != nil || healthyErr != nil || currentShow.Unavailable || currentHealthy.Unavailable ||
						len(store.Children(show.ID)) != wantChildren || len(store.Leaves(show.ID)) != wantChildren {
						t.Fatal("healthy hierarchy or child indexes changed incorrectly")
					}
					kept, err := store.Collection(collection.ID)
					if err != nil || !slices.Equal(kept.ItemIDs, []int{show.ID}) || !slices.Contains(store.CollectionTitles(show.ID), "Keep") {
						t.Fatal("collection membership/indexes disagree with retained show")
					}
				}
			})
		}
	}
}
