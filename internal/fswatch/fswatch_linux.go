package fswatch

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
)

const mask = syscall.IN_CREATE | syscall.IN_DELETE | syscall.IN_MOVED_FROM | syscall.IN_MOVED_TO |
	syscall.IN_CLOSE_WRITE | syscall.IN_ONLYDIR

// Watcher watches directory trees with inotify.
type Watcher struct {
	fd     int
	f      *os.File
	skip   func(string) bool
	events chan string
	done   chan struct{}
	once   sync.Once

	mu    sync.Mutex
	dirs  map[int32]string // watch descriptor -> directory
	wds   map[string]int32
	limit bool
}

// New starts a watcher. skip names directories (below a root) that are not
// watched; names starting with "." are never watched or reported.
func New(skip func(dirName string) bool) (*Watcher, error) {
	fd, err := syscall.InotifyInit1(syscall.IN_CLOEXEC | syscall.IN_NONBLOCK)
	if err != nil {
		return nil, os.NewSyscallError("inotify_init1", err)
	}
	if skip == nil {
		skip = func(string) bool { return false }
	}
	w := &Watcher{
		fd: fd, f: os.NewFile(uintptr(fd), "inotify"), skip: skip,
		events: make(chan string, 256), done: make(chan struct{}), dirs: map[int32]string{}, wds: map[string]int32{},
	}
	go w.run()
	return w, nil
}

// Events delivers the path of every changed entry, or Overflow. It is
// closed by Close.
func (w *Watcher) Events() <-chan string { return w.events }

// Close stops the watcher.
func (w *Watcher) Close() error {
	w.once.Do(func() { close(w.done) })
	return w.f.Close()
}

// send delivers an event unless the watcher is closing.
func (w *Watcher) send(path string) {
	select {
	case w.events <- path:
	case <-w.done:
	}
}

// AddTree watches root and the directories below it. It returns ErrLimit if
// the watch limit stopped it early, and an error if root cannot be watched.
func (w *Watcher) AddTree(root string) error {
	root = filepath.Clean(root)
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if path == root {
				return err
			}
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if !d.IsDir() {
			return nil
		}
		if path != root && w.ignored(d.Name()) {
			return fs.SkipDir
		}
		if err := w.add(path); err != nil {
			if errors.Is(err, ErrLimit) || path == root {
				return err
			}
			return fs.SkipDir // vanished or unreadable
		}
		return nil
	})
}

func (w *Watcher) ignored(name string) bool { return strings.HasPrefix(name, ".") || w.skip(name) }

func (w *Watcher) add(dir string) error {
	wd, err := syscall.InotifyAddWatch(w.fd, dir, mask)
	if err != nil {
		if errors.Is(err, syscall.ENOSPC) {
			return ErrLimit
		}
		return &fs.PathError{Op: "inotify_add_watch", Path: dir, Err: err}
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if old, ok := w.dirs[int32(wd)]; ok && old != dir {
		delete(w.wds, old)
	}
	w.dirs[int32(wd)] = dir
	w.wds[dir] = int32(wd)
	return nil
}

// forget stops watching dir and everything below it, for a directory that
// was moved away; its new location, if watched, is added again.
func (w *Watcher) forget(dir string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for path, wd := range w.wds {
		if path == dir || strings.HasPrefix(path, dir+string(filepath.Separator)) {
			_, _ = syscall.InotifyRmWatch(w.fd, uint32(wd))
			delete(w.wds, path)
			delete(w.dirs, wd)
		}
	}
}

func (w *Watcher) run() {
	defer close(w.events)
	buf := make([]byte, 64<<10)
	for {
		n, err := w.f.Read(buf)
		if err != nil {
			return
		}
		for off := 0; off+syscall.SizeofInotifyEvent <= n; {
			wd := int32(binary.NativeEndian.Uint32(buf[off:]))
			m := binary.NativeEndian.Uint32(buf[off+4:])
			size := int(binary.NativeEndian.Uint32(buf[off+12:]))
			start := off + syscall.SizeofInotifyEvent
			name := string(bytes.TrimRight(buf[start:min(start+size, n)], "\x00"))
			off = start + size
			w.handle(wd, m, name)
		}
	}
}

func (w *Watcher) handle(wd int32, m uint32, name string) {
	if m&syscall.IN_Q_OVERFLOW != 0 {
		w.send(Overflow)
		return
	}
	w.mu.Lock()
	dir, ok := w.dirs[wd]
	if ok && m&syscall.IN_IGNORED != 0 {
		delete(w.dirs, wd)
		if w.wds[dir] == wd {
			delete(w.wds, dir)
		}
	}
	w.mu.Unlock()
	if !ok || name == "" || strings.HasPrefix(name, ".") {
		return
	}
	path := filepath.Join(dir, name)
	if m&syscall.IN_ISDIR != 0 {
		switch {
		case w.skip(name):
			return
		case m&(syscall.IN_CREATE|syscall.IN_MOVED_TO) != 0:
			_ = w.AddTree(path) // a limit error leaves it to interval scans
		case m&syscall.IN_MOVED_FROM != 0:
			w.forget(path)
		}
	}
	w.send(path)
}
