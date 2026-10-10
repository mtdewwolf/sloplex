// Package subtitles extracts, converts and shifts text subtitles. See
// docs/spec/streams.md.
package subtitles

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/mtdewwolf/sloplex/internal/atomicfile"
)

// Formats.
const (
	SRT = "srt"
	VTT = "vtt"
	ASS = "ass"
)

// ErrUnavailable is returned when ffmpeg is needed but missing.
var ErrUnavailable = errors.New("subtitle conversion needs ffmpeg")

var textCodecs = map[string]bool{
	"subrip": true, "srt": true, "ass": true, "ssa": true, "webvtt": true, "vtt": true,
	"mov_text": true, "text": true,
}

// IsText reports whether a subtitle codec is text-based.
func IsText(codec string) bool { return textCodecs[codec] }

// NativeFormat is the format a text subtitle is served in when no format
// is requested: ASS keeps its styling, everything else becomes SRT.
func NativeFormat(codec string) string {
	switch codec {
	case "ass", "ssa":
		return ASS
	case "webvtt", "vtt":
		return VTT
	}
	return SRT
}

// ValidFormat reports whether f is a supported output format.
func ValidFormat(f string) bool { return f == SRT || f == VTT || f == ASS }

// ContentType returns the content type of a format.
func ContentType(f string) string {
	switch f {
	case VTT:
		return "text/vtt; charset=utf-8"
	case ASS:
		return "text/x-ssa"
	}
	return "application/x-subrip"
}

var muxers = map[string]struct{ codec, muxer string }{
	SRT: {"srt", "srt"},
	VTT: {"webvtt", "webvtt"},
	ASS: {"ass", "ass"},
}

// Service extracts and converts subtitles with ffmpeg, caching results.
type Service struct {
	ffmpeg   string
	cacheDir string

	mu    sync.Mutex
	locks map[string]*sync.Mutex
}

// New returns a Service caching under cacheDir. An empty ffmpeg disables
// extraction and conversion.
func New(ffmpeg, cacheDir string) *Service {
	return &Service{ffmpeg: ffmpeg, cacheDir: cacheDir, locks: map[string]*sync.Mutex{}}
}

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

// Extract returns a cached file holding an embedded text subtitle stream
// in its native format.
func (s *Service) Extract(ctx context.Context, video string, modTime int64, partID, index int, codec string) (string, error) {
	if s.ffmpeg == "" {
		return "", ErrUnavailable
	}
	format := NativeFormat(codec)
	out := filepath.Join(s.cacheDir, fmt.Sprintf("%d-%d-%d.%s", partID, modTime, index, format))
	// -copyts keeps cue times exactly as stored; otherwise ffmpeg shifts them
	// by the file's start time (e.g. AAC priming).
	return out, s.run(ctx, out, "-copyts", "-i", video, "-map", fmt.Sprintf("0:%d", index), "-c:s", muxers[format].codec, "-f", muxers[format].muxer)
}

// Convert returns a file holding src (a text subtitle in srcFormat) in the
// requested format. Same-format requests return src itself.
func (s *Service) Convert(ctx context.Context, src, srcFormat, format string) (string, error) {
	if srcFormat == format || (srcFormat == "ssa" && format == ASS) {
		return src, nil
	}
	if s.ffmpeg == "" {
		return "", ErrUnavailable
	}
	fi, err := os.Stat(src)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(fmt.Appendf(nil, "%s\x00%d", src, fi.ModTime().UnixNano()))
	out := filepath.Join(s.cacheDir, hex.EncodeToString(sum[:12])+"."+format)
	var args []string
	if b, err := os.ReadFile(src); err == nil && !utf8.Valid(b) {
		args = append(args, "-sub_charenc", "CP1252")
	}
	args = append(args, "-i", src, "-map", "0:s:0", "-c:s", muxers[format].codec, "-f", muxers[format].muxer)
	return out, s.run(ctx, out, args...)
}

// run produces out with ffmpeg unless it already exists.
func (s *Service) run(ctx context.Context, out string, args ...string) error {
	defer s.lock(out)()
	if _, err := os.Stat(out); err == nil {
		return nil
	}
	if err := os.MkdirAll(s.cacheDir, 0o755); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	full := append([]string{"-nostdin", "-hide_banner", "-loglevel", "error"}, args...)
	full = append(full, "pipe:1")
	cmd := exec.CommandContext(ctx, s.ffmpeg, full...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("ffmpeg: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return atomicfile.Write(out, stdout.Bytes(), 0o644)
}

var cueRE = regexp.MustCompile(`^((?:\d+:)?\d{2}:\d{2}\.\d{3}) --> ((?:\d+:)?\d{2}:\d{2}\.\d{3})(.*)$`)

// ShiftVTT moves every cue of a WebVTT document earlier by offset and drops
// cues that end at or before zero.
func ShiftVTT(vtt []byte, offset time.Duration) []byte {
	if offset <= 0 {
		return vtt
	}
	text := strings.ReplaceAll(string(vtt), "\r\n", "\n")
	blocks := strings.Split(text, "\n\n")
	var out []string
	for _, b := range blocks {
		lines := strings.Split(b, "\n")
		cue := -1
		for i, l := range lines {
			if cueRE.MatchString(l) {
				cue = i
				break
			}
		}
		if cue < 0 {
			out = append(out, b) // header, NOTE or STYLE block
			continue
		}
		m := cueRE.FindStringSubmatch(lines[cue])
		start, _ := parseVTTTime(m[1])
		end, _ := parseVTTTime(m[2])
		start, end = start-offset, end-offset
		if end <= 0 {
			continue
		}
		lines[cue] = formatVTTTime(max(start, 0)) + " --> " + formatVTTTime(end) + m[3]
		out = append(out, strings.Join(lines, "\n"))
	}
	return []byte(strings.Join(out, "\n\n"))
}

func parseVTTTime(s string) (time.Duration, error) {
	parts := strings.Split(s, ":")
	var h, m int
	var sec float64
	var err error
	switch len(parts) {
	case 3:
		h, _ = strconv.Atoi(parts[0])
		m, _ = strconv.Atoi(parts[1])
		sec, err = strconv.ParseFloat(parts[2], 64)
	case 2:
		m, _ = strconv.Atoi(parts[0])
		sec, err = strconv.ParseFloat(parts[1], 64)
	default:
		return 0, fmt.Errorf("bad time %q", s)
	}
	return time.Duration(h)*time.Hour + time.Duration(m)*time.Minute + time.Duration(sec*float64(time.Second)+0.5), err
}

func formatVTTTime(d time.Duration) string {
	ms := d.Milliseconds()
	return fmt.Sprintf("%02d:%02d:%02d.%03d", ms/3600000, ms/60000%60, ms/1000%60, ms%1000)
}
