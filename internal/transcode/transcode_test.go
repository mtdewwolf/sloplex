package transcode

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mtdewwolf/sloplex/internal/probe"
)

func TestDecide(t *testing.T) {
	h264 := probe.Info{VideoCodec: "h264", Width: 1920, Height: 1080, Bitrate: 8000, AudioCodec: "aac", AudioChannels: 2}
	for name, tc := range map[string]struct {
		r                  Request
		copyV, copyA, hasA bool
		w, h               int
	}{
		"h264 aac fits":        {Request{Source: h264}, true, true, true, 1920, 1080},
		"box too small":        {Request{Source: h264, MaxWidth: 1280, MaxHeight: 720}, false, true, true, 1280, 720},
		"bitrate cap":          {Request{Source: h264, MaxBitrate: 4000}, false, true, true, 1920, 1080},
		"directStream=0":       {Request{Source: h264, NoDirectStream: true}, false, true, true, 1920, 1080},
		"hevc 5.1":             {Request{Source: probe.Info{VideoCodec: "hevc", Width: 3840, Height: 2160, AudioCodec: "eac3", AudioChannels: 6}, MaxWidth: 1920, MaxHeight: 1080}, false, false, true, 1920, 1080},
		"no audio":             {Request{Source: probe.Info{VideoCodec: "mpeg4", Width: 640, Height: 360}}, false, false, false, 640, 360},
		"odd scale stays even": {Request{Source: probe.Info{VideoCodec: "vp9", Width: 1000, Height: 750}, MaxWidth: 333}, false, false, false, 332, 248},
	} {
		p := Decide(tc.r)
		if p.CopyVideo != tc.copyV || p.CopyAudio != tc.copyA || p.HasAudio != tc.hasA || p.Width != tc.w || p.Height != tc.h {
			t.Errorf("%s: plan = %+v", name, p)
		}
		if p.Bandwidth <= 0 {
			t.Errorf("%s: no bandwidth estimate", name)
		}
	}
}

func TestArgs(t *testing.T) {
	r := Request{Input: "/m/a.mkv", Offset: 90.5, MaxWidth: 1280, MaxHeight: 720, MaxBitrate: 3000,
		Source: probe.Info{VideoCodec: "hevc", Width: 1920, Height: 1080, AudioCodec: "dts", AudioChannels: 6}}
	hls := strings.Join(Args(r, Decide(r), Output{HLSDir: "/tmp/s"}, Pipeline{}), " ")
	for _, want := range []string{
		"-ss 90.500 -i /m/a.mkv", "-map 0:a:0?", "-c:v libx264", "-maxrate 3000k -bufsize 6000k",
		"min(iw\\,1280)", "-force_key_frames expr:gte(t,n_forced*4)", "-c:a aac -ac 2",
		"-f hls", "-hls_segment_filename /tmp/s/%05d.ts /tmp/s/index.m3u8",
	} {
		if !strings.Contains(hls, want) {
			t.Errorf("HLS args missing %q:\n%s", want, hls)
		}
	}
	cp := Request{Input: "/m/b.mp4", Source: probe.Info{VideoCodec: "h264", Width: 1280, Height: 720, AudioCodec: "aac", AudioChannels: 2}}
	mp4 := Args(cp, Decide(cp), Output{}, Pipeline{})
	if !slices.Contains(mp4, "copy") || slices.Contains(mp4, "-ss") || mp4[len(mp4)-1] != "pipe:1" || slices.Contains(mp4, "-force_key_frames") {
		t.Errorf("copy MP4 args = %v", mp4)
	}
}

// clip makes a short MPEG-4/MP2 file that needs transcoding.
func clip(t *testing.T, seconds string) (ffmpeg, path string) {
	t.Helper()
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not installed")
	}
	path = filepath.Join(t.TempDir(), "clip.mkv")
	out, err := exec.Command(ffmpeg, "-v", "error", "-f", "lavfi", "-i", "testsrc=size=640x360:rate=24:duration="+seconds,
		"-f", "lavfi", "-i", "sine=duration="+seconds, "-shortest", "-c:v", "mpeg4", "-c:a", "mp2", path).CombinedOutput()
	if err != nil {
		t.Skipf("cannot make clip: %v %s", err, out)
	}
	return ffmpeg, path
}

var clipInfo = probe.Info{VideoCodec: "mpeg4", Width: 640, Height: 360, AudioCodec: "mp2", AudioChannels: 1}

