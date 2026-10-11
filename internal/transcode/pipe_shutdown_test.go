package transcode

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// An exited wrapper can leave a descendant holding stderr open. Waiting for
// that pipe must be bounded even though the wrapper itself exited successfully.
func TestProgressiveInheritedStderr(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires POSIX shell")
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "ffmpeg")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nsleep 3 >&2 &\nprintf payload\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	m := &Manager{ffmpeg: script}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	wrote, err := m.streamOnce(ctx, Request{Input: "unused", Source: clipInfo}, Plan{}, Pipeline{}, dir, io.Discard, func() {})
	if !wrote || !errors.Is(err, exec.ErrWaitDelay) {
		t.Fatalf("wrote=%t error=%v; want output and bounded pipe wait error", wrote, err)
	}
}
