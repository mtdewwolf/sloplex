package artwork

import (
	"context"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
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

func TestFrame(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not installed")
	}
	dir := t.TempDir()
	video := filepath.Join(dir, "ep.mkv")
	if out, err := exec.Command(ffmpeg, "-v", "error", "-f", "lavfi", "-i", "testsrc=size=1280x720:duration=2:rate=10",
		"-c:v", "mpeg4", video).CombinedOutput(); err != nil {
		t.Skipf("cannot generate clip: %v %s", err, out)
	}
	s := New(filepath.Join(dir, "cache"), ffmpeg)
	// A bogus long duration seeks past the end; Frame must fall back to 0.
	for _, dur := range []int64{2000, 3_600_000} {
		path, err := s.Frame(context.Background(), 7, dur, video, dur)
		if err != nil {
			t.Fatalf("duration %d: %v", dur, err)
		}
		f, err := os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		cfg, err := jpeg.DecodeConfig(f)
		f.Close()
		if err != nil || cfg.Width != 640 || cfg.Height != 360 {
			t.Errorf("duration %d: frame %+v, %v", dur, cfg, err)
		}
	}
	if New(dir, "").CanGenerate() {
		t.Error("CanGenerate without ffmpeg")
	}
}
