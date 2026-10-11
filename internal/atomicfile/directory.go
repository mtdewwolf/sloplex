package atomicfile

import "errors"

type directory interface {
	Sync() error
	Close() error
}

func syncDirectoryWithOpen(path string, open func(string) (directory, error)) error {
	dir, err := open(path)
	if err != nil {
		return operationError("open parent directory", path, err)
	}
	var syncErr, closeErr error
	if err := dir.Sync(); err != nil {
		syncErr = operationError("sync parent directory", path, err)
	}
	if err := dir.Close(); err != nil {
		closeErr = operationError("close parent directory", path, err)
	}
	return errors.Join(syncErr, closeErr)
}
