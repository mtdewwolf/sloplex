package transcode

import (
	"bytes"
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mtdewwolf/sloplex/internal/probe"
)

// testCapacity is a modest 4-core machine: fps at 720p per preset.
var testCapacity = Capacity{"faster": 112, "veryfast": 160, "superfast": 224, "ultrafast": 352}

func TestLoad(t *testing.T) {
	src := probe.Info{VideoCodec: "h264", Width: 1920, Height: 1080, FrameRate: 24}
	r := Request{Source: src, MaxWidth: 1280, MaxHeight: 720}
	p := Decide(r)
	prev := 1e9
	for _, preset := range autoPresets {
		l := testCapacity.Load(src, p, preset)
		if l <= 0 || l >= prev {
			t.Errorf("load at %s = %v, want positive and below the slower preset's %v", preset, l, prev)
		}
		prev = l
	}
	base := testCapacity.Load(src, p, DefaultPreset)
	hdr := src
	hdr.HDR = probe.HDRPQ
	if l := testCapacity.Load(hdr, Decide(Request{Source: hdr, MaxWidth: 1280, MaxHeight: 720}), DefaultPreset); l < 1.9*base {
		t.Errorf("tone mapping load %v, want well above SDR's %v", l, base)
	}
	fast := src
	fast.FrameRate = 48
	if l := testCapacity.Load(fast, p, DefaultPreset); l < 1.99*base || l > 2.01*base {
		t.Errorf("48 fps load %v, want twice %v", l, base)
	}
	unknown := src
	unknown.FrameRate = 0
	if l := testCapacity.Load(unknown, p, DefaultPreset); l < 1.24*base || l > 1.26*base {
		t.Errorf("unknown frame rate load %v, want 30 fps (%v)", l, base*30/24)
	}
	copied := Request{Source: probe.Info{VideoCodec: "h264", Width: 1280, Height: 720, AudioCodec: "aac", AudioChannels: 2}}
	if l := testCapacity.Load(copied.Source, Decide(copied), DefaultPreset); l != 0 {
		t.Errorf("copied video load = %v", l)
	}
	if l := Capacity(nil).Load(src, p, DefaultPreset); l != 0 {
		t.Errorf("unknown capacity load = %v", l)
	}
}

func TestChoosePreset(t *testing.T) {
	src := probe.Info{VideoCodec: "h264", Width: 1920, Height: 1080, FrameRate: 24}
	p := Decide(Request{Source: src, MaxWidth: 1280, MaxHeight: 720})
	for _, tc := range []struct {
		running float64
		want    string
	}{
		// Loads here: faster 0.366, veryfast 0.302, superfast 0.259, ultrafast 0.220.
		{0, "faster"}, {0.42, "veryfast"}, {0.47, "superfast"}, {0.52, "ultrafast"}, {5, "ultrafast"},
	} {
		got, load := testCapacity.ChoosePreset(tc.running, src, p)
		if got != tc.want || load != testCapacity.Load(src, p, got) {
			t.Errorf("running %v: preset %s (load %v), want %s", tc.running, got, load, tc.want)
		}
	}
	if got, load := Capacity(nil).ChoosePreset(0, src, p); got != DefaultPreset || load != 0 {
		t.Errorf("unknown capacity: %s %v", got, load)
	}
	// 4K HDR is too heavy for this machine at any preset.
	uhd := probe.Info{VideoCodec: "hevc", Width: 3840, Height: 2160, FrameRate: 24, HDR: probe.HDRPQ}
	if got, _ := testCapacity.ChoosePreset(0, uhd, Decide(Request{Source: uhd, MaxWidth: 1920, MaxHeight: 1080})); got != "ultrafast" {
		t.Errorf("4K HDR preset = %s", got)
	}
}

func TestValidPreset(t *testing.T) {
	for _, v := range []string{"auto", "ultrafast", "veryfast", "medium"} {
		if !ValidPreset(v) {
			t.Errorf("%q rejected", v)
		}
	}
	for _, v := range []string{"", "placebo", "fast "} {
		if ValidPreset(v) {
			t.Errorf("%q accepted", v)
		}
	}
}

func TestArgsPreset(t *testing.T) {
	r := Request{Input: "in.mkv", Source: clipInfo}
	p := Decide(r)
	if got := strings.Join(Args(r, p, Output{}, Pipeline{}), " "); !strings.Contains(got, "-preset veryfast -crf 21") {
		t.Errorf("default preset:\n%s", got)
	}
	p.Preset = "superfast"
	if got := strings.Join(Args(r, p, Output{}, Pipeline{}), " "); !strings.Contains(got, "-preset superfast -crf 21") {
		t.Errorf("chosen preset:\n%s", got)
	}
	if got := strings.Join(Args(r, p, Output{}, Pipeline{Accel: AccelNVENC}), " "); strings.Contains(got, "superfast") {
		t.Errorf("x264 preset leaked into a hardware pipeline:\n%s", got)
	}
}

func TestBenchmark(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not installed")
	}
	defer func(n int) { benchFrames = n }(benchFrames)
	benchFrames = 4
	c := Benchmark(context.Background(), ffmpeg, nil)
	prev := 0.0
	for i := len(autoPresets) - 1; i >= 0; i-- {
		preset := autoPresets[i]
		if c[preset] <= 0 || (prev > 0 && c[preset] >= prev) {
			t.Errorf("%s: %v fps, want positive and below the next faster preset (%v)", preset, c[preset], prev)
		}
		prev = c[preset]
	}
	if c := Benchmark(context.Background(), filepath.Join(t.TempDir(), "no-ffmpeg"), nil); c != nil {
		t.Errorf("failed benchmark = %v, want nil", c)
	}
}

