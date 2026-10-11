package atomicfile

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "f.json")
	for _, content := range []string{"one", "two"} {
		if err := Write(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		b, err := os.ReadFile(path)
		if err != nil || string(b) != content {
			t.Fatalf("read %q, %v; want %q", b, err, content)
		}
	}
	fi, _ := os.Stat(path)
	if runtime.GOOS != "windows" && fi.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v", fi.Mode().Perm())
	}
	if entries, _ := os.ReadDir(filepath.Dir(path)); len(entries) != 1 {
		t.Errorf("temp files left behind: %v", entries)
	}
}

var errInjected = errors.New("injected filesystem failure")

type faultFile struct {
	*os.File
	fail                     string
	chmodded, synced, closed bool
}

func (f *faultFile) Write(b []byte) (int, error) {
	if f.fail == "write" || f.fail == "short write" {
		n, err := f.File.Write(b[:len(b)/2])
		if err != nil {
			return n, err
		}
		if f.fail == "write" {
			return n, errInjected
		}
		return n, nil
	}
	return f.File.Write(b)
}

func (f *faultFile) Chmod(mode os.FileMode) error {
	if f.fail == "chmod" {
		return errInjected
	}
	if err := f.File.Chmod(mode); err != nil {
		return err
	}
	f.chmodded = true
	return nil
}

func (f *faultFile) Sync() error {
	if f.fail == "file sync" {
		return errInjected
	}
	if !f.chmodded {
		return errors.New("file synchronized before permissions were applied")
	}
	if err := f.File.Sync(); err != nil {
		return err
	}
	f.synced = true
	return nil
}

func (f *faultFile) Close() error {
	f.closed = true
	err := f.File.Close()
	if f.fail == "close" {
		return errors.Join(err, errInjected)
	}
	return err
}

type faultDirectory struct {
	fail   string
	closed bool
}

func (d *faultDirectory) Sync() error {
	if d.fail == "directory sync" {
		return errInjected
	}
	return nil
}

func (d *faultDirectory) Close() error {
	d.closed = true
	if d.fail == "directory close" {
		return errInjected
	}
	return nil
}

func TestReplacementFailureBoundaries(t *testing.T) {
	for _, fail := range []string{"mkdir", "create", "write", "short write", "chmod", "file sync", "close", "rename", "directory open", "directory sync", "directory close"} {
		for _, existing := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/existing=%t", fail, existing), func(t *testing.T) {
				t.Parallel()
				dir := t.TempDir()
				path := filepath.Join(dir, "state.json")
				if existing {
					if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
						t.Fatal(err)
					}
				}
				ops := systemOperations()
				var file *faultFile
				dirHandle := &faultDirectory{fail: fail}
				if fail == "mkdir" {
					ops.mkdirAll = func(string, os.FileMode) error { return errInjected }
				}
				ops.createTemp = func(dir, pattern string) (temporaryFile, error) {
					if fail == "create" {
						return nil, errInjected
					}
					f, err := os.CreateTemp(dir, pattern)
					if err != nil {
						return nil, err
					}
					file = &faultFile{File: f, fail: fail}
					return file, nil
				}
				ops.rename = func(from, to string) error {
					if !file.synced || !file.closed {
						t.Fatal("rename attempted before file synchronization and close")
					}
					if fail == "rename" {
						return errInjected
					}
					return os.Rename(from, to)
				}
				ops.syncDir = func(dir string) error {
					assertContents(t, path, "new complete data")
					return syncDirectoryWithOpen(dir, func(string) (directory, error) {
						if fail == "directory open" {
							return nil, errInjected
						}
						return dirHandle, nil
					})
				}
				err := write(path, []byte("new complete data"), 0o600, ops)
				wantErr := errInjected
				if fail == "short write" {
					wantErr = io.ErrShortWrite
				}
				if !errors.Is(err, wantErr) {
					t.Fatalf("error = %v; want wrapped %v", err, wantErr)
				}
				var committed *CommittedError
				afterRename := fail == "directory open" || fail == "directory sync" || fail == "directory close"
				if errors.As(err, &committed) != afterRename {
					t.Fatalf("committed error = %v; want %t", err, afterRename)
				}
				if afterRename {
					if committed.Path != path {
						t.Fatalf("committed path = %q; want %q", committed.Path, path)
					}
					assertContents(t, path, "new complete data")
					if fail != "directory open" && !dirHandle.closed {
						t.Fatal("directory handle was not closed")
					}
				} else if existing {
					assertContents(t, path, "old")
				} else if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("uncommitted destination stat = %v; want absent", err)
				}
				if file != nil && !file.closed {
					t.Fatal("temporary handle was not closed")
				}
				entries, err := os.ReadDir(dir)
				if err != nil {
					t.Fatal(err)
				}
				wantEntries := 0
				if existing || afterRename {
					wantEntries = 1
				}
				if len(entries) != wantEntries {
					t.Fatalf("temporary files left behind: %v", entries)
				}
			})
		}
	}
}

