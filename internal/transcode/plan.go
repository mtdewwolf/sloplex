// Package transcode runs ffmpeg to convert media into H.264/AAC for HLS or
// progressive MP4 delivery. See docs/spec/transcode.md.
package transcode

import (
	"fmt"
	"strconv"

	"github.com/mtdewwolf/sloplex/internal/probe"
)

// Request describes what to transcode.
type Request struct {
	Input  string
	Source probe.Info
	// Offset is the start position in seconds.
	Offset float64
	// MaxWidth and MaxHeight cap the output size; 0 means the source size.
	MaxWidth, MaxHeight int
	// MaxBitrate caps the video bitrate in kbps; 0 means no cap.
	MaxBitrate int
	// NoDirectStream forces video encoding even when copying would do.
	NoDirectStream bool
}

// Plan is the decision for a request.
type Plan struct {
	CopyVideo bool
	// CopyAudio is meaningful only when HasAudio.
	CopyAudio bool
	HasAudio  bool
	// Width and Height are the expected output size (0 if unknown).
	Width, Height int
	// Bandwidth is an estimate in bits per second for playlists.
	Bandwidth int
}

// Decide applies the decision rules.
func Decide(r Request) Plan {
	src := r.Source
	p := Plan{HasAudio: src.AudioCodec != ""}
	p.CopyAudio = p.HasAudio && src.AudioCodec == "aac" && src.AudioChannels > 0 && src.AudioChannels <= 2

	fits := (r.MaxWidth == 0 || src.Width <= r.MaxWidth) && (r.MaxHeight == 0 || src.Height <= r.MaxHeight)
	withinRate := r.MaxBitrate == 0 || (src.Bitrate > 0 && src.Bitrate <= r.MaxBitrate)
	p.CopyVideo = src.VideoCodec == "h264" && fits && withinRate && !r.NoDirectStream

	p.Width, p.Height = src.Width, src.Height
	if !p.CopyVideo {
		p.Width, p.Height = fitBox(src.Width, src.Height, r.MaxWidth, r.MaxHeight)
	}
	switch {
	case p.CopyVideo && src.Bitrate > 0:
		p.Bandwidth = src.Bitrate * 1000
	case r.MaxBitrate > 0:
		p.Bandwidth = r.MaxBitrate*1000 + 160_000
	default:
		// A rough CRF 21 estimate: ~0.1 bits per pixel at 24 fps.
		p.Bandwidth = max(800_000, p.Width*p.Height*24/10) + 160_000
	}
	return p
}

// fitBox scales w×h down to fit within maxW×maxH, keeping the aspect ratio
// and even dimensions. Zero limits leave that axis unconstrained.
func fitBox(w, h, maxW, maxH int) (int, int) {
	if w <= 0 || h <= 0 {
		return 0, 0
	}
	scale := 1.0
	if maxW > 0 && w > maxW {
		scale = min(scale, float64(maxW)/float64(w))
	}
	if maxH > 0 && h > maxH {
		scale = min(scale, float64(maxH)/float64(h))
	}
	even := func(v float64) int { return max(2, int(v)/2*2) }
	return even(float64(w) * scale), even(float64(h) * scale)
}

// segmentSeconds is the HLS segment length.
const segmentSeconds = 4

// Output selects the delivery format.
type Output struct {
	// HLSDir, when set, writes index.m3u8 and %05d.ts there. Otherwise
	// fragmented MP4 is written to stdout.
	HLSDir string
}

// Args builds the ffmpeg arguments for a request and plan.
func Args(r Request, p Plan, out Output) []string {
	args := []string{"-nostdin", "-hide_banner", "-loglevel", "error"}
	if r.Offset > 0 {
		args = append(args, "-ss", strconv.FormatFloat(r.Offset, 'f', 3, 64))
	}
	args = append(args, "-i", r.Input, "-map", "0:v:0", "-map", "0:a:0?", "-sn", "-dn")

	if p.CopyVideo {
		args = append(args, "-c:v", "copy")
	} else {
		args = append(args, "-c:v", "libx264", "-preset", "veryfast", "-crf", "21", "-pix_fmt", "yuv420p")
		if r.MaxWidth > 0 || r.MaxHeight > 0 {
			args = append(args, "-vf", scaleFilter(r.MaxWidth, r.MaxHeight))
		}
		if r.MaxBitrate > 0 {
			args = append(args, "-maxrate", fmt.Sprintf("%dk", r.MaxBitrate), "-bufsize", fmt.Sprintf("%dk", 2*r.MaxBitrate))
		}
		if out.HLSDir != "" {
			args = append(args, "-force_key_frames", fmt.Sprintf("expr:gte(t,n_forced*%d)", segmentSeconds))
		}
	}
	if p.HasAudio {
		if p.CopyAudio {
			args = append(args, "-c:a", "copy")
		} else {
			args = append(args, "-c:a", "aac", "-ac", "2", "-b:a", "160k")
		}
	}

	if out.HLSDir != "" {
		return append(args,
			"-f", "hls",
			"-hls_time", strconv.Itoa(segmentSeconds),
			"-hls_list_size", "0",
			"-hls_playlist_type", "event",
			"-hls_segment_filename", out.HLSDir+"/%05d.ts",
			out.HLSDir+"/index.m3u8")
	}
	return append(args, "-movflags", "frag_keyframe+empty_moov+default_base_moof", "-f", "mp4", "pipe:1")
}

// scaleFilter fits the video inside the box without upscaling, with even
// dimensions.
func scaleFilter(maxW, maxH int) string {
	w, h := "iw", "ih"
	if maxW > 0 {
		w = fmt.Sprintf("min(iw\\,%d)", maxW)
	}
	if maxH > 0 {
		h = fmt.Sprintf("min(ih\\,%d)", maxH)
	}
	return fmt.Sprintf("scale=w='%s':h='%s':force_original_aspect_ratio=decrease:force_divisible_by=2", w, h)
}