// TestManagerPresets checks that the manager picks presets from capacity
// and load, and honours a configured preset.
func TestManagerPresets(t *testing.T) {
	ffmpeg, path := clip(t, "2")
	m, err := New(Options{FFmpeg: ffmpeg, Dir: filepath.Join(t.TempDir(), "tc"), MaxTranscodes: 3})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	stream := func(m *Manager) Plan {
		var plan Plan
		var buf bytes.Buffer
		if err := m.Stream(context.Background(), 1, Request{Input: path, Source: clipInfo}, &buf, func(p Plan) { plan = p }); err != nil {
			t.Fatal(err)
		}
		return plan
	}
	if p := stream(m); p.Preset != DefaultPreset {
		t.Errorf("before the benchmark: preset %q", p.Preset)
	}
	m.SetCapacity(testCapacity)
	if p := stream(m); p.Preset != "faster" {
		t.Errorf("idle machine: preset %q", p.Preset)
	}

	// Keep the scheduling input deterministic: a two-second real encoder
	// can finish before the second stream acquires the scheduling lock.
	// It is freshly touched, so the idle reaper leaves it alone; were it
	// reaped anyway, cancelling it ends it like a real session.
	done := make(chan struct{})
	s := &Session{done: done, load: 0.7, cancel: sync.OnceFunc(func() { close(done) })}
	s.lastTouch.Store(time.Now().UnixNano())
	s.pipeline.Store(&Pipeline{})
	m.mu.Lock()
	m.sessions["scheduling-test"] = s
	m.mu.Unlock()
	defer func() { m.mu.Lock(); delete(m.sessions, "scheduling-test"); m.mu.Unlock() }()
	p := stream(m)
	load := m.SoftwareLoad()
	m.mu.Lock()
	delete(m.sessions, "scheduling-test")
	m.mu.Unlock()
	if p.Preset != "ultrafast" {
		t.Errorf("loaded machine: preset %q", p.Preset)
	}
	if load != 0.7 {
		t.Errorf("SoftwareLoad = %v, want 0.7", load)
	}

	fixed, _ := New(Options{FFmpeg: ffmpeg, Dir: filepath.Join(t.TempDir(), "tc2"), MaxTranscodes: 1, Preset: "superfast"})
	defer fixed.Close()
	fixed.SetCapacity(testCapacity)
	if p := stream(fixed); p.Preset != "superfast" {
		t.Errorf("configured preset: %q", p.Preset)
	}
}

func TestWebMReservation(t *testing.T) {
	r := Request{Source: clipInfo, WebM: true}
	for _, configured := range []string{"", "auto", "medium", "ultrafast"} {
		m := &Manager{preset: configured, capacity: testCapacity}
		p := Decide(r)
		if load := m.choosePresetLocked(r, &p); load != targetLoad || p.Preset != "" || presetLog(p) != "" {
			t.Fatalf("configured %q: plan %+v load %v", configured, p, load)
		}
		args := strings.Join(Args(r, p, Output{}, Pipeline{}), " ")
		if !strings.Contains(args, "-c:v libvpx-vp9") || strings.Contains(args, "-preset") {
			t.Fatal(args)
		}
	}
	r.Source.VideoCodec = "vp9"
	if load := testCapacity.Load(r.Source, Decide(r), "medium"); load != 0 {
		t.Fatalf("copied VP9 load %v", load)
	}
}

func TestCapacityRefreshesExistingEncodes(t *testing.T) {
	r := Request{Source: clipInfo}
	p := Decide(r)
	p.Preset = DefaultPreset
	s := &Session{Request: r, Plan: p, done: make(chan struct{})}
	s.pipeline.Store(&Pipeline{})
	st := &streamInfo{source: r.Source, plan: p}
	m := &Manager{sessions: map[string]*Session{"test": s}, streams: map[*streamInfo]struct{}{st: {}}}
	c := Capacity{}
	for k, v := range testCapacity {
		c[k] = v
	}
	m.SetCapacity(c)
	want := 2 * testCapacity.Load(r.Source, p, DefaultPreset)
	if got := m.SoftwareLoad(); got <= 0 || got != want {
		t.Fatalf("load %v, want %v", got, want)
	}
	if s.Plan.Preset != DefaultPreset || st.plan.Preset != DefaultPreset {
		t.Fatal("running preset changed")
	}
	c[DefaultPreset] = 1
	if m.Capacity()[DefaultPreset] != testCapacity[DefaultPreset] {
		t.Fatal("capacity aliases caller map")
	}
}

func TestPartialHardwareCountsCPUFilters(t *testing.T) {
	r := Request{Source: probe.Info{VideoCodec: "hevc", Width: 3840, Height: 2160, FrameRate: 24, HDR: probe.HDRPQ}, MaxWidth: 1280, MaxHeight: 720}
	p := Decide(r)
	s := &Session{Request: r, Plan: p, done: make(chan struct{})}
	s.pipeline.Store(&Pipeline{Accel: AccelNVENC})
	m := &Manager{sessions: map[string]*Session{"test": s}}
	m.SetCapacity(testCapacity)
	if got, full := m.SoftwareLoad(), testCapacity.Load(r.Source, p, DefaultPreset); got <= 0 || got >= full {
		t.Fatalf("partial hardware load %v, software %v", got, full)
	}
	s.pipeline.Store(&Pipeline{Accel: AccelNVENC, Full: true})
	if got := m.SoftwareLoad(); got != 0 {
		t.Fatalf("full hardware CPU load %v", got)
	}
}
