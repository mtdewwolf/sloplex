package transcode

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Errors returned by Manager.
var (
	ErrUnavailable = errors.New("transcoding unavailable: ffmpeg not found")
	ErrBusy        = errors.New("too many transcodes")
	ErrNotFound    = errors.New("not found")
)

// Manager runs and tracks transcodes.
type Manager struct {
	ffmpeg    string
	dir       string
	max       int
	hw        HW
	requested string
	log       *slog.Logger
	idle      time.Duration
	pollGap   time.Duration

	mu       sync.Mutex
	sessions map[string]*Session
	streams  map[*streamInfo]struct{}
	closed   bool
	stop     chan struct{}
	wg       sync.WaitGroup
}

// Session is a running or finished HLS transcode.
type Session struct {
	ID        string
	Account   int
	Label     string
	ItemID    int
	Plan      Plan
	dir       string
	cancel    context.CancelFunc
	done      chan struct{}
	err       error // set before done is closed
	lastTouch atomic.Int64
	pipeline  atomic.Pointer[Pipeline]
}

type streamInfo struct {
	id       string
	itemID   int
	plan     Plan
	started  time.Time
	pipeline Pipeline // guarded by Manager.mu
}

// Info describes a running transcode for listings.
type Info struct {
	ID       string
	ItemID   int
	Protocol string
	Plan     Plan
	Pipeline Pipeline
	Complete bool
}

// Options configures a Manager.
type Options struct {
	// FFmpeg is the ffmpeg binary; empty disables transcoding.
	FFmpeg string
	// Dir holds HLS sessions. It is cleared of leftovers on start.
	Dir           string
	MaxTranscodes int
	// HW is the detected hardware acceleration (zero for software), and
	// Requested the configured hwaccel value, for reporting.
	HW        HW
	Requested string
	Log       *slog.Logger
}

// New returns a Manager.
func New(o Options) (*Manager, error) {
	if o.Log == nil {
		o.Log = slog.New(slog.DiscardHandler)
	}
	if o.MaxTranscodes < 1 {
		o.MaxTranscodes = 1
	}
	if err := os.RemoveAll(o.Dir); err != nil {
		return nil, err
	}
	m := &Manager{
		ffmpeg: o.FFmpeg, dir: o.Dir, max: o.MaxTranscodes, hw: o.HW, requested: o.Requested, log: o.Log,
		idle: 2 * time.Minute, pollGap: 100 * time.Millisecond,
		sessions: map[string]*Session{}, streams: map[*streamInfo]struct{}{},
		stop: make(chan struct{}),
	}
	m.wg.Add(1)
	go m.reaper()
	return m, nil
}

// SetIdleTimeout changes how long an untouched session survives. For tests.
func (m *Manager) SetIdleTimeout(d time.Duration) { m.mu.Lock(); m.idle = d; m.mu.Unlock() }

// Available reports whether ffmpeg is configured.
func (m *Manager) Available() bool { return m != nil && m.ffmpeg != "" }

// HW returns the active hardware acceleration.
func (m *Manager) HW() HW { return m.hw }

// Requested returns the configured hwaccel value.
func (m *Manager) Requested() string { return m.requested }

// MaxTranscodes returns the concurrency limit.
func (m *Manager) MaxTranscodes() int { return m.max }

func newID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

// activeLocked counts running transcodes. It must hold m.mu.
func (m *Manager) activeLocked() int {
	n := len(m.streams)
	for _, s := range m.sessions {
		select {
		case <-s.done:
		default:
			n++
		}
	}
	return n
}

