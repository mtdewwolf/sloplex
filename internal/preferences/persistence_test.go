package preferences

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/mtdewwolf/sloplex/internal/atomicfile"
)

func TestSetPersistenceOutcome(t *testing.T) {
	for _, installed := range []bool{false, true} {
		t.Run(fmt.Sprintf("installed=%t", installed), func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "preferences.json")
			s := open(t, path, boot)
			if err := s.Set(map[string]string{FriendlyName: "Old", LogDebug: "0"}); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			changed, cancel := s.Subscribe()
			defer cancel()
			cause := errors.New("injected persistence failure")
			var outcome error = cause
			if installed {
				outcome = &atomicfile.CommittedError{Path: path, Err: cause}
			}
			outcome = fmt.Errorf("save preferences: %w", outcome)
			s.writeFile = func(path string, data []byte, mode os.FileMode) error {
				if installed {
					if err := atomicfile.Write(path, data, mode); err != nil {
						return err
					}
				}
				return outcome
			}
			err = s.Set(map[string]string{FriendlyName: "New", LogDebug: "1", ScheduledLibraryUpdateInterval: "900"})
			if err != outcome || !errors.Is(err, cause) {
				t.Fatalf("save error = %v; want original %v", err, outcome)
			}
			var committed *atomicfile.CommittedError
			if errors.As(err, &committed) != installed {
				t.Fatalf("committed outcome = %v; want %t", err, installed)
			}
			notified := false
			select {
			case <-changed:
				notified = true
			default:
			}
			if notified != installed {
				t.Fatalf("notified = %t; want %t", notified, installed)
			}
			after, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Equal(before, after) == installed {
				t.Fatalf("file changed = %t; want %t", !bytes.Equal(before, after), installed)
			}
			reopened := open(t, path, boot)
			if !reflect.DeepEqual(s.All(), reopened.All()) {
				t.Fatal("published preferences and reopened file disagree")
			}
			wantName, wantDebug, wantInterval := "Old", false, time.Hour
			if installed {
				wantName, wantDebug, wantInterval = "New", true, 15*time.Minute
			}
			if s.FriendlyName() != wantName || s.Debug() != wantDebug || s.ScanInterval() != wantInterval {
				t.Fatalf("published values = %q/%t/%v; want %q/%t/%v", s.FriendlyName(), s.Debug(), s.ScanInterval(), wantName, wantDebug, wantInterval)
			}
		})
	}
}

func TestValidationFailureDoesNotPersistOrNotify(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "preferences.json")
	s := open(t, path, boot)
	if err := s.Set(map[string]string{FriendlyName: "Old"}); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	changed, cancel := s.Subscribe()
	defer cancel()
	s.writeFile = func(string, []byte, os.FileMode) error {
		t.Fatal("validation failure attempted persistence")
		return nil
	}
	err = s.Set(map[string]string{FriendlyName: "New", LogDebug: "invalid"})
	var invalid *InvalidError
	if !errors.As(err, &invalid) {
		t.Fatalf("error = %v; want InvalidError", err)
	}
	select {
	case <-changed:
		t.Fatal("validation failure notified subscribers")
	default:
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) || s.FriendlyName() != "Old" || !reflect.DeepEqual(s.All(), open(t, path, boot).All()) {
		t.Fatal("validation failure changed memory or persisted preferences")
	}
}