func assertContents(t *testing.T, path, want string) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil || string(b) != want {
		t.Fatalf("read %q, %v; want %q", b, err, want)
	}
}

func TestSuccessfulReplacementSynchronizesBeforeRename(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "state.json")
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	ops := systemOperations()
	var file *faultFile
	ops.createTemp = func(dir, pattern string) (temporaryFile, error) {
		f, err := os.CreateTemp(dir, pattern)
		if err != nil {
			return nil, err
		}
		file = &faultFile{File: f}
		return file, nil
	}
	renamed, directorySynced := false, false
	ops.rename = func(from, to string) error {
		assertContents(t, to, "old")
		assertContents(t, from, "new")
		if !file.chmodded || !file.synced || !file.closed {
			t.Fatal("rename attempted before permissions, sync, and close")
		}
		err := os.Rename(from, to)
		renamed = err == nil
		return err
	}
	ops.syncDir = func(dir string) error {
		if !renamed || dir != filepath.Dir(path) {
			t.Fatal("directory synchronization must follow rename in destination parent")
		}
		assertContents(t, path, "new")
		directorySynced = true
		return nil
	}
	if err := write(path, []byte("new"), 0o600, ops); err != nil {
		t.Fatal(err)
	}
	if !directorySynced {
		t.Fatal("replacement returned success without directory synchronization")
	}
}

func TestCleanupFailuresRemainInspectable(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "state.json")
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	cleanupErr := errors.New("injected removal failure")
	closeErr := errors.New("injected cleanup close failure")
	ops := systemOperations()
	var name string
	ops.createTemp = func(dir, pattern string) (temporaryFile, error) {
		f, err := os.CreateTemp(dir, pattern)
		if err != nil {
			return nil, err
		}
		name = f.Name()
		return &cleanupFaultFile{File: f, closeErr: closeErr}, nil
	}
	ops.remove = func(string) error { return cleanupErr }
	err := write(path, []byte("new"), 0o600, ops)
	for _, cause := range []error{errInjected, cleanupErr, closeErr} {
		if !errors.Is(err, cause) {
			t.Fatalf("error = %v; missing %v", err, cause)
		}
	}
	var committed *CommittedError
	if errors.As(err, &committed) {
		t.Fatalf("cleanup failure incorrectly marked committed: %v", err)
	}
	assertContents(t, path, "old")
	if _, err := os.Stat(name); err != nil {
		t.Fatalf("expected temporary file after failed removal: %v", err)
	}
}

type cleanupFaultFile struct {
	*os.File
	closeErr error
}

func (f *cleanupFaultFile) Write([]byte) (int, error) { return 0, errInjected }
func (f *cleanupFaultFile) Close() error              { return errors.Join(f.File.Close(), f.closeErr) }

func TestCommittedErrorCanBeInspectedThroughWrapping(t *testing.T) {
	err := fmt.Errorf("save store: %w", &CommittedError{Path: "state.json", Err: errInjected})
	var committed *CommittedError
	if !errors.As(err, &committed) || committed.Path != "state.json" || !errors.Is(err, errInjected) {
		t.Fatalf("committed replacement is not inspectable: %v", err)
	}
}