// StartHLS starts an HLS session. A running session with the same account
// and non-empty label is stopped first.
func (m *Manager) StartHLS(account int, label string, itemID int, r Request) (*Session, error) {
	if !m.Available() {
		return nil, ErrUnavailable
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil, ErrUnavailable
	}
	var replaced []*Session
	if label != "" {
		for id, s := range m.sessions {
			if s.Account == account && s.Label == label {
				replaced = append(replaced, s)
				delete(m.sessions, id)
			}
		}
	}
	for _, s := range replaced {
		s.cancel()
	}
	if m.activeLocked() >= m.max {
		m.mu.Unlock()
		m.cleanup(replaced)
		return nil, ErrBusy
	}
	p := Decide(r)
	s := &Session{ID: newID(), Account: account, Label: label, ItemID: itemID, Plan: p, done: make(chan struct{})}
	s.dir = filepath.Join(m.dir, s.ID)
	s.lastTouch.Store(time.Now().UnixNano())
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		m.mu.Unlock()
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	attempts := Pipelines(m.hw, p)
	s.pipeline.Store(&attempts[0])
	m.sessions[s.ID] = s
	m.mu.Unlock()
	m.cleanup(replaced)

	m.log.Info("transcode started", "session", s.ID, "item", itemID, "protocol", "hls",
		"video", decision(p.CopyVideo), "audio", audioDecision(p), "pipeline", attempts[0].String())
	go m.runHLS(ctx, s, r, attempts)
	return s, nil
}

// runHLS runs the pipelines in order until one succeeds. It moves on only
// when a pipeline fails before writing any segment.
func (m *Manager) runHLS(ctx context.Context, s *Session, r Request, attempts []Pipeline) {
	defer close(s.done)
	for i, pl := range attempts {
		s.pipeline.Store(&pl)
		if err := prepareBurn(r, s.dir); err != nil {
			s.err = err
			return
		}
		cmd := exec.CommandContext(ctx, m.ffmpeg, Args(r, s.Plan, Output{HLSDir: s.dir}, pl)...)
		cmd.Dir = s.dir
		var stderr bytes.Buffer
		cmd.Stderr = &limitedWriter{w: &stderr, n: 8 << 10}
		err := cmd.Run()
		if err == nil || ctx.Err() != nil {
			return
		}
		fail := ffmpegError(err, &stderr)
		if hasSegments(s.dir) || i == len(attempts)-1 {
			s.err = fail
			m.log.Warn("transcode failed", "session", s.ID, "pipeline", pl.String(), "err", fail)
			return
		}
		m.log.Warn("transcode pipeline failed, falling back", "session", s.ID,
			"pipeline", pl.String(), "next", attempts[i+1].String(), "err", fail)
		clearDir(s.dir)
	}
}

func ffmpegError(err error, stderr *bytes.Buffer) error {
	return fmt.Errorf("ffmpeg: %w: %s", err, strings.TrimSpace(stderr.String()))
}

// prepareBurn copies a text subtitle to be burned in into the working
// directory under its fixed name.
func prepareBurn(r Request, dir string) error {
	if r.Burn == nil || r.Burn.File == "" {
		return nil
	}
	b, err := os.ReadFile(r.Burn.File)
	if err != nil {
		return fmt.Errorf("subtitle for burn-in: %w", err)
	}
	return os.WriteFile(filepath.Join(dir, BurnFile(r.Burn.File)), b, 0o644)
}

func hasSegments(dir string) bool {
	matches, _ := filepath.Glob(filepath.Join(dir, "*.ts"))
	return len(matches) > 0
}

func clearDir(dir string) {
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		os.RemoveAll(filepath.Join(dir, e.Name()))
	}
}

// cleanup waits for sessions to exit and removes their files.
func (m *Manager) cleanup(sessions []*Session) {
	for _, s := range sessions {
		s.cancel()
		<-s.done
		os.RemoveAll(s.dir)
	}
}

// Session returns a session by ID and marks it active.
func (m *Manager) Session(id string) (*Session, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[id]
	if ok {
		s.lastTouch.Store(time.Now().UnixNano())
	}
	return s, ok
}

func (m *Manager) matching(account int, label string) []*Session {
	var out []*Session
	for _, s := range m.sessions {
		if s.Account == account && s.Label == label && label != "" {
			out = append(out, s)
		}
	}
	return out
}

