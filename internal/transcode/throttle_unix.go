//go:build unix

package transcode

import (
	"os"
	"syscall"
)

// canThrottle reports whether ffmpeg can be paused on this OS.
const canThrottle = true

func pauseProcess(p *os.Process) error  { return p.Signal(syscall.SIGSTOP) }
func resumeProcess(p *os.Process) error { return p.Signal(syscall.SIGCONT) }
