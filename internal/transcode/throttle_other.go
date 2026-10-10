//go:build !unix

package transcode

import (
	"errors"
	"os"
)

// canThrottle reports whether ffmpeg can be paused on this OS.
const canThrottle = false

func pauseProcess(*os.Process) error  { return errors.ErrUnsupported }
func resumeProcess(*os.Process) error { return errors.ErrUnsupported }