// Stop ends an account's sessions with a label and returns how many.
func (m *Manager) Stop(account int, label string) int {
	m.mu.Lock()
	found := m.matching(account, label)
	for _, s := range found {
		delete(m.sessions, s.ID)
	}
	m.mu.Unlock()
	m.cleanup(found)
	return len(found)
}

// Ping marks an account's sessions with a label as active.
func (m *Manager) Ping(account int, label string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	found := m.matching(account, label)
	for _, s := range found {
		s.lastTouch.Store(time.Now().UnixNano())
	}
	return len(found)
}

// List describes all running transcodes.
func (m *Manager) List() []Info {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Info
	for _, s := range m.sessions {
		complete := false
		select {
		case <-s.done:
			complete = true
		default:
		}
		out = append(out, Info{ID: s.ID, ItemID: s.ItemID, Protocol: "hls", Plan: s.Plan, Pipeline: *s.pipeline.Load(), Complete: complete})
	}
	for st := range m.streams {
		out = append(out, Info{ID: st.id, ItemID: st.itemID, Protocol: "http", Plan: st.plan, Pipeline: st.pipeline})
	}
	return out
}

func (m *Manager) reaper() {
	defer m.wg.Done()
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		select {
		case <-m.stop:
			return
		case <-t.C:
		}
		m.mu.Lock()
		cutoff := time.Now().Add(-m.idle).UnixNano()
		var stale []*Session
		for id, s := range m.sessions {
			if s.lastTouch.Load() < cutoff {
				stale = append(stale, s)
				delete(m.sessions, id)
			}
		}
		m.mu.Unlock()
		for _, s := range stale {
			m.log.Info("transcode idle, stopping", "session", s.ID)
		}
		m.cleanup(stale)
	}
}

// Close stops every transcode and removes the working directory.
func (m *Manager) Close() {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	m.closed = true
	all := make([]*Session, 0, len(m.sessions))
	for id, s := range m.sessions {
		all = append(all, s)
		delete(m.sessions, id)
	}
	m.mu.Unlock()
	close(m.stop)
	m.wg.Wait()
	m.cleanup(all)
	os.RemoveAll(m.dir)
}

var segmentRE = regexp.MustCompile(`^\d{5}\.ts$`)

// Playlist waits until the media playlist lists at least one segment and
// returns its path.
func (m *Manager) Playlist(ctx context.Context, s *Session) (string, error) {
	path := filepath.Join(s.dir, "index.m3u8")
	return path, m.wait(ctx, s, func() bool {
		segs, _ := listedSegments(path)
		return len(segs) > 0
	})
}

// Segment waits until a segment is listed in the playlist and returns its
// path.
func (m *Manager) Segment(ctx context.Context, s *Session, name string) (string, error) {
	if !segmentRE.MatchString(name) {
		return "", ErrNotFound
	}
	playlist := filepath.Join(s.dir, "index.m3u8")
	return filepath.Join(s.dir, name), m.wait(ctx, s, func() bool {
		segs, _ := listedSegments(playlist)
		return segs[name]
	})
}

// wait polls ready until it holds, the session's ffmpeg exits, or 30 s or
// ctx run out.
func (m *Manager) wait(ctx context.Context, s *Session, ready func() bool) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	t := time.NewTicker(m.pollGap)
	defer t.Stop()
	for {
		if ready() {
			return nil
		}
		select {
		case <-s.done:
			if ready() {
				return nil
			}
			if s.err != nil {
				return s.err
			}
			return ErrNotFound
		case <-ctx.Done():
			return ErrNotFound
		case <-t.C:
		}
	}
}

// listedSegments reads the segment names in a media playlist.
func listedSegments(path string) (map[string]bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	out := map[string]bool{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line != "" && !strings.HasPrefix(line, "#") {
			out[filepath.Base(line)] = true
		}
	}
	return out, sc.Err()
}

