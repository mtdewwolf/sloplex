package transcode

import (
	"bytes"
	"context"
	"encoding/json"
	"image/jpeg"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mtdewwolf/sloplex/internal/probe"
)

func TestArgsStreams(t *testing.T) {
	src := probe.Info{VideoCodec: "h264", Width: 1280, Height: 720, AudioCodec: "ac3", AudioChannels: 6}
	audio := Request{Input: "in.mkv", Source: src, SelectAudio: true, AudioIndex: 2}
	got := strings.Join(Args(audio, Decide(audio), Output{}, Pipeline{}), " ")
	if !strings.Contains(got, "-map 0:v:0 -map 0:2 ") || !strings.Contains(got, "-c:v copy") || !strings.Contains(got, "-c:a aac -ac 2") {
		t.Errorf("audio selection args: %s", got)
	}

	text := Request{Input: "in.mkv", Source: src, Offset: 12.5, Burn: &Burn{File: "/weird dir/it's:here/Movie.EN.ASS"}}
	p := Decide(text)
	if p.CopyVideo || !p.Burn {
		t.Fatalf("burn plan = %+v", p)
	}
	got = strings.Join(Args(text, p, Output{}, Pipeline{}), " ")
	if !strings.Contains(got, "-vf setpts=PTS+12.500/TB,subtitles=filename=burn.ass,setpts=PTS-STARTPTS") || strings.Contains(got, "weird") {
		t.Errorf("text burn args: %s", got)
	}

	image := Request{Input: "in.mkv", Source: src, MaxWidth: 640, MaxHeight: 360, Burn: &Burn{Index: 4}}
	got = strings.Join(Args(image, Decide(image), Output{}, Pipeline{}), " ")
	if !strings.Contains(got, "-filter_complex [0:v:0][0:4]overlay=eof_action=pass,scale=") || !strings.Contains(got, "[vout] -map [vout] -map 0:a:0?") {
		t.Errorf("image burn args: %s", got)
	}

	// Burning in skips the full-hardware pipeline; VAAPI encode-only uploads
	// after the subtitles are rendered.
	pls := Pipelines(HW{Accel: AccelVAAPI, Device: "/d"}, p)
	if len(pls) != 2 || pls[0].Full || pls[0].Accel != AccelVAAPI {
		t.Fatalf("burn pipelines = %v", pls)
	}
	got = strings.Join(Args(text, p, Output{}, pls[0]), " ")
	if !strings.Contains(got, "subtitles=filename=burn.ass,setpts=PTS-STARTPTS,format=nv12,hwupload") {
		t.Errorf("vaapi burn args: %s", got)
	}
}

// brightPixels counts near-white pixels in the frame at time t of an MP4.
func brightPixels(t *testing.T, ffmpeg, mp4 string, at string) int {
	t.Helper()
	out, err := exec.Command(ffmpeg, "-v", "error", "-ss", at, "-i", mp4, "-frames:v", "1", "-f", "image2pipe", "-c:v", "mjpeg", "pipe:1").Output()
	if err != nil {
		t.Fatalf("frame at %s: %v", at, err)
	}
	img, err := jpeg.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r, g, bl, _ := img.At(x, y).RGBA()
			if r > 0xc000 && g > 0xc000 && bl > 0xc000 {
				n++
			}
		}
	}
	return n
}

