// Package atomicfile synchronizes and replaces individual files. See
// docs/spec/storage-durability.md for platform and filesystem limitations.
package atomicfile

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// CommittedError means the replacement was installed by a successful rename,
// but finalizing directory durability failed. Callers must retain the newly
// written state instead of rolling back as though the old file were installed.
// Use errors.As to recognize this error, including through error wrapping.
type CommittedError struct {
	Path string
	Err  error
}

func (e *CommittedError) Error() string {
	return fmt.Sprintf("atomicfile %q: replacement installed, directory durability unconfirmed: %v", e.Path, e.Err)
}

func (e *CommittedError) Unwrap() error { return e.Err }

// Write replaces path with data via a synchronized temporary file and a rename,
// creating the parent directory if needed. On Unix it then synchronizes the
// parent directory. A *CommittedError means the new file is already installed.
// Success is subject to filesystem guarantees and does not confirm durability
// of newly created parent directories. Non-Unix rename and directory durability
// have weaker guarantees; see the package specification.
func Write(path string, data []byte, perm os.FileMode) error {
	return write(path, data, perm, systemOperations())
}

type temporaryFile interface {
	Name() string
	Write([]byte) (int, error)
	Chmod(os.FileMode) error
	Sync() error
	Close() error
}

// Operations belong to a single invocation, so fault-injection tests do not
// replace process-wide filesystem functions or interfere with other writers.
type operations struct {
	mkdirAll   func(string, os.FileMode) error
	createTemp func(string, string) (temporaryFile, error)
	remove     func(string) error
	rename     func(string, string) error
	syncDir    func(string) error
}

func systemOperations() operations {
	return operations{
		mkdirAll: os.MkdirAll,
		createTemp: func(dir, pattern string) (temporaryFile, error) {
			return os.CreateTemp(dir, pattern)
		},
		remove:  os.Remove,
		rename:  os.Rename,
		syncDir: syncDirectory,
	}
}

func operationError(op, path string, err error) error {
	return fmt.Errorf("atomicfile %s %q: %w", op, path, err)
}

func write(path string, data []byte, perm os.FileMode, ops operations) (err error) {
	dir := filepath.Dir(path)
	if err := ops.mkdirAll(dir, 0o755); err != nil {
		return operationError("create parent directory", dir, err)
	}
	tmp, err := ops.createTemp(dir, "."+filepath.Base(path)+"-*")
	if err != nil {
		return operationError("create temporary file", path, err)
	}
	name := tmp.Name()
	closed, renamed := false, false
	defer func() {
		if !closed {
			if closeErr := tmp.Close(); closeErr != nil {
				err = errors.Join(err, operationError("close temporary file", name, closeErr))
			}
		}
		if !renamed {
			if removeErr := ops.remove(name); removeErr != nil {
				err = errors.Join(err, operationError("remove temporary file", name, removeErr))
			}
		}
	}()
	if n, err := tmp.Write(data); err != nil {
		return operationError("write temporary file", name, err)
	} else if n != len(data) {
		return operationError("write temporary file", name, io.ErrShortWrite)
	}
	if err := tmp.Chmod(perm); err != nil {
		return operationError("chmod temporary file", name, err)
	}
	if err := tmp.Sync(); err != nil {
		return operationError("sync temporary file", name, err)
	}
	closed = true // Close releases the handle even when it reports an error.
	if err := tmp.Close(); err != nil {
		return operationError("close temporary file", name, err)
	}
	if err := ops.rename(name, path); err != nil {
		return operationError("rename temporary file", path, err)
	}
	renamed = true
	if err := ops.syncDir(dir); err != nil {
		return &CommittedError{Path: path, Err: err}
	}
	return nil
}
