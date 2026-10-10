package artwork

import (
	"context"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func solid(w, h int, c color.Color) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, c)
		}
	}
	return img
}

func TestResizeGeometry(t *testing.T) {
	src := solid(1000, 1500, color.RGBA{200, 100, 50, 255}) // 2:3 poster
	for _, tt := range []struct {
		opts Options
		w, h int
	}{
		{Options{Width: 300, Height: 300}, 200, 300},                    // fit
		{Options{Width: 300, Height: 300, Cover: true}, 300, 300},       // cover + crop
		{Options{Width: 2000, Height: 3000}, 1000, 1500},                // no upscale
		{Options{Width: 2000, Height: 3000, Upscale: true}, 2000, 3000}, // upscale
		{Options{Width: 1920, Height: 1080, Cover: true}, 1000, 1080},   // cover without upscale
	} {
		got := Resize(src, tt.opts).Bounds()
		if got.Dx() != tt.w || got.Dy() != tt.h {
			t.Errorf("Resize(%+v) = %dx%d, want %dx%d", tt.opts, got.Dx(), got.Dy(), tt.w, tt.h)
		}
	}
}

func TestResizeAverages(t *testing.T) {
	// Alternating black and white columns average to mid grey.
	src := image.NewRGBA(image.Rect(0, 0, 4, 2))
	for y := 0; y < 2; y++ {
		for x := 0; x < 4; x++ {
			v := uint8(0)
			if x%2 == 1 {
				v = 254
			}
			src.Set(x, y, color.RGBA{v, v, v, 255})
		}
	}
	out := Resize(src, Options{Width: 2, Height: 1}).(*image.RGBA)
	if c := out.RGBAAt(0, 0); c.R != 127 || c.A != 255 {
		t.Errorf("averaged pixel = %+v, want grey 127", c)
	}
}

func TestTranscodeCaches(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "poster.png")
	f, err := os.Create(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(f, solid(400, 600, color.White)); err != nil {
		t.Fatal(err)
	}
	f.Close()

	s := New(filepath.Join(dir, "cache"), "")
	out, err := s.Transcode(src, Options{Width: 100, Height: 150})
	if err != nil {
		t.Fatal(err)
	}
	g, err := os.Open(out)
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	img, err := jpeg.Decode(g)
	if err != nil {
		t.Fatal(err)
	}
	if b := img.Bounds(); b.Dx() != 100 || b.Dy() != 150 {
		t.Errorf("output %v", b)
	}
	again, err := s.Transcode(src, Options{Width: 100, Height: 150})
	if err != nil || again != out {
		t.Errorf("second call = %q, %v; want cached %q", again, err, out)
	}
	other, _ := s.Transcode(src, Options{Width: 50, Height: 50})
	if other == out {
		t.Error("different options share a cache entry")
	}
	if _, err := s.Transcode(filepath.Join(dir, "missing.jpg"), Options{Width: 1, Height: 1}); err == nil {
		t.Error("missing source succeeded")
	}
}

// clip writes a test video with ffmpeg, skipping the test if it cannot.
func clip(t *testing.T, ffmpeg, path string, args ...string) string {
	t.Helper()
	args = append([]string{"-v", "error", "-f", "lavfi", "-i"}, args...)
	if out, err := exec.Command(ffmpeg, append(args, path)...).CombinedOutput(); err != nil {
		t.Skipf("cannot generate clip: %v %s", err, out)
	}
	return path
}

// meanLuma decodes a JPEG and averages its luma.
func meanLuma(t *testing.T, path string) float64 {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	img, err := jpeg.Decode(f)
	if err != nil {
		t.Fatal(err)
	}
	y := img.(*image.YCbCr)
	sum := 0
	for _, v := range y.Y {
		sum += int(v)
	}
	return float64(sum) / float64(len(y.Y))
}