func TestBurnTextSubtitles(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not installed")
	}
	dir := t.TempDir()
	video := filepath.Join(dir, "black.mkv")
	if out, err := exec.Command(ffmpeg, "-v", "error", "-f", "lavfi", "-i", "color=black:s=640x360:r=10:d=6", "-c:v", "mpeg4", video).CombinedOutput(); err != nil {
		t.Skipf("cannot make video: %v %s", err, out)
	}
	// The subtitle file lives in a directory whose name would break an
	// unescaped filter argument.
	subDir := filepath.Join(dir, "it's: [tricky], dir")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatal(err)
	}
	srt := filepath.Join(subDir, "black.en.srt")
	if err := os.WriteFile(srt, []byte("1\n00:00:02,000 --> 00:00:04,000\nWWWWWWWWWWWW\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	m, err := New(Options{FFmpeg: ffmpeg, Dir: filepath.Join(dir, "tc"), MaxTranscodes: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	src := probe.Info{VideoCodec: "mpeg4", Width: 640, Height: 360}

	transcode := func(offset float64) string {
		var buf bytes.Buffer
		r := Request{Input: video, Source: src, Offset: offset, Burn: &Burn{File: srt}}
		if err := m.Stream(context.Background(), 1, r, &buf, func(Plan) {}); err != nil {
			t.Fatalf("offset %v: %v", offset, err)
		}
		out := filepath.Join(dir, "out.mp4")
		if err := os.WriteFile(out, buf.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
		return out
	}

	out := transcode(0)
	if n := brightPixels(t, ffmpeg, out, "1.0"); n != 0 {
		t.Errorf("subtitle visible before its start: %d bright pixels", n)
	}
	if n := brightPixels(t, ffmpeg, out, "3.0"); n < 100 {
		t.Fatalf("subtitle not rendered at 3s (%d bright pixels); is a font installed?", n)
	}
	// Started at 1.5s: output time 0.3 is source 1.8 (no subtitle), output
	// 1.5 is source 3.0 (subtitle).
	out = transcode(1.5)
	if n := brightPixels(t, ffmpeg, out, "0.3"); n != 0 {
		t.Errorf("offset stream shows subtitle too early: %d bright pixels", n)
	}
	if n := brightPixels(t, ffmpeg, out, "1.5"); n < 100 {
		t.Errorf("offset stream lost the subtitle at source 3.0s: %d bright pixels", n)
	}
	if entries, _ := os.ReadDir(filepath.Join(dir, "tc")); len(entries) != 0 {
		t.Errorf("stream working directories left behind: %v", entries)
	}
}

func TestSelectedAudioIsUsed(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not installed")
	}
	dir := t.TempDir()
	video := filepath.Join(dir, "two-audio.mkv")
	if out, err := exec.Command(ffmpeg, "-v", "error", "-f", "lavfi", "-i", "color=black:s=320x240:r=10:d=2",
		"-f", "lavfi", "-i", "sine=f=440:d=2", "-f", "lavfi", "-i", "sine=f=880:d=2",
		"-map", "0", "-map", "1", "-map", "2", "-c:v", "libx264", "-c:a:0", "aac", "-c:a:1", "ac3", "-ac:a:1", "6",
		"-metadata:s:a:0", "language=eng", "-metadata:s:a:1", "language=fre", video).CombinedOutput(); err != nil {
		t.Skipf("cannot make video: %v %s", err, out)
	}
	m, _ := New(Options{FFmpeg: ffmpeg, Dir: filepath.Join(dir, "tc"), MaxTranscodes: 1})
	defer m.Close()
	var buf bytes.Buffer
	r := Request{Input: video, SelectAudio: true, AudioIndex: 2,
		Source: probe.Info{VideoCodec: "h264", Width: 320, Height: 240, AudioCodec: "ac3", AudioChannels: 6}}
	var plan Plan
	if err := m.Stream(context.Background(), 1, r, &buf, func(p Plan) { plan = p }); err != nil {
		t.Fatal(err)
	}
	if !plan.CopyVideo || plan.CopyAudio {
		t.Errorf("plan = %+v, want video copy and audio transcode", plan)
	}
	out := filepath.Join(dir, "out.mp4")
	_ = os.WriteFile(out, buf.Bytes(), 0o644)
	js, err := exec.Command("ffprobe", "-v", "error", "-select_streams", "a", "-show_entries", "stream=codec_name,channels:stream_tags=language", "-of", "json", out).Output()
	if err != nil {
		t.Skipf("ffprobe: %v", err)
	}
	var res struct {
		Streams []struct {
			Codec    string            `json:"codec_name"`
			Channels int               `json:"channels"`
			Tags     map[string]string `json:"tags"`
		}
	}
	_ = json.Unmarshal(js, &res)
	if len(res.Streams) != 1 || res.Streams[0].Codec != "aac" || res.Streams[0].Channels != 2 || res.Streams[0].Tags["language"] != "fre" {
		t.Errorf("output audio = %+v, want one AAC stereo stream from the French track", res.Streams)
	}
}
