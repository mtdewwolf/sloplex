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
	Input string
	// InputFormat forces the input demuxer (e.g. "lavfi"); usually empty.
	InputFormat string
	Source      probe.Info
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
	// HLSDir, when set, writes index.m3u8 and %05d.ts there.
	HLSDir string
	// Null discards the output (used to test encoders).
	Null bool
	// Otherwise fragmented MP4 is written to stdout.
}

// Accelerators.
const (
	AccelVAAPI        = "vaapi"
	AccelQSV          = "qsv"
	AccelNVENC        = "nvenc"
	AccelVideoToolbox = "videotoolbox"
)

var encoders = map[string]string{
	"":                "libx264",
	AccelVAAPI:        "h264_vaapi",
	AccelQSV:          "h264_qsv",
	AccelNVENC:        "h264_nvenc",
	AccelVideoToolbox: "h264_videotoolbox",
}

// Encoder returns the ffmpeg video encoder of an accelerator ("" for
// software).
func Encoder(accel string) string { return encoders[accel] }

// HW is the active hardware acceleration; a zero HW means software.
type HW struct {
	Accel  string
	Device string
}

// Pipeline is one way to run a video encode. A zero Pipeline is software.
type Pipeline struct {
	Accel  string
	Device string
	// Full keeps frames on the GPU from decode to encode.
	Full bool
}

func (p Pipeline) String() string {
	switch {
	case p.Accel == "":
		return "software"
	case p.Full:
		return p.Accel + " (full)"
	default:
		return p.Accel + " (encode only)"
	}
}

// Pipelines lists the pipelines to try for a plan, fastest first. Copied
// video needs no encoder, so only the software pipeline is used.
func Pipelines(hw HW, p Plan) []Pipeline {
	if p.CopyVideo || hw.Accel == "" {
		return []Pipeline{{}}
	}
	full := Pipeline{Accel: hw.Accel, Device: hw.Device, Full: true}
	if hw.Accel == AccelVideoToolbox {
		return []Pipeline{full, {}}
	}
	return []Pipeline{full, {Accel: hw.Accel, Device: hw.Device}, {}}
}

