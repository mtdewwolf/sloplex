package library

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mtdewwolf/sloplex/internal/atomicfile"
)

func TestSyncSectionsPersistenceOutcome(t *testing.T) {
	for _, installed := range []bool{false, true} {
		t.Run(fmt.Sprintf("installed=%t", installed), func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "library.json")
			s, sectionID := newStore(t, path)
			if _, err := s.ApplyScan(sectionID, []ScannedFile{file("/m/a.mkv", "Alpha", 2000)}, t0); err != nil {
				t.Fatal(err)
			}
			oldItem := s.Items(sectionID)[0]
			oldPart := oldItem.Media[0].Parts[0].ID
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			cause := errors.New("injected persistence failure")
			var outcome error = cause
			if installed {
				outcome = &atomicfile.CommittedError{Path: path, Err: cause}
			}
			outcome = fmt.Errorf("save library: %w", outcome)
			s.writeFile = func(path string, data []byte, mode os.FileMode) error {
				if installed {
					if err := atomicfile.Write(path, data, mode); err != nil {
						return err
					}
				}
				return outcome
			}
			err = s.SyncSections([]SectionDef{{Title: "Films", Type: TypeMovie, Paths: []string{"/f"}}}, t0.Add(time.Hour))
			if err != outcome || !errors.Is(err, cause) {
				t.Fatalf("save error = %v; want original %v", err, outcome)
			}
			var committed *atomicfile.CommittedError
			if errors.As(err, &committed) != installed {
				t.Fatalf("committed outcome = %v; want %t", err, installed)
			}
			after, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Equal(before, after) == installed {
				t.Fatalf("file changed = %t; want %t", !bytes.Equal(before, after), installed)
			}
			reopened, err := Open(path)
			if err != nil {
				t.Fatal(err)
			}
			// Compare the persisted representation: omitted empty slices can
			// reload as nil without changing catalog state or ID counters.
			if !bytes.Equal(sectionSnapshot(t, s), sectionSnapshot(t, reopened)) {
				t.Fatal("published library and reopened file disagree")
			}
			sections := s.Sections()
			if len(sections) != 1 {
				t.Fatalf("sections = %+v", sections)
			}
			_, itemErr := s.Item(oldItem.ID)
			_, _, partErr := s.Part(oldPart)
			if installed {
				if sections[0].Title != "Films" || sections[0].ID == sectionID || !errors.Is(itemErr, ErrNotFound) || !errors.Is(partErr, ErrNotFound) {
					t.Fatalf("installed configuration/indexes rolled back: sections=%+v item=%v part=%v", sections, itemErr, partErr)
				}
			} else if sections[0].Title != "Movies" || sections[0].ID != sectionID || itemErr != nil || partErr != nil {
				t.Fatalf("failed save changed configuration/indexes: sections=%+v item=%v part=%v", sections, itemErr, partErr)
			}
		})
	}
}
