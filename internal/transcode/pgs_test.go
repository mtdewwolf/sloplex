package transcode

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mtdewwolf/sloplex/internal/probe"
	"github.com/mtdewwolf/sloplex/internal/testutil"
)

// TestBurnPGSSubtitles burns a real PGS (Blu-ray) subtitle track in with
// the overlay pipeline and checks the pixels, with and without an offset.
func TestBurnPGSSubtitles(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not installed")
	}
	dir := t.TempDir()
	sup := filepath.Join(dir, "subs.sup")
	testutil.WriteSUP(t, sup, 640, 360, 220, 260, 200, 60, 2000, 4000)
	video := filepath.Join(dir, "blu-ray.mkv")
	// -copyts keeps the subtitle at 2s; otherwise ffmpeg rebases each input
	// to start at 0, as mkvmerge would not.
	if out, err := exec.Command(ffmpeg, "-v", "error", "-f", "lavfi", "-i", "color=black:s=640x360:r=10:d=6", "-i", sup,
		"-map", "0", "-map", "1", "-c:v", "libx264", "-c:s", "copy", "-copyts", video).CombinedOutput(); err != nil {
		t.Fatalf("muxing the PGS track failed: %v %s", err, out)
	}
	if info, err := (&probeRunner{}).streams(video); err != nil || !strings.Contains(info, "hdmv_pgs_subtitle") {
		t.Fatalf("muxed file has no PGS stream: %q %v", info, err)
	}

	m, err := New(Options{FFmpeg: ffmpeg, Dir: filepath.Join(dir, "tc"), MaxTranscodes: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	transcode := func(offset float64) string {
		var buf bytes.Buffer
		r := Request{Input: video, Offset: offset, Burn: &Burn{Index: 1},
			Source: probe.Info{VideoCodec: "h264", Width: 640, Height: 360}}
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
	if n := testutil.BrightPixels(t, ffmpeg, out, "1.0"); n != 0 {
		t.Errorf("PGS box visible before it starts: %d bright pixels", n)
	}
	if n := testutil.BrightPixels(t, ffmpeg, out, "3.0"); n < 200*60/2 {
		t.Fatalf("PGS box not burned in at 3s: %d bright pixels, want about %d", n, 200*60)
	}
	if n := testutil.BrightPixels(t, ffmpeg, out, "5.0"); n != 0 {
		t.Errorf("PGS box still visible after it ends: %d bright pixels", n)
	}
	out = transcode(1.5)
	if n := testutil.BrightPixels(t, ffmpeg, out, "0.3"); n != 0 {
		t.Errorf("offset stream shows the PGS box too early: %d bright pixels", n)
	}
	if n := testutil.BrightPixels(t, ffmpeg, out, "1.5"); n < 200*60/2 {
		t.Errorf("offset stream lost the PGS box at source 3.0s: %d bright pixels", n)
	}
}

// TestBurnPGSOnDownscaledHDR burns a PGS subtitle into an HDR source that is
// tone mapped at a smaller output size, so the subtitle canvas has to be
// scaled to match the picture.
func TestBurnPGSOnDownscaledHDR(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not installed")
	}
	dir := t.TempDir()
	hdr := filepath.Join(dir, "hdr.mkv")
	testutil.WriteHDRClip(t, ffmpeg, hdr, "smpte2084", 64, 64) // black
	sup := filepath.Join(dir, "subs.sup")
	testutil.WriteSUP(t, sup, 640, 360, 220, 260, 200, 60, 500, 1500)
	video := filepath.Join(dir, "uhd.mkv")
	// Keep the fixture's video packets in presentation order. On ffmpeg 7,
	// a short HEVC clip with B-frames can advance the subtitle-to-video
	// heartbeat past the clear packet before decoding it, even with a plain
	// unscaled overlay. This test checks canvas scaling, not that upstream
	// subtitle timing limitation.
	if out, err := exec.Command(ffmpeg, "-v", "error", "-i", hdr, "-i", sup,
		"-map", "0", "-map", "1", "-c:v", "libx265", "-x265-params", "bframes=0:log-level=error",
		"-c:s", "copy", "-copyts", video).CombinedOutput(); err != nil {
		t.Fatalf("muxing the PGS track failed: %v %s", err, out)
	}
	m, err := New(Options{FFmpeg: ffmpeg, Dir: filepath.Join(dir, "tc"), MaxTranscodes: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	var buf bytes.Buffer
	r := Request{Input: video, Burn: &Burn{Index: 1}, MaxWidth: 320, MaxHeight: 180,
		Source: probe.Info{VideoCodec: "hevc", Width: 640, Height: 360, HDR: probe.HDRPQ}}
	if err := m.Stream(context.Background(), 1, r, &buf, func(Plan) {}); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "out.mp4")
	if err := os.WriteFile(out, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	// The 200×60 box at (220, 260) becomes 100×30 at (110, 130).
	if n := testutil.BrightPixels(t, ffmpeg, out, "0.2"); n != 0 {
		t.Errorf("PGS box visible before it starts: %d bright pixels", n)
	}
	if n := testutil.BrightPixels(t, ffmpeg, out, "1.0"); n < 100*30/2 || n > 100*30*2 {
		t.Errorf("PGS box at 1s: %d bright pixels, want about %d", n, 100*30)
	}
	if mid := testutil.AverageLuma(t, ffmpeg, out, "1.0", "100:30:110:130"); mid < 200 {
		t.Errorf("PGS box not where the scaled canvas puts it: average luma %d", mid)
	}
	if n := testutil.BrightPixels(t, ffmpeg, out, "1.8"); n != 0 {
		t.Errorf("PGS box still visible after it ends: %d bright pixels", n)
	}
}

type probeRunner struct{}

func (probeRunner) streams(path string) (string, error) {
	out, err := exec.Command("ffprobe", "-v", "error", "-show_entries", "stream=codec_name", "-of", "csv=p=0", path).Output()
	return string(out), err
}
