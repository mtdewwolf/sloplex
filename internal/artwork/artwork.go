// Package artwork generates and resizes images: ffmpeg frame grabs for
// episode thumbnails and the cached resizer behind /photo/:/transcode. See
// docs/spec/artwork.md.
package artwork

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"image/draw"
	"image/jpeg"
	_ "image/png" // register the PNG decoder
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/mtdewwolf/sloplex/internal/atomicfile"
)

// MaxPixels is the largest source image the resizer accepts.
const MaxPixels = 50_000_000

// ErrTooLarge is returned for sources over MaxPixels.
var ErrTooLarge = errors.New("image too large")

// Service caches generated and resized images under a directory.
type Service struct {
	cacheDir string
	ffmpeg   string

	mu    sync.Mutex
	locks map[string]*sync.Mutex
}

// New returns a Service caching under cacheDir. ffmpeg is the path of the
// ffmpeg binary, or "" to disable frame grabs.
func New(cacheDir, ffmpeg string) *Service {
	return &Service{cacheDir: cacheDir, ffmpeg: ffmpeg, locks: map[string]*sync.Mutex{}}
}

// CanGenerate reports whether frame grabs are available.
func (s *Service) CanGenerate() bool { return s != nil && s.ffmpeg != "" }

// lock serialises work on one cache key so concurrent requests for the same
// image run the expensive step once.
func (s *Service) lock(key string) func() {
	s.mu.Lock()
	l := s.locks[key]
	if l == nil {
		l = &sync.Mutex{}
		s.locks[key] = l
	}
	s.mu.Unlock()
	l.Lock()
	return l.Unlock
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// Frame returns a cached JPEG frame grab of a video part, generating it on
// first use.
func (s *Service) Frame(ctx context.Context, partID int, modTime int64, video string, durationMs int64) (string, error) {
	if !s.CanGenerate() {
		return "", errors.New("frame grabs disabled")
	}
	out := filepath.Join(s.cacheDir, "thumbs", fmt.Sprintf("%d-%d.jpg", partID, modTime))
	defer s.lock(out)()
	if exists(out) {
		return out, nil
	}
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return "", err
	}

	at := 5 * time.Second
	if durationMs > 0 {
		at = time.Duration(durationMs) * time.Millisecond / 10
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	tmp := out + ".tmp.jpg"
	defer os.Remove(tmp)
	grab := func(at time.Duration) error {
		cmd := exec.CommandContext(ctx, s.ffmpeg, "-v", "error", "-y",
			"-ss", strconv.FormatFloat(at.Seconds(), 'f', 3, 64), "-i", video,
			"-frames:v", "1", "-vf", "scale=640:-2", "-q:v", "3", tmp)
		if b, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("ffmpeg: %w: %s", err, b)
		}
		if !exists(tmp) {
			// Seeking past the end yields no frame and no error.
			return errors.New("ffmpeg produced no frame")
		}
		return nil
	}
	err := grab(at)
	if err != nil && at > 0 {
		err = grab(0) // very short file or bad duration: fall back to the start
	}
	if err != nil {
		return "", err
	}
	return out, os.Rename(tmp, out)
}

// Options describes a resize.
type Options struct {
	Width, Height int
	// Cover scales to cover the box and centre-crops; otherwise fit inside.
	Cover bool
	// Upscale allows enlarging small sources.
	Upscale bool
}

// Transcode returns a cached JPEG of src resized per opts.
func (s *Service) Transcode(src string, opts Options) (string, error) {
	fi, err := os.Stat(src)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(fmt.Appendf(nil, "%s\x00%d\x00%d\x00%d\x00%t\x00%t",
		src, fi.ModTime().UnixNano(), opts.Width, opts.Height, opts.Cover, opts.Upscale))
	out := filepath.Join(s.cacheDir, "photo", hex.EncodeToString(sum[:16])+".jpg")
	defer s.lock(out)()
	if exists(out) {
		return out, nil
	}

	f, err := os.Open(src)
	if err != nil {
		return "", err
	}
	defer f.Close()
	cfg, _, err := image.DecodeConfig(f)
	if err != nil {
		return "", fmt.Errorf("decode %s: %w", src, err)
	}
	if cfg.Width*cfg.Height > MaxPixels {
		return "", ErrTooLarge
	}
	if _, err := f.Seek(0, 0); err != nil {
		return "", err
	}
	img, _, err := image.Decode(f)
	if err != nil {
		return "", fmt.Errorf("decode %s: %w", src, err)
	}

	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, Resize(img, opts), &jpeg.Options{Quality: 85}); err != nil {
		return "", err
	}
	return out, atomicfile.Write(out, buf.Bytes(), 0o644)
}

// Resize scales img into the box described by opts using an area-averaging
// filter, preserving the aspect ratio.
func Resize(img image.Image, opts Options) image.Image {
	sb := img.Bounds()
	sw, sh := sb.Dx(), sb.Dy()
	if sw == 0 || sh == 0 || opts.Width <= 0 || opts.Height <= 0 {
		return img
	}
	fx, fy := float64(opts.Width)/float64(sw), float64(opts.Height)/float64(sh)
	scale := min(fx, fy)
	if opts.Cover {
		scale = max(fx, fy)
	}
	if scale > 1 && !opts.Upscale {
		scale = 1
	}
	dw, dh := max(1, int(float64(sw)*scale+0.5)), max(1, int(float64(sh)*scale+0.5))

	// Crop the source so the scaled result fits the box when covering.
	crop := sb
	if opts.Cover {
		cw := min(sw, int(float64(opts.Width)/scale+0.5))
		ch := min(sh, int(float64(opts.Height)/scale+0.5))
		x0, y0 := sb.Min.X+(sw-cw)/2, sb.Min.Y+(sh-ch)/2
		crop = image.Rect(x0, y0, x0+cw, y0+ch)
		dw, dh = min(dw, opts.Width), min(dh, opts.Height)
	}

	src := image.NewRGBA(image.Rect(0, 0, crop.Dx(), crop.Dy()))
	draw.Draw(src, src.Bounds(), img, crop.Min, draw.Src)
	if dw == crop.Dx() && dh == crop.Dy() {
		return src
	}
	return boxResize(src, dw, dh)
}

// boxResize averages the source pixels covered by each destination pixel.
// When enlarging, each destination pixel covers at least one source pixel.
func boxResize(src *image.RGBA, dw, dh int) *image.RGBA {
	sw, sh := src.Bounds().Dx(), src.Bounds().Dy()
	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))
	for y := 0; y < dh; y++ {
		y0 := y * sh / dh
		y1 := max(y0+1, (y+1)*sh/dh)
		for x := 0; x < dw; x++ {
			x0 := x * sw / dw
			x1 := max(x0+1, (x+1)*sw/dw)
			var r, g, b, a, n uint64
			for sy := y0; sy < y1; sy++ {
				row := src.Pix[sy*src.Stride:]
				for sx := x0; sx < x1; sx++ {
					p := row[sx*4 : sx*4+4]
					r, g, b, a = r+uint64(p[0]), g+uint64(p[1]), b+uint64(p[2]), a+uint64(p[3])
					n++
				}
			}
			d := dst.Pix[y*dst.Stride+x*4:]
			d[0], d[1], d[2], d[3] = uint8(r/n), uint8(g/n), uint8(b/n), uint8(a/n)
		}
	}
	return dst
}
