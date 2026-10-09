package probe

import (
	"context"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestParseFFprobe(t *testing.T) {
	out := []byte(`{
		"streams": [
			{"codec_type": "video", "codec_name": "hevc", "width": 3840, "height": 2160},
			{"codec_type": "audio", "codec_name": "eac3", "channels": 6},
			{"codec_type": "audio", "codec_name": "aac", "channels": 2},
			{"codec_type": "subtitle", "codec_name": "subrip"}
		],
		"format": {"duration": "7265.123000", "bit_rate": "15000000"}
	}`)
	got, err := parseFFprobe(out)
	if err != nil {
		t.Fatal(err)
	}
	want := Info{Duration: 7265123, Bitrate: 15000, Width: 3840, Height: 2160, VideoCodec: "hevc", AudioCodec: "eac3", AudioChannels: 6}
	if got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestContainerForPath(t *testing.T) {
	if c := ContainerForPath("/a/B.MKV"); c != "mkv" {
		t.Errorf("mkv: %q", c)
	}
	if c := ContainerForPath("/a/b.m4v"); c != "mp4" {
		t.Errorf("m4v: %q", c)
	}
	if IsVideo("/a/b.srt") {
		t.Error("srt reported as video")
	}
}

// TestFFprobeReal exercises the real binary on a tiny generated clip when
// ffmpeg and ffprobe are installed.
func TestFFprobeReal(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not installed")
	}
	p, err := NewFFprobe("")
	if err != nil {
		t.Skip("ffprobe not installed")
	}
	path := filepath.Join(t.TempDir(), "clip.mkv")
	cmd := exec.Command(ffmpeg, "-v", "error", "-f", "lavfi", "-i", "testsrc=size=320x240:duration=1:rate=10",
		"-f", "lavfi", "-i", "sine=duration=1", "-shortest", "-c:v", "mpeg4", "-c:a", "mp2", path)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("ffmpeg could not generate clip: %v\n%s", err, out)
	}
	info, err := p.Probe(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Width != 320 || info.Height != 240 || info.Container != "mkv" || info.Duration < 900 || info.AudioCodec == "" {
		t.Errorf("info = %+v", info)
	}
}
