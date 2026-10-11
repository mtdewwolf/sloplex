//go:build unix

package atomicfile

import "os"

func syncDirectory(path string) error {
	return syncDirectoryWithOpen(path, func(path string) (directory, error) {
		return os.Open(path)
	})
}
