// Package fswatch reports changes under directory trees. It uses inotify on
// Linux and is unavailable elsewhere; see "Watching" in docs/spec/http-api.md.
package fswatch

import "errors"

var (
	// ErrUnsupported is returned by New on systems without a watcher.
	ErrUnsupported = errors.New("watching folders is not supported on this system")
	// ErrLimit is returned by AddTree when the system's watch limit was
	// reached; part of the tree is not watched.
	ErrLimit = errors.New("watch limit reached (raise fs.inotify.max_user_watches)")
)

// Overflow is sent on Events when the system dropped events, so any
// watched path may have changed.
const Overflow = ""
