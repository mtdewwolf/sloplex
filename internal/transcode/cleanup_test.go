package transcode

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCleanupPreservesScratchRoot(t *testing.T) {
	dir := t.TempDir()
	before, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(dir, "old-session")
	if err := os.Mkdir(stale, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stale, "segment.ts"), []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	m, err := New(Options{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Close)
	assertRoot := func() {
		t.Helper()
		after, err := os.Stat(dir)
		if err != nil {
			t.Fatal(err)
		}
		if !os.SameFile(before, after) {
			t.Fatal("scratch root was replaced")
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 0 {
			t.Fatalf("stale contents remain: %v", entries)
		}
	}
	assertRoot()
	if err := os.WriteFile(filepath.Join(dir, "new-session"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	m.Close()
	assertRoot()
}

func TestCleanupDoesNotFollowSymlinks(t *testing.T) {
	outside := t.TempDir()
	keep := filepath.Join(outside, "keep")
	if err := os.WriteFile(keep, []byte("safe"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, rootLink := range []bool{false, true} {
		dir := filepath.Join(t.TempDir(), "scratch")
		if rootLink {
			if err := os.Symlink(outside, dir); err != nil {
				t.Fatal(err)
			}
		} else {
			if err := os.Mkdir(dir, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(outside, filepath.Join(dir, "link")); err != nil {
				t.Fatal(err)
			}
		}
		if err := clearSessionDirectory(dir); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(keep); err != nil {
			t.Fatalf("symlink target damaged: %v", err)
		}
	}
}

func TestCleanupAbsentDirectory(t *testing.T) {
	if err := clearSessionDirectory(filepath.Join(t.TempDir(), "missing")); err != nil {
		t.Fatal(err)
	}
}
