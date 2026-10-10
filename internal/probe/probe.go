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
	// Streams lists every stream in file order (see docs/spec/streams.md).
	Streams []Stream `json:"streams,omitempty"`
	// HDR is "pq" or "hlg" when the first video stream is HDR.
	HDR string `json:"hdr,omitempty"`
	// FrameRate is the first video stream's average frame rate, or 0 if
	// unknown.
	FrameRate float64 `json:"frameRate,omitempty"`
	// ProbeVersion is Version when the file was probed by ffprobe, so
	// libraries probed by older versions are re-probed once.
	ProbeVersion int `json:"probeVersion,omitempty"`
}

// Version is the current probe format version: 2 added streams, 3 added
// color information, 4 added the frame rate.
const Version = 4

// HDR transfer kinds.
const (
	HDRPQ  = "pq"
	HDRHLG = "hlg"
)

// HDRKind maps an ffprobe color_transfer to an HDR kind, or "".
func HDRKind(transfer string) string {
	switch transfer {
	case "smpte2084":
		return HDRPQ
	case "arib-std-b67":
		return HDRHLG
	}
	return ""
}

// Stream types.
const (
	StreamVideo    = "video"
	StreamAudio    = "audio"
	StreamSubtitle = "subtitle"
)

// Stream is one stream of a media file.
type Stream struct {
	// Index is the ffprobe stream index; -1 for sidecar files.
	Index           int    `json:"index"`
	Type            string `json:"type"`
	Codec           string `json:"codec"`
	Language        string `json:"language,omitempty"`
	Title           string `json:"title,omitempty"`
	Channels        int    `json:"channels,omitempty"`
	ChannelLayout   string `json:"channelLayout,omitempty"`
	Width           int    `json:"width,omitempty"`
	Height          int    `json:"height,omitempty"`
	ColorTransfer   string `json:"colorTransfer,omitempty"`
	ColorPrimaries  string `json:"colorPrimaries,omitempty"`
	Default         bool   `json:"default,omitempty"`
	Forced          bool   `json:"forced,omitempty"`
	HearingImpaired bool   `json:"hearingImpaired,omitempty"`
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
	info.ProbeVersion = Version
	return info, nil
}

type ffprobeOutput struct {
	Format struct {
		Duration string `json:"duration"`
		BitRate  string `json:"bit_rate"`
	} `json:"format"`
	Streams []struct {
		Index         int               `json:"index"`
		CodecType     string            `json:"codec_type"`
		CodecName     string            `json:"codec_name"`
		Width         int               `json:"width"`
		Height        int               `json:"height"`
		Channels      int               `json:"channels"`
		ChannelLayout string            `json:"channel_layout"`
		ColorTransfer string            `json:"color_transfer"`
		ColorPrims    string            `json:"color_primaries"`
		AvgFrameRate  string            `json:"avg_frame_rate"`
		RFrameRate    string            `json:"r_frame_rate"`
		Tags          map[string]string `json:"tags"`
		Disposition   map[string]int    `json:"disposition"`
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
		if s.CodecType != StreamVideo && s.CodecType != StreamAudio && s.CodecType != StreamSubtitle {
			continue
		}
		if s.CodecType == StreamVideo && s.Disposition["attached_pic"] == 1 {
			continue // embedded cover art, not a playable video stream
		}
		st := Stream{Index: s.Index, Type: s.CodecType, Codec: s.CodecName,
			Language: s.Tags["language"], Title: s.Tags["title"],
			Default: s.Disposition["default"] == 1, Forced: s.Disposition["forced"] == 1,
			HearingImpaired: s.Disposition["hearing_impaired"] == 1}
		if st.Language == "und" {
			st.Language = ""
		}
		switch s.CodecType {
		case StreamVideo:
			st.Width, st.Height = s.Width, s.Height
			st.ColorTransfer, st.ColorPrimaries = s.ColorTransfer, s.ColorPrims
			if info.VideoCodec == "" {
				info.VideoCodec, info.Width, info.Height = s.CodecName, s.Width, s.Height
				info.HDR = HDRKind(s.ColorTransfer)
				info.FrameRate = parseRate(s.AvgFrameRate)
				if info.FrameRate == 0 {
					info.FrameRate = parseRate(s.RFrameRate)
				}
			}
		case StreamAudio:
			st.Channels, st.ChannelLayout = s.Channels, s.ChannelLayout
			if info.AudioCodec == "" {
				info.AudioCodec, info.AudioChannels = s.CodecName, s.Channels
			}
		}
		info.Streams = append(info.Streams, st)
	}
	return info, nil
}

// parseRate parses an ffprobe rational such as "24000/1001", rounded to
// three decimals. Unknown ("0/0") or implausible rates give 0.
func parseRate(v string) float64 {
	num, den, ok := strings.Cut(v, "/")
	n, err1 := strconv.ParseFloat(num, 64)
	d := 1.0
	var err2 error
	if ok {
		d, err2 = strconv.ParseFloat(den, 64)
	}
	if err1 != nil || err2 != nil || d <= 0 || n <= 0 || n/d > 1000 {
		return 0
	}
	return math.Round(n/d*1000) / 1000
}
