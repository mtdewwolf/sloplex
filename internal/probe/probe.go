// Package probe extracts technical media information from video files.
package probe

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Info is the technical description of one media file.
type Info struct {
	Duration      int64  `json:"duration,omitempty"` // milliseconds
	Bitrate       int    `json:"bitrate,omitempty"`  // kbps
	Width         int    `json:"width,omitempty"`
	Height        int    `json:"height,omitempty"`
	VideoCodec    string `json:"videoCodec,omitempty"`
	AudioCodec    string `json:"audioCodec,omitempty"`
	AudioChannels int    `json:"audioChannels,omitempty"`
	Container     string `json:"container,omitempty"`
}

// Prober inspects a media file.
type Prober interface {
	Probe(ctx context.Context, path string) (Info, error)
}

// containers maps file extensions to container names.
var containers = map[string]string{
	".mkv":  "mkv",
	".mp4":  "mp4",
	".m4v":  "mp4",
	".avi":  "avi",
	".mov":  "mov",
	".wmv":  "asf",
	".ts":   "mpegts",
	".m2ts": "mpegts",
	".webm": "webm",
	".mpg":  "mpeg",
	".mpeg": "mpeg",
	".flv":  "flv",
}

// IsVideo reports whether path has a recognised video extension.
func IsVideo(path string) bool {
	_, ok := containers[strings.ToLower(filepath.Ext(path))]
	return ok
}

// ContainerForPath returns the container name implied by the file extension.
func ContainerForPath(path string) string {
	return containers[strings.ToLower(filepath.Ext(path))]
}

// None is a Prober that only reports the container from the file extension.
type None struct{}

// Probe implements Prober.
func (None) Probe(_ context.Context, path string) (Info, error) {
	return Info{Container: ContainerForPath(path)}, nil
}

// FFprobe runs the ffprobe binary.
type FFprobe struct {
	Path    string
	Timeout time.Duration
}

// NewFFprobe returns an FFprobe prober for bin, or the binary on PATH when
// bin is empty.
func NewFFprobe(bin string) (*FFprobe, error) {
	if bin == "" {
		bin = "ffprobe"
	}
	path, err := exec.LookPath(bin)
	if err != nil {
		return nil, err
	}
	return &FFprobe{Path: path, Timeout: 30 * time.Second}, nil
}

// Probe implements Prober.
func (f *FFprobe) Probe(ctx context.Context, path string) (Info, error) {
	if f.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, f.Timeout)
		defer cancel()
	}
	out, err := exec.CommandContext(ctx, f.Path,
		"-v", "error", "-print_format", "json", "-show_format", "-show_streams", "--", path,
	).Output()
	if err != nil {
		return Info{}, fmt.Errorf("ffprobe %s: %w", path, err)
	}
	info, err := parseFFprobe(out)
	if err != nil {
		return Info{}, fmt.Errorf("ffprobe %s: %w", path, err)
	}
	info.Container = ContainerForPath(path)
	return info, nil
}

type ffprobeOutput struct {
	Format struct {
		Duration string `json:"duration"`
		BitRate  string `json:"bit_rate"`
	} `json:"format"`
	Streams []struct {
		CodecType string `json:"codec_type"`
		CodecName string `json:"codec_name"`
		Width     int    `json:"width"`
		Height    int    `json:"height"`
		Channels  int    `json:"channels"`
	} `json:"streams"`
}

func parseFFprobe(b []byte) (Info, error) {
	var out ffprobeOutput
	if err := json.Unmarshal(b, &out); err != nil {
		return Info{}, err
	}
	var info Info
	if d, err := strconv.ParseFloat(out.Format.Duration, 64); err == nil && d > 0 {
		info.Duration = int64(math.Round(d * 1000))
	}
	if br, err := strconv.ParseInt(out.Format.BitRate, 10, 64); err == nil && br > 0 {
		info.Bitrate = int(br / 1000)
	}
	for _, s := range out.Streams {
		switch s.CodecType {
		case "video":
			if info.VideoCodec == "" {
				info.VideoCodec, info.Width, info.Height = s.CodecName, s.Width, s.Height
			}
		case "audio":
			if info.AudioCodec == "" {
				info.AudioCodec, info.AudioChannels = s.CodecName, s.Channels
			}
		}
	}
	return info, nil
}
