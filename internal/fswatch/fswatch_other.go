//go:build !linux

package fswatch

// Watcher is unavailable on this system.
type Watcher struct{}

// New returns ErrUnsupported.
func New(skip func(dirName string) bool) (*Watcher, error) { return nil, ErrUnsupported }

// AddTree does nothing.
func (w *Watcher) AddTree(root string) error { return ErrUnsupported }

// Events returns nil.
func (w *Watcher) Events() <-chan string { return nil }

// Close does nothing.
func (w *Watcher) Close() error { return nil }
