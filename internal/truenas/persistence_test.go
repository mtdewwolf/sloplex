package truenas

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/mtdewwolf/sloplex/internal/atomicfile"
)

func TestSetPersistenceOutcome(t *testing.T) {
	for _, installed := range []bool{false, true} {
		t.Run(fmt.Sprintf("installed=%t", installed), func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "truenas.json")
			s, err := Open(path)
			if err != nil {
				t.Fatal(err)
			}
			old := Settings{URL: "https://old.lan", App: "sloplex", APIKey: "old-key"}
			if err := s.Set(old); err != nil {
				t.Fatal(err)
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
			outcome = fmt.Errorf("save settings: %w", outcome)
			s.writeFile = func(path string, data []byte, mode os.FileMode) error {
				if installed {
					if err := atomicfile.Write(path, data, mode); err != nil {
						return err
					}
				}
				return outcome
			}
			next := Settings{URL: "https://new.lan", App: "new-app", APIKey: "new-key"}
			err = s.Set(next)
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
			want := old
			if installed {
				want = next
			}
			for _, store := range []*Store{s, reopened} {
				if got, ok := store.Get(); !ok || got != want {
					t.Fatalf("memory or reopened settings = %+v, %t; want %+v", got, ok, want)
				}
			}
		})
	}
}
