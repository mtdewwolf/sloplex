//go:build !unix

package atomicfile

// Directory synchronization is unavailable in this implementation on non-Unix
// platforms. File synchronization still precedes rename.
func syncDirectory(string) error { return nil }