// Stream writes a fragmented MP4 transcode to w until it ends or ctx is
// cancelled. ready is called once the transcode has a slot, before any
// output.
func (m *Manager) Stream(ctx context.Context, itemID int, r Request, w io.Writer, ready func(Plan)) error {
	if !m.Available() {
		return ErrUnavailable
	}
	p := Decide(r)
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return ErrUnavailable
	}
	if m.activeLocked() >= m.max {
		m.mu.Unlock()
		return ErrBusy
	}
	st := &streamInfo{id: newID(), itemID: itemID, plan: p, started: time.Now()}
	m.streams[st] = struct{}{}
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		delete(m.streams, st)
		m.mu.Unlock()
	}()

	workdir := ""
	if r.Burn != nil && r.Burn.File != "" {
		if err := os.MkdirAll(m.dir, 0o755); err != nil {
			return err
		}
		dir, err := os.MkdirTemp(m.dir, "stream-")
		if err != nil {
			return err
		}
		defer os.RemoveAll(dir)
		if err := prepareBurn(r, dir); err != nil {
			return err
		}
		workdir = dir
	}
	attempts := Pipelines(m.hw, p)
	m.log.Info("transcode started", "session", st.id, "item", itemID, "protocol", "http",
		"video", decision(p.CopyVideo), "audio", audioDecision(p), "pipeline", attempts[0].String())
	for i, pl := range attempts {
		m.mu.Lock()
		st.pipeline = pl
		m.mu.Unlock()
		wrote, err := m.streamOnce(ctx, r, p, pl, workdir, w, func() { ready(p) })
		if err == nil || wrote || ctx.Err() != nil || i == len(attempts)-1 {
			return err
		}
		m.log.Warn("transcode pipeline failed, falling back", "session", st.id,
			"pipeline", pl.String(), "next", attempts[i+1].String(), "err", err)
	}
	return nil
}

// streamOnce runs one pipeline. wrote reports whether any output reached
// w; ready is called just before the first write. A client disconnect is
// not an error.
func (m *Manager) streamOnce(ctx context.Context, r Request, p Plan, pl Pipeline, workdir string, w io.Writer, ready func()) (wrote bool, err error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := exec.CommandContext(ctx, m.ffmpeg, Args(r, p, Output{}, pl)...)
	cmd.Dir = workdir
	var stderr bytes.Buffer
	cmd.Stderr = &limitedWriter{w: &stderr, n: 8 << 10}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return false, err
	}
	if err := cmd.Start(); err != nil {
		return false, err
	}

	// Hold back the first chunk: if this pipeline fails before producing
	// anything, the caller can still try the next one.
	buf := make([]byte, 32<<10)
	var n int
	var rerr error
	for n == 0 && rerr == nil {
		n, rerr = stdout.Read(buf)
	}
	if n == 0 {
		waitErr := cmd.Wait()
		if ctx.Err() != nil {
			return false, nil
		}
		if waitErr == nil {
			waitErr = errors.New("no output")
		}
		return false, ffmpegError(waitErr, &stderr)
	}

	ready()
	_, copyErr := w.Write(buf[:n])
	if copyErr == nil {
		_, copyErr = io.Copy(w, stdout)
	}
	// Decide before cancelling: afterwards ctx.Err() is always set.
	clientGone := copyErr != nil || ctx.Err() != nil
	if clientGone {
		cancel()
	}
	waitErr := cmd.Wait()
	if clientGone {
		return true, nil
	}
	if waitErr != nil {
		return true, ffmpegError(waitErr, &stderr)
	}
	return true, nil
}

func decision(copy bool) string {
	if copy {
		return "copy"
	}
	return "transcode"
}

func audioDecision(p Plan) string {
	if !p.HasAudio {
		return ""
	}
	return decision(p.CopyAudio)
}

// Decisions returns the video and audio decision strings of a plan.
func Decisions(p Plan) (video, audio string) { return decision(p.CopyVideo), audioDecision(p) }

// limitedWriter keeps the first n bytes written to it.
type limitedWriter struct {
	w io.Writer
	n int
}

func (l *limitedWriter) Write(p []byte) (int, error) {
	if l.n > 0 {
		k := min(len(p), l.n)
		l.w.Write(p[:k])
		l.n -= k
	}
	return len(p), nil
}