// Args builds the ffmpeg arguments for a request, plan and pipeline.
func Args(r Request, p Plan, out Output, pl Pipeline) []string {
	encode := !p.CopyVideo
	args := []string{"-nostdin", "-hide_banner", "-loglevel", "error"}
	if encode {
		args = append(args, hwInputArgs(pl, r.Source.VideoCodec)...)
	}
	if r.Offset > 0 {
		args = append(args, "-ss", strconv.FormatFloat(r.Offset, 'f', 3, 64))
	}
	if r.InputFormat != "" {
		args = append(args, "-f", r.InputFormat)
	}
	args = append(args, "-i", r.Input, "-map", "0:v:0", "-map", "0:a:0?", "-sn", "-dn")

	if !encode {
		args = append(args, "-c:v", "copy")
	} else {
		if vf := videoFilter(r, p, pl); vf != "" {
			args = append(args, "-vf", vf)
		}
		args = append(args, "-c:v", Encoder(pl.Accel))
		args = append(args, rateControl(r, p, pl)...)
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

	switch {
	case out.HLSDir != "":
		return append(args,
			"-f", "hls",
			"-hls_time", strconv.Itoa(segmentSeconds),
			"-hls_list_size", "0",
			"-hls_playlist_type", "event",
			"-hls_segment_filename", out.HLSDir+"/%05d.ts",
			out.HLSDir+"/index.m3u8")
	case out.Null:
		return append(args, "-f", "null", "-")
	}
	return append(args, "-movflags", "frag_keyframe+empty_moov+default_base_moof", "-f", "mp4", "pipe:1")
}

// qsvDecoders maps source codecs to QSV decoders, which ffmpeg needs named
// explicitly for full-hardware QSV decoding.
var qsvDecoders = map[string]string{
	"h264": "h264_qsv", "hevc": "hevc_qsv", "mpeg2video": "mpeg2_qsv",
	"vp9": "vp9_qsv", "av1": "av1_qsv", "vc1": "vc1_qsv",
}

// hwInputArgs are the device and decoder options placed before -i.
func hwInputArgs(pl Pipeline, srcCodec string) []string {
	switch {
	case pl.Accel == AccelVAAPI && pl.Full:
		return []string{"-hwaccel", "vaapi", "-hwaccel_device", pl.Device, "-hwaccel_output_format", "vaapi"}
	case pl.Accel == AccelVAAPI:
		return []string{"-vaapi_device", pl.Device}
	case pl.Accel == AccelQSV && pl.Full:
		args := []string{"-hwaccel", "qsv", "-qsv_device", pl.Device, "-hwaccel_output_format", "qsv"}
		if dec, ok := qsvDecoders[srcCodec]; ok {
			args = append(args, "-c:v", dec)
		}
		return args
	case pl.Accel == AccelNVENC && pl.Full:
		return []string{"-hwaccel", "cuda", "-hwaccel_output_format", "cuda"}
	case pl.Accel == AccelVideoToolbox:
		return []string{"-hwaccel", "videotoolbox"}
	}
	return nil
}

// videoFilter builds the -vf chain for a pipeline.
func videoFilter(r Request, p Plan, pl Pipeline) string {
	if pl.Accel == "" {
		if r.MaxWidth > 0 || r.MaxHeight > 0 {
			return scaleFilter(r.MaxWidth, r.MaxHeight)
		}
		return ""
	}
	scale := p.Width > 0 && p.Height > 0 && (p.Width != r.Source.Width || p.Height != r.Source.Height)
	hwScale := func(name string) string {
		if scale {
			return fmt.Sprintf("%s=w=%d:h=%d:format=nv12", name, p.Width, p.Height)
		}
		return name + "=format=nv12"
	}
	swScale := func(format string) string {
		if scale {
			return fmt.Sprintf("scale=%d:%d,format=%s", p.Width, p.Height, format)
		}
		return "format=" + format
	}
	switch {
	case pl.Accel == AccelVAAPI && pl.Full:
		return hwScale("scale_vaapi")
	case pl.Accel == AccelVAAPI:
		if scale {
			return fmt.Sprintf("format=nv12,hwupload,scale_vaapi=w=%d:h=%d", p.Width, p.Height)
		}
		return "format=nv12,hwupload"
	case pl.Accel == AccelQSV && pl.Full:
		return hwScale("scale_qsv")
	case pl.Accel == AccelNVENC && pl.Full:
		return hwScale("scale_cuda")
	case pl.Accel == AccelNVENC:
		return swScale("yuv420p")
	default: // qsv encode-only, videotoolbox
		return swScale("nv12")
	}
}

// rateControl returns the encoder's quality and bitrate options.
func rateControl(r Request, p Plan, pl Pipeline) []string {
	capArgs := func() []string {
		return []string{"-maxrate", fmt.Sprintf("%dk", r.MaxBitrate), "-bufsize", fmt.Sprintf("%dk", 2*r.MaxBitrate)}
	}
	target := func() []string {
		return append([]string{"-b:v", fmt.Sprintf("%dk", r.MaxBitrate*8/10)}, capArgs()...)
	}
	switch pl.Accel {
	case "":
		out := []string{"-preset", "veryfast", "-crf", "21", "-pix_fmt", "yuv420p"}
		if r.MaxBitrate > 0 {
			out = append(out, capArgs()...)
		}
		return out
	case AccelVAAPI:
		if r.MaxBitrate > 0 {
			return target()
		}
		return []string{"-qp", "23"}
	case AccelQSV:
		out := []string{"-preset", "veryfast", "-forced_idr", "1"}
		if r.MaxBitrate > 0 {
			return append(out, target()...)
		}
		return append(out, "-global_quality", "23")
	case AccelNVENC:
		out := []string{"-preset", "p4", "-rc", "vbr", "-cq", "23", "-b:v", "0", "-forced-idr", "1"}
		if r.MaxBitrate > 0 {
			out = append(out, capArgs()...)
		}
		return out
	case AccelVideoToolbox:
		kbps := r.MaxBitrate
		if kbps == 0 {
			switch h := p.Height; {
			case h > 0 && h <= 480:
				kbps = 1500
			case h > 0 && h <= 720:
				kbps = 3000
			case h > 0 && h <= 1080:
				kbps = 6000
			default:
				kbps = 15000
			}
		}
		return []string{"-realtime", "1", "-b:v", fmt.Sprintf("%dk", kbps)}
	}
	return nil
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
