package transcode

import (
	"bufio"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// HLS throttling; see "Throttling" in docs/spec/transcode.md.

// Default throttle limits: how far the newest segment may run ahead of the
// newest one a player asked for before ffmpeg is paused, and how close it
// must come again before ffmpeg resumes.
const (
	DefaultPauseAhead  = 90 * time.Second
	DefaultResumeAhead = 45 * time.Second
)

// ahead is how much media the session has produced beyond the newest
// segment requested.
func (s *Session) ahead() time.Duration {
	dir, _ := s.LevelDir("base")
	f, err := os.Open(filepath.Join(dir, "index.m3u8"))
	if err != nil {
		return 0
	}
	defer f.Close()
	requested := s.requested.Load()
	var lead float64
	var duration float64
	var index int64
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(line, "#EXTINF:") {
			value, _, _ := strings.Cut(strings.TrimPrefix(line, "#EXTINF:"), ",")
			duration, _ = strconv.ParseFloat(value, 64)
			if duration <= 0 || math.IsNaN(duration) || math.IsInf(duration, 0) {
				duration = segmentSeconds
			}
		} else if line != "" && !strings.HasPrefix(line, "#") {
			if index > requested {
				lead += duration
			}
			index++
			duration = segmentSeconds
		}
	}
	return time.Duration(lead * float64(time.Second))
}

// requestSegment records that a player asked for segment n and wakes the
// throttle so a paused ffmpeg resumes at once when needed.
func (s *Session) requestSegment(n int) {
	for {
		cur := s.requested.Load()
		if int64(n) <= cur || s.requested.CompareAndSwap(cur, int64(n)) {
			break
		}
	}
	select {
	case s.nudge <- struct{}{}:
	default:
	}
}

// Throttled reports whether the session's ffmpeg is paused.
func (s *Session) Throttled() bool { return s.throttled.Load() }

// runThrottled runs cmd for s, pausing it while it is too far ahead of the
// player when throttling is on.
func (m *Manager) runThrottled(cmd *exec.Cmd, s *Session) error {
	if err := cmd.Start(); err != nil {
		return err
	}
	if !m.throttle || !canThrottle {
		return cmd.Wait()
	}
	done, stopped := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(stopped)
		m.throttleLoop(s, cmd.Process, done)
	}()
	err := cmd.Wait()
	close(done)
	<-stopped
	return err
}

func (m *Manager) throttleLoop(s *Session, proc *os.Process, done <-chan struct{}) {
	t := time.NewTicker(m.throttleTick)
	defer t.Stop()
	paused := false
	defer func() {
		if paused {
			resumeProcess(proc)
		}
		s.throttled.Store(false)
	}()
	for {
		select {
		case <-done:
			return
		case <-t.C:
		case <-s.nudge:
		}
		ahead := s.ahead()
		switch {
		case !paused && ahead > m.pauseAhead:
			if err := pauseProcess(proc); err == nil {
				paused = true
				s.throttled.Store(true)
				m.log.Debug("transcode throttled", "session", s.ID, "ahead", ahead)
			}
		case paused && ahead <= m.resumeAhead:
			if err := resumeProcess(proc); err == nil {
				paused = false
				s.throttled.Store(false)
				m.log.Debug("transcode resumed", "session", s.ID, "ahead", ahead)
			}
		}
	}
}
