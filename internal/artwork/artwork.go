// Package artwork generates and resizes images: ffmpeg stills for items
// without local artwork, and the cached resizer behind /photo/:/transcode.
// See docs/spec/artwork.md.
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
	"runtime"
	"strconv"
	"sync"
	"time"

	"github.com/mtdewwolf/sloplex/internal/atomicfile"
	"github.com/mtdewwolf/sloplex/internal/transcode"
)

// MaxPixels is the largest source image the resizer accepts.
const MaxPixels = 50_000_000

// ErrTooLarge is returned for sources over MaxPixels.
var ErrTooLarge = errors.New("image too large")

// Service caches generated and resized images under a directory.
type Service struct {
	cacheDir string
	ffmpeg   string
	gen      chan struct{} // limits concurrent generation

	mu    sync.Mutex
	locks map[string]*sync.Mutex
}

// New returns a Service caching under cacheDir. ffmpeg is the path of the
// ffmpeg binary, or "" to disable generated images.
func New(cacheDir, ffmpeg string) *Service {
	return &Service{cacheDir: cacheDir, ffmpeg: ffmpeg, locks: map[string]*sync.Mutex{},
		gen: make(chan struct{}, max(1, runtime.NumCPU()/2))}
}

// CanGenerate reports whether generated images are available.
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

// Still is a kind of generated image; see "Generated images" in
// docs/spec/artwork.md.
type Still int

const (
	EpisodeThumb Still = iota
	MoviePoster
	MovieArt
)

type still struct {
	dir   string
	at    float64 // where in the video, as a fraction of its duration
	width int
	after string // filters after scaling and tone mapping
}

var stills = map[Still]still{
	EpisodeThumb: {"thumbs", 0.1, 640, ""},
	MoviePoster: {"posters", 0.2, 960, "thumbnail=24,split[a][b];" +
		"[a]scale=600:900:force_original_aspect_ratio=increase,crop=600:900,boxblur=20:2,eq=brightness=-0.12[bg];" +
		"[b]scale=600:900:force_original_aspect_ratio=decrease[fg];[bg][fg]overlay=(W-w)/2:(H-h)/2"},
	MovieArt: {"backgrounds", 1.0 / 3, 1280, "thumbnail=24"},
}

// Source is the video part a still is made from.
type Source struct {
	PartID   int
	ModTime  int64
	File     string
	Duration int64  // milliseconds; 0 if unknown
	HDR      string // probe.HDRPQ or probe.HDRHLG to tone map to SDR
}

// filter is the ffmpeg filter graph for a still: square pixels at the
// still's width, then SDR, then the still's own filters.
func (st still) filter(hdr string) string {
	f := fmt.Sprintf("scale=%d:trunc(%d*ih/(iw*sar)/2)*2,setsar=1", st.width, st.width)
	if hdr != "" {
		f += "," + transcode.ToneMapFilter(hdr, 0, 0)
	}
	if st.after != "" {
		f += "," + st.after
	}
	return f
}

// Frame returns a cached JPEG still of a video part, generating it on first
// use.
func (s *Service) Frame(ctx context.Context, kind Still, src Source) (string, error) {
	if !s.CanGenerate() {
		return "", errors.New("generated images disabled")
	}
	st, ok := stills[kind]
	if !ok {
		return "", fmt.Errorf("unknown still %d", kind)
	}
	out := filepath.Join(s.cacheDir, st.dir, fmt.Sprintf("%d-%d.jpg", src.PartID, src.ModTime))
	defer s.lock(out)()
	if exists(out) {
		return out, nil
	}
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return "", err
	}
	select {
	case s.gen <- struct{}{}:
		defer func() { <-s.gen }()
	case <-ctx.Done():
		return "", ctx.Err()
	}

	at := 5 * time.Second
	if src.Duration > 0 {
		at = time.Duration(float64(src.Duration)*st.at) * time.Millisecond
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	tmp := out + ".tmp.jpg"
	defer os.Remove(tmp)
	grab := func(at time.Duration) error {
		cmd := exec.CommandContext(ctx, s.ffmpeg, "-v", "error", "-y",
			"-ss", strconv.FormatFloat(at.Seconds(), 'f', 3, 64), "-i", src.File,
			"-frames:v", "1", "-vf", st.filter(src.HDR), "-q:v", "3", tmp)
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