func TestFrame(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not installed")
	}
	dir := t.TempDir()
	hd := clip(t, ffmpeg, filepath.Join(dir, "hd.mkv"), "testsrc=size=1280x720:duration=2:rate=10", "-c:v", "mpeg4")
	scope := clip(t, ffmpeg, filepath.Join(dir, "scope.mkv"), "testsrc2=size=1280x544:duration=3:rate=24", "-c:v", "mpeg4")
	// 720x480 with 32:27 pixels displays at 16:9.
	anamorphic := clip(t, ffmpeg, filepath.Join(dir, "dvd.mkv"), "testsrc2=size=720x480:duration=2:rate=24",
		"-vf", "setsar=32/27", "-c:v", "mpeg4")
	s := New(filepath.Join(dir, "cache"), ffmpeg)

	part := 0
	for _, c := range []struct {
		name  string
		kind  Still
		video string
		dur   int64
		w, h  int
	}{
		{"episode", EpisodeThumb, hd, 2000, 640, 360},
		// A bogus long duration seeks past the end; Frame falls back to 0.
		{"episode, bad duration", EpisodeThumb, hd, 3_600_000, 640, 360},
		{"poster", MoviePoster, scope, 3000, 600, 900},
		{"poster, bad duration", MoviePoster, scope, 3_600_000, 600, 900},
		{"poster, unknown duration", MoviePoster, scope, 0, 600, 900},
		{"background", MovieArt, scope, 3000, 1280, 544},
		{"anamorphic background", MovieArt, anamorphic, 2000, 1280, 720},
		{"anamorphic episode", EpisodeThumb, anamorphic, 2000, 640, 360},
	} {
		part++
		path, err := s.Frame(context.Background(), c.kind, Source{PartID: part, ModTime: 1, File: c.video, Duration: c.dur})
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		f, err := os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		cfg, err := jpeg.DecodeConfig(f)
		f.Close()
		if err != nil || cfg.Width != c.w || cfg.Height != c.h {
			t.Errorf("%s: %dx%d (%v), want %dx%d", c.name, cfg.Width, cfg.Height, err, c.w, c.h)
		}
	}

	// Each kind has its own cache entry for the same part, reused later.
	src := Source{PartID: 100, ModTime: 1, File: scope, Duration: 3000}
	poster, _ := s.Frame(context.Background(), MoviePoster, src)
	art, _ := s.Frame(context.Background(), MovieArt, src)
	thumb, _ := s.Frame(context.Background(), EpisodeThumb, src)
	if poster == art || art == thumb || poster == thumb {
		t.Errorf("kinds share cache entries: %s %s %s", poster, art, thumb)
	}
	if again, err := s.Frame(context.Background(), MoviePoster, Source{PartID: 100, ModTime: 1, File: "/missing"}); err != nil || again != poster {
		t.Errorf("cached poster not reused: %q %v", again, err)
	}
	if _, err := s.Frame(context.Background(), Still(99), src); err == nil {
		t.Error("unknown kind succeeded")
	}
	if New(dir, "").CanGenerate() {
		t.Error("CanGenerate without ffmpeg")
	}
}

// TestFrameHDR checks that HDR video is tone mapped: the still of a PQ clip
// differs clearly from the same clip read as SDR.
func TestFrameHDR(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not installed")
	}
	dir := t.TempDir()
	pq := clip(t, ffmpeg, filepath.Join(dir, "pq.mkv"), "testsrc2=size=1280x720:duration=2:rate=24",
		"-pix_fmt", "yuv420p10le", "-c:v", "libx265", "-x265-params", "log-level=error",
		"-color_primaries", "bt2020", "-color_trc", "smpte2084", "-colorspace", "bt2020nc")
	s := New(filepath.Join(dir, "cache"), ffmpeg)
	naive, err := s.Frame(context.Background(), MovieArt, Source{PartID: 1, ModTime: 1, File: pq, Duration: 2000})
	if err != nil {
		t.Fatal(err)
	}
	mapped, err := s.Frame(context.Background(), MovieArt, Source{PartID: 2, ModTime: 1, File: pq, Duration: 2000, HDR: "pq"})
	if err != nil {
		t.Fatal(err)
	}
	if n, m := meanLuma(t, naive), meanLuma(t, mapped); math.Abs(m-n) < 20 {
		t.Errorf("tone mapped luma %.0f, naive %.0f; want a clear difference", m, n)
	}
}

// TestFrameLimit checks that generation waits for a free slot and gives up
// when the request does.
func TestFrameLimit(t *testing.T) {
	// A missing binary fails at once, so only waiting can hit the deadline.
	s := New(t.TempDir(), filepath.Join(t.TempDir(), "no-ffmpeg"))
	if cap(s.gen) < 1 {
		t.Fatal("no generation slots")
	}
	for i := 0; i < cap(s.gen); i++ {
		s.gen <- struct{}{}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := s.Frame(ctx, MoviePoster, Source{PartID: 1, File: "/nonexistent"}); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Frame with every slot taken: %v", err)
	}
	<-s.gen
	if _, err := s.Frame(context.Background(), MoviePoster, Source{PartID: 1, File: "/nonexistent"}); err == nil || errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Frame with a free slot: %v", err)
	}
}