func TestHLSSession(t *testing.T) {
	ffmpeg, path := clip(t, "10")
	dir := filepath.Join(t.TempDir(), "transcode")
	m, err := New(Options{FFmpeg: ffmpeg, Dir: dir, MaxTranscodes: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	ctx := context.Background()

	s, err := m.StartHLS(1, "player-1", 42, Request{Input: path, Source: clipInfo, MaxWidth: 320, MaxHeight: 180})
	if err != nil {
		t.Fatal(err)
	}
	if s.Plan.CopyVideo || s.Plan.Width != 320 || s.Plan.Height != 180 {
		t.Errorf("plan = %+v", s.Plan)
	}
	// Only one slot: a second session is refused.
	if _, err := m.StartHLS(2, "other", 43, Request{Input: path, Source: clipInfo}); !errors.Is(err, ErrBusy) {
		t.Errorf("second session = %v, want ErrBusy", err)
	}

	pl, err := m.Playlist(ctx, s, "base")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(pl)
	if !strings.Contains(string(b), "00000.ts") {
		t.Fatalf("playlist:\n%s", b)
	}
	seg, err := m.Segment(ctx, s, "base", "00000.ts")
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(seg)
	if len(data) < 188 || data[0] != 0x47 {
		t.Errorf("segment is not MPEG-TS (%d bytes)", len(data))
	}
	for _, bad := range []string{"../index.m3u8", "0.ts", "00000.mp4"} {
		if _, err := m.Segment(ctx, s, "base", bad); !errors.Is(err, ErrNotFound) {
			t.Errorf("Segment(%q) = %v", bad, err)
		}
	}
	// The whole 10 s clip finishes: the last segment and ENDLIST appear.
	if _, err := m.Segment(ctx, s, "base", "00002.ts"); err != nil {
		t.Errorf("last segment: %v", err)
	}
	<-s.done
	b, _ = os.ReadFile(pl)
	if !strings.Contains(string(b), "#EXT-X-ENDLIST") {
		t.Errorf("finished playlist lacks ENDLIST:\n%s", b)
	}
	if list := m.List(); len(list) != 1 || !list[0].Complete || list[0].Protocol != "hls" {
		t.Errorf("List = %+v", list)
	}

	// Restarting with the same label replaces the session (seeking).
	s2, err := m.StartHLS(1, "player-1", 42, Request{Input: path, Source: clipInfo, Offset: 5})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := m.Session(s.ID); ok {
		t.Error("old session still present")
	}
	if _, err := os.Stat(s.dir); !os.IsNotExist(err) {
		t.Error("old session directory not removed")
	}
	if n := m.Stop(1, "player-1"); n != 1 {
		t.Errorf("Stop = %d", n)
	}
	if _, err := os.Stat(s2.dir); !os.IsNotExist(err) {
		t.Error("stopped session directory not removed")
	}
}

func TestIdleReaper(t *testing.T) {
	ffmpeg, path := clip(t, "2")
	m, err := New(Options{FFmpeg: ffmpeg, Dir: filepath.Join(t.TempDir(), "tc"), MaxTranscodes: 2})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	m.SetIdleTimeout(500 * time.Millisecond)
	s, err := m.StartHLS(1, "x", 1, Request{Input: path, Source: clipInfo})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		m.mu.Lock()
		_, alive := m.sessions[s.ID]
		m.mu.Unlock()
		if !alive {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("idle session not reaped")
		}
		time.Sleep(100 * time.Millisecond)
	}
	if _, err := os.Stat(s.dir); !os.IsNotExist(err) {
		t.Error("reaped session directory not removed")
	}
}

func TestStreamMP4(t *testing.T) {
	ffmpeg, path := clip(t, "3")
	m, err := New(Options{FFmpeg: ffmpeg, Dir: filepath.Join(t.TempDir(), "tc"), MaxTranscodes: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	var buf bytes.Buffer
	var plan Plan
	if err := m.Stream(context.Background(), 7, Request{Input: path, Source: clipInfo}, &buf, func(p Plan) { plan = p }); err != nil {
		t.Fatal(err)
	}
	if buf.Len() < 1000 || !bytes.Equal(buf.Bytes()[4:8], []byte("ftyp")) {
		t.Errorf("output is not MP4 (%d bytes, starts %q)", buf.Len(), buf.Bytes()[:min(8, buf.Len())])
	}
	if plan.CopyVideo {
		t.Error("mpeg4 source should be transcoded")
	}

	// A broken input reports ffmpeg's error.
	err = m.Stream(context.Background(), 7, Request{Input: filepath.Join(t.TempDir(), "missing.mkv")}, &buf, func(Plan) {})
	if err == nil || !strings.Contains(err.Error(), "ffmpeg") {
		t.Errorf("missing input = %v", err)
	}

	// Without ffmpeg everything is unavailable.
	none, _ := New(Options{Dir: filepath.Join(t.TempDir(), "tc2"), MaxTranscodes: 1})
	defer none.Close()
	if _, err := none.StartHLS(1, "", 1, Request{}); !errors.Is(err, ErrUnavailable) {
		t.Errorf("no ffmpeg = %v", err)
	}
}

func TestFirstPTS(t *testing.T) {
	ffmpeg, path := clip(t, "6")
	m, _ := New(Options{FFmpeg: ffmpeg, Dir: filepath.Join(t.TempDir(), "tc"), MaxTranscodes: 1})
	defer m.Close()
	s, err := m.StartHLS(1, "", 1, Request{Input: path, Source: clipInfo, Offset: 1.5})
	if err != nil {
		t.Fatal(err)
	}
	seg, err := m.Segment(context.Background(), s, "base", "00000.ts")
	if err != nil {
		t.Fatal(err)
	}
	f, _ := os.Open(seg)
	defer f.Close()
	pts, err := FirstPTS(f)
	if err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("ffprobe", "-v", "error", "-select_streams", "v", "-show_entries", "packet=pts",
		"-read_intervals", "%+#1", "-of", "csv=p=0", seg).Output()
	if err != nil {
		t.Skip("ffprobe not installed")
	}
	want, _ := strconv.ParseInt(strings.Trim(strings.Split(string(out), "\n")[0], " ,"), 10, 64)
	if pts != want {
		t.Errorf("FirstPTS = %d, first video packet PTS = %d", pts, want)
	}
	if s.Request.Offset != 1.5 {
		t.Errorf("session request offset = %v", s.Request.Offset)
	}
	if _, err := FirstPTS(strings.NewReader("not a transport stream at all, definitely not 188 bytes of sync...." + strings.Repeat("x", 400))); err == nil {
		t.Error("garbage accepted")
	}
}
