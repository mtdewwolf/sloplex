package transcode

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/mtdewwolf/sloplex/internal/probe"
)

func TestDecideWebM(t *testing.T) {
	for _, c := range []struct {
		src        probe.Info
		copyV, cpA bool
	}{
		{probe.Info{VideoCodec: "vp9", AudioCodec: "opus", AudioChannels: 2, Width: 640, Height: 360}, true, true},
		{probe.Info{VideoCodec: "h264", AudioCodec: "aac", AudioChannels: 2, Width: 640, Height: 360}, false, false},
		{probe.Info{VideoCodec: "vp9", AudioCodec: "opus", AudioChannels: 6, Width: 640, Height: 360}, true, false},
		{probe.Info{VideoCodec: "vp9", Width: 640, Height: 360, HDR: probe.HDRPQ}, false, false},
	} {
		p := Decide(Request{Source: c.src, WebM: true})
		if !p.WebM || p.CopyVideo != c.copyV || p.CopyAudio != c.cpA {
			t.Errorf("Decide(%+v) = %+v, want copy video %v, audio %v", c.src, p, c.copyV, c.cpA)
		}
	}

	r := Request{Input: "in.mkv", WebM: true, MaxWidth: 1280, MaxHeight: 720, MaxBitrate: 2000,
		Source: probe.Info{VideoCodec: "mpeg4", AudioCodec: "mp2", AudioChannels: 2, Width: 1920, Height: 1080}}
	p := Decide(r)
	// VP9 is encoded in software even when a GPU is active.
	pls := Pipelines(HW{Accel: AccelVAAPI, Device: "/d"}, p)
	if len(pls) != 1 || pls[0].Accel != "" {
		t.Fatalf("WebM pipelines = %v", pls)
	}
	got := strings.Join(Args(r, p, Output{}, pls[0]), " ")
	for _, want := range []string{"-vf scale=", "-c:v libvpx-vp9 -deadline realtime", "-b:v 2000k -maxrate 2000k -bufsize 4000k",
		"-c:a libopus -ac 2", "-metadata:s DURATION= -f webm pipe:1"} {
		if !strings.Contains(got, want) {
			t.Errorf("WebM args lack %q: %s", want, got)
		}
	}
	if strings.Contains(got, "movflags") || strings.Contains(got, "libx264") {
		t.Errorf("WebM args carry MP4 options: %s", got)
	}
}

func TestStreamWebM(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not installed")
	}
	dir := t.TempDir()
	// A Matroska source, so it carries the per-stream DURATION tags that
	// must not leak into a stream started at an offset.
	video := filepath.Join(dir, "legacy.mkv")
	if out, err := exec.Command(ffmpeg, "-v", "error", "-f", "lavfi", "-i", "testsrc=size=640x360:rate=24:duration=8",
		"-f", "lavfi", "-i", "sine=duration=8", "-shortest", "-c:v", "mpeg4", "-c:a", "mp2", video).CombinedOutput(); err != nil {
		t.Skipf("cannot make clip: %v %s", err, out)
	}
	m, err := New(Options{FFmpeg: ffmpeg, Dir: filepath.Join(dir, "tc"), MaxTranscodes: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	var buf bytes.Buffer
	r := Request{Input: video, Offset: 5, WebM: true, MaxWidth: 320, MaxHeight: 180,
		Source: probe.Info{VideoCodec: "mpeg4", AudioCodec: "mp2", AudioChannels: 1, Width: 640, Height: 360}}
	if err := m.Stream(context.Background(), Owner{Account: 1}, 1, r, &buf, func(Plan) {}); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "out.webm")
	if err := os.WriteFile(out, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := exec.Command("ffprobe", "-v", "error", "-show_entries", "stream=codec_name,width:format=format_name,duration", "-of", "csv=p=0", out).Output()
	if err != nil {
		t.Skipf("ffprobe: %v", err)
	}
	lines := strings.Fields(strings.ReplaceAll(string(res), `"`, ""))
	if len(lines) != 3 || lines[0] != "vp9,320" || !strings.HasPrefix(lines[1], "opus") || !strings.HasPrefix(lines[2], "matroska,webm") {
		t.Fatalf("output = %q, want 320-wide VP9 and Opus in WebM", lines)
	}
	// The stream is 3 s long: no duration (it was written as a stream), or
	// about 3 s, but never the source's 8 s.
	if d := strings.TrimPrefix(lines[2], "matroska,webm,"); d != "N/A" {
		if f, _ := strconv.ParseFloat(d, 64); f > 4 {
			t.Errorf("WebM claims a duration of %s s for a 3 s stream", d)
		}
	}
}
