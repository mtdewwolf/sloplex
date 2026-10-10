// Package transcode runs ffmpeg to convert media into H.264/AAC for HLS or
// progressive MP4 delivery. See docs/spec/transcode.md.
package transcode

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

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
	// SelectAudio uses the audio stream with ffprobe index AudioIndex
	// instead of the first one. Source's audio fields should describe it.
	SelectAudio bool
	AudioIndex  int
	// Burn renders a subtitle into the video.
	Burn *Burn
	// PartID identifies the source part for callers; ffmpeg ignores it.
	PartID int
	// WebM selects VP9/Opus in WebM instead of H.264/AAC in MP4, for
	// progressive streams only; see "WebM" in docs/spec/transcode.md.
	WebM bool
	// ABR controls lower quality levels of HLS sessions.
	ABR ABR
}

// ABR is the abr request parameter; see "Adaptive quality levels" in
// docs/spec/transcode.md.
type ABR int

const (
	// ABRAuto adds lower levels that fit the CPU budget.
	ABRAuto ABR = iota
	// ABROff adds no lower levels.
	ABROff
	// ABROn adds every candidate level.
	ABROn
)

// Level is a lower quality level of an HLS session.
type Level struct {
	Name          string
	Width, Height int
	// MaxBitrate is the video bitrate cap in kbps.
	MaxBitrate int
	// Bandwidth is the playlist BANDWIDTH in bits per second.
	Bandwidth int
}

// ladder is the candidate quality levels, largest first.
var ladder = []struct {
	name       string
	maxW, maxH int
	kbps       int
}{
	{"1080p", 1920, 1080, 8000},
	{"720p", 1280, 720, 3000},
	{"480p", 854, 480, 1200},
}

// LevelCandidates lists the lower quality levels a plan could offer,
// largest first: those shorter than the main output and with a lower
// bandwidth.
func LevelCandidates(r Request, p Plan) []Level {
	if p.CopyVideo || p.WebM || p.Width <= 0 || p.Height <= 0 {
		return nil
	}
	var out []Level
	for _, l := range ladder {
		w, h := fitBox(r.Source.Width, r.Source.Height, l.maxW, l.maxH)
		bw := l.kbps*1000 + 160_000
		if h >= p.Height || bw >= p.Bandwidth || (len(out) > 0 && out[len(out)-1].Height == h) {
			continue
		}
		out = append(out, Level{Name: l.name, Width: w, Height: h, MaxBitrate: l.kbps, Bandwidth: bw})
	}
	return out
}

// Burn describes a subtitle to burn in; see docs/spec/streams.md.
type Burn struct {
	// File is a text subtitle file (srt, ass, ssa, vtt) rendered with
	// libass. It is copied into the working directory as BurnFile(File).
	File string
	// Index is the ffprobe index of an image subtitle stream to overlay,
	// used when File is empty.
	Index int
}

// BurnFile is the working-directory name a text subtitle is copied to, so
// filter arguments never contain user paths.
func BurnFile(src string) string { return "burn" + strings.ToLower(filepath.Ext(src)) }

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
	// Burn is set when a subtitle is burned in.
	Burn bool
	// ToneMap is set when an HDR source is tone mapped to SDR, and HDR is
	// the source's kind (probe.HDRPQ or probe.HDRHLG).
	ToneMap bool
	HDR     string
	// WebM is set for VP9/Opus output.
	WebM bool
	// Levels are the lower quality levels of an HLS session; the Manager
	// sets them.
	Levels []Level
	// Preset is the libx264 preset for the software pipeline; empty means
	// DefaultPreset. The Manager sets it; see "CPU transcoding".
	Preset string
}

// Decide applies the decision rules.
func Decide(r Request) Plan {
	src := r.Source
	p := Plan{HasAudio: src.AudioCodec != "", WebM: r.WebM}
	videoCodec, audioCodec := "h264", "aac"
	if r.WebM {
		videoCodec, audioCodec = "vp9", "opus"
	}
	p.CopyAudio = p.HasAudio && src.AudioCodec == audioCodec && src.AudioChannels > 0 && src.AudioChannels <= 2

	fits := (r.MaxWidth == 0 || src.Width <= r.MaxWidth) && (r.MaxHeight == 0 || src.Height <= r.MaxHeight)
	withinRate := r.MaxBitrate == 0 || (src.Bitrate > 0 && src.Bitrate <= r.MaxBitrate)
	p.Burn = r.Burn != nil
	p.ToneMap = src.HDR != ""
	p.HDR = src.HDR
	p.CopyVideo = src.VideoCodec == videoCodec && fits && withinRate && !r.NoDirectStream && !p.Burn && !p.ToneMap

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
	// ToneMap is the GPU tone mapper that passed the startup check, or ""
	// for the CPU; see "GPU tone mapping" in docs/spec/transcode.md.
	ToneMap string
}

// GPU tone mappers.
const (
	ToneMapVAAPI  = "vaapi"
	ToneMapQSV    = "qsv"
	ToneMapOpenCL = "opencl"
)

// openCLDevice selects the OpenCL device: GPUs only. Tests point it at a
// CPU implementation.
var openCLDevice = "ocl:,device_type=gpu"

// Pipeline is one way to run a video encode. A zero Pipeline is software.
type Pipeline struct {
	Accel  string
	Device string
	// Full keeps frames on the GPU from decode to encode.
	Full bool
	// ToneMap tone maps HDR on the GPU with this tone mapper; "" uses the
	// CPU.
	ToneMap string
}

func (p Pipeline) String() string {
	var s string
	switch {
	case p.Accel == "":
		s = "software"
	case p.Full:
		s = p.Accel + " (full)"
	default:
		s = p.Accel + " (encode only)"
	}
	if p.ToneMap != "" {
		s += " with " + p.ToneMap + " tone mapping"
	}
	return s
}

// gpuToneMapPipeline returns the pipeline that tone maps a plan on the GPU,
// if the server has a fitting tone mapper; see "GPU tone mapping".
func gpuToneMapPipeline(hw HW, p Plan) (Pipeline, bool) {
	if !p.ToneMap || p.CopyVideo || p.WebM {
		return Pipeline{}, false
	}
	switch {
	case hw.ToneMap == ToneMapVAAPI && hw.Accel == AccelVAAPI, hw.ToneMap == ToneMapQSV && hw.Accel == AccelQSV:
		// HDR10 only, and frames never leave the GPU, so no burn-in.
		if p.HDR != probe.HDRPQ || p.Burn {
			return Pipeline{}, false
		}
		return Pipeline{Accel: hw.Accel, Device: hw.Device, Full: true, ToneMap: hw.ToneMap}, true
	case hw.ToneMap == ToneMapOpenCL && (hw.Accel == AccelNVENC || hw.Accel == ""):
		return Pipeline{Accel: hw.Accel, Device: hw.Device, ToneMap: ToneMapOpenCL}, true
	}
	return Pipeline{}, false
}

// Pipelines lists the pipelines to try for a plan, fastest first. Copied
// video needs no encoder, and VP9 is only encoded in software, so those use
// only the software pipeline.
func Pipelines(hw HW, p Plan) []Pipeline {
	out := basePipelines(hw, p)
	if gpu, ok := gpuToneMapPipeline(hw, p); ok {
		out = append([]Pipeline{gpu}, out...)
	}
	return out
}

// basePipelines lists the pipelines without GPU tone mapping.
func basePipelines(hw HW, p Plan) []Pipeline {
	if p.CopyVideo || p.WebM || hw.Accel == "" {
		return []Pipeline{{}}
	}
	full := Pipeline{Accel: hw.Accel, Device: hw.Device, Full: true}
	encodeOnly := Pipeline{Accel: hw.Accel, Device: hw.Device}
	if p.Burn || p.ToneMap {
		// Burning in and tone mapping need frames in system memory.
		return []Pipeline{encodeOnly, {}}
	}
	if hw.Accel == AccelVideoToolbox {
		return []Pipeline{full, {}}
	}
	return []Pipeline{full, encodeOnly, {}}
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
	args = append(args, "-i", r.Input)

	levels := levelsFor(p, out, pl)
	videoMap := "0:v:0"
	vf, graph := "", "" // the video filters, as -vf or as a filter graph ending in [vout]
	if encode {
		chain := videoFilter(r, p, pl)
		tone := ""
		preW, preH, preScaled := toneMapSize(r, p)
		switch {
		case !p.ToneMap:
		case pl.ToneMap == ToneMapOpenCL:
			tone = openCLToneMapFilter(preW, preH)
		case pl.ToneMap != "":
			// VAAPI and QSV tone map inside the hardware chain.
			preScaled = false
		default:
			tone = toneMapFilter(r.Source.HDR, preW, preH)
		}
		switch {
		case r.Burn != nil && r.Burn.File != "":
			f := "subtitles=filename=" + BurnFile(r.Burn.File)
			if r.Offset > 0 {
				// Input seeking restarts timestamps at 0; render subtitles
				// at source time, then restore.
				off := strconv.FormatFloat(r.Offset, 'f', 3, 64)
				f = "setpts=PTS+" + off + "/TB," + f + ",setpts=PTS-STARTPTS"
			}
			vf = joinFilters(tone, f, chain)
		case r.Burn != nil:
			src, sub := "[0:v:0]", fmt.Sprintf("[0:%d]", r.Burn.Index)
			if tone != "" {
				graph, src = "[0:v:0]"+tone+"[sdr];", "[sdr]"
			}
			if preScaled {
				// The picture is already at the output size; scale the
				// subtitle canvas by the same factor.
				graph += fmt.Sprintf("%sscale=%d:%d[sub];", sub, preW, preH)
				sub = "[sub]"
			}
			graph += joinFilters(src+sub+"overlay=eof_action=pass", chain) + "[vout]"
			videoMap = "[vout]"
		default:
			vf = joinFilters(tone, chain)
		}
	}
	audioMap := "0:a:0?"
	if r.SelectAudio {
		audioMap = fmt.Sprintf("0:%d", r.AudioIndex)
	}
	if len(levels) == 0 {
		switch {
		case graph != "":
			args = append(args, "-filter_complex", graph)
		case vf != "":
			args = append(args, "-vf", vf)
		}
		args = append(args, "-map", videoMap, "-map", audioMap, "-sn", "-dn")
	} else {
		// Split the main picture once it is ready, and scale a copy for
		// each level; every level gets its own audio stream.
		top := "[0:v:0]"
		switch {
		case graph != "":
			top, graph = "[vout]", graph+";"
		case vf != "":
			top, graph = "[vout]", "[0:v:0]"+vf+"[vout];"
		}
		graph += fmt.Sprintf("%ssplit=%d[lv0]", top, len(levels)+1)
		for i := range levels {
			graph += fmt.Sprintf("[lv%d]", i+1)
		}
		maps := []string{"-map", "[lv0]"}
		for i, l := range levels {
			graph += fmt.Sprintf(";[lv%d]scale=%d:%d[lo%d]", i+1, l.Width, l.Height, i+1)
		}
		args = append(args, "-filter_complex", graph)
		for i := 0; i <= len(levels); i++ {
			if i > 0 {
				maps = append(maps, "-map", fmt.Sprintf("[lo%d]", i))
			}
			if p.HasAudio {
				maps = append(maps, "-map", strings.TrimSuffix(audioMap, "?"))
			}
		}
		args = append(append(args, maps...), "-sn", "-dn")
	}

	if !encode {
		args = append(args, "-c:v", "copy")
	} else {
		if p.WebM {
			args = append(args, "-c:v", "libvpx-vp9")
		} else {
			args = append(args, "-c:v", Encoder(pl.Accel))
		}
		if p.ToneMap {
			args = append(args, "-colorspace", "bt709", "-color_primaries", "bt709", "-color_trc", "bt709")
		}
		rc := rateControl(r, p, pl)
		if len(levels) > 0 {
			// A request's bitrate cap is for the main output only.
			for i, a := range rc {
				if a == "-maxrate" || a == "-bufsize" {
					rc[i] = a + ":v:0"
				}
			}
			for i, l := range levels {
				rc = append(rc, fmt.Sprintf("-maxrate:v:%d", i+1), fmt.Sprintf("%dk", l.MaxBitrate),
					fmt.Sprintf("-bufsize:v:%d", i+1), fmt.Sprintf("%dk", 2*l.MaxBitrate))
			}
		}
		args = append(args, rc...)
		if out.HLSDir != "" {
			args = append(args, "-force_key_frames", fmt.Sprintf("expr:gte(t,n_forced*%d)", segmentSeconds))
		}
	}
	if p.HasAudio {
		switch {
		case p.CopyAudio:
			args = append(args, "-c:a", "copy")
		case p.WebM:
			args = append(args, "-c:a", "libopus", "-ac", "2", "-b:a", "128k")
		default:
			args = append(args, "-c:a", "aac", "-ac", "2", "-b:a", "160k")
		}
	}

	switch {
	case out.HLSDir != "" && len(levels) > 0:
		streams := make([]string, 0, len(levels)+1)
		for i := 0; i <= len(levels); i++ {
			name := "base"
			if i > 0 {
				name = levels[i-1].Name
			}
			if p.HasAudio {
				streams = append(streams, fmt.Sprintf("v:%d,a:%d,name:%s", i, i, name))
			} else {
				streams = append(streams, fmt.Sprintf("v:%d,name:%s", i, name))
			}
		}
		return append(args,
			"-f", "hls",
			"-hls_time", strconv.Itoa(segmentSeconds),
			"-hls_list_size", "0",
			"-hls_playlist_type", "event",
			"-var_stream_map", strings.Join(streams, " "),
			"-hls_segment_filename", out.HLSDir+"/%v/%05d.ts",
			out.HLSDir+"/%v/index.m3u8")
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
	case p.WebM:
		// Matroska sources carry per-stream DURATION tags for the whole
		// file; copied into a stream that starts at an offset, browsers
		// would take them as its length.
		return append(args, "-metadata:s", "DURATION=", "-f", "webm", "pipe:1")
	}
	return append(args, "-movflags", "frag_keyframe+empty_moov+default_base_moof", "-f", "mp4", "pipe:1")
}

// levelsFor returns the lower quality levels Args writes: only for HLS on
// the software pipeline, with encoded H.264 video.
func levelsFor(p Plan, out Output, pl Pipeline) []Level {
	if out.HLSDir == "" || pl.Accel != "" || p.CopyVideo || p.WebM {
		return nil
	}
	return p.Levels
}

// qsvDecoders maps source codecs to QSV decoders, which ffmpeg needs named
// explicitly for full-hardware QSV decoding.
var qsvDecoders = map[string]string{
	"h264": "h264_qsv", "hevc": "hevc_qsv", "mpeg2video": "mpeg2_qsv",
	"vp9": "vp9_qsv", "av1": "av1_qsv", "vc1": "vc1_qsv",
}

// joinFilters joins non-empty filter chains with commas.
func joinFilters(parts ...string) string {
	var out []string
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, ",")
}

// toneMapSize is the size tone mapping should scale to first: the planned
// output size when that is smaller than the source. Tone mapping costs per
// pixel, so working at the output size saves most of it.
func toneMapSize(r Request, p Plan) (w, h int, ok bool) {
	src := r.Source
	if !p.ToneMap || p.Width <= 0 || p.Height <= 0 || src.Width <= 0 || src.Height <= 0 {
		return 0, 0, false
	}
	if p.Width >= src.Width && p.Height >= src.Height {
		return 0, 0, false
	}
	return p.Width, p.Height, true
}

// toneMapFilter converts an HDR source (probe.HDRPQ or HDRHLG) to 8-bit
// BT.709 SDR, first scaling to w×h when both are set; see "HDR tone
// mapping" in docs/spec/transcode.md.
func toneMapFilter(hdr string, w, h int) string {
	tin := "smpte2084"
	if hdr == probe.HDRHLG {
		tin = "arib-std-b67"
	}
	pre := ""
	if w > 0 && h > 0 {
		pre = fmt.Sprintf("zscale=w=%d:h=%d:filter=bicubic,", w, h)
	}
	return pre + "zscale=tin=" + tin + ":min=bt2020nc:pin=bt2020:rin=tv:t=linear:npl=100," +
		"format=gbrpf32le,zscale=p=bt709,tonemap=tonemap=hable:desat=0," +
		"zscale=t=bt709:m=bt709:r=tv,format=yuv420p"
}

// openCLToneMapFilter tone maps on an OpenCL GPU, scaling to w×h first
// when that makes the picture smaller; see "GPU tone mapping".
func openCLToneMapFilter(w, h int) string {
	pre := ""
	if w > 0 && h > 0 {
		pre = fmt.Sprintf("scale=%d:%d,", w, h)
	}
	return pre + "format=p010,hwupload," +
		"tonemap_opencl=tonemap=hable:desat=0:t=bt709:m=bt709:p=bt709:r=tv:format=nv12," +
		"hwdownload,format=nv12"
}

// hwInputArgs are the device and decoder options placed before -i.
func hwInputArgs(pl Pipeline, srcCodec string) []string {
	if pl.ToneMap == ToneMapOpenCL {
		args := []string{"-init_hw_device", "opencl=" + openCLDevice, "-filter_hw_device", "ocl"}
		if pl.Accel == AccelNVENC {
			// Decode on the GPU into system memory for the OpenCL upload.
			args = append(args, "-hwaccel", "cuda")
		}
		return args
	}
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
	case pl.ToneMap == ToneMapVAAPI:
		tm := "tonemap_vaapi=format=nv12:t=bt709:m=bt709:p=bt709"
		if scale {
			return fmt.Sprintf("scale_vaapi=w=%d:h=%d,%s", p.Width, p.Height, tm)
		}
		return tm
	case pl.ToneMap == ToneMapQSV:
		if scale {
			return fmt.Sprintf("vpp_qsv=tonemap=1:format=nv12:w=%d:h=%d", p.Width, p.Height)
		}
		return "vpp_qsv=tonemap=1:format=nv12"
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
	if p.WebM {
		// Realtime VP9: constant quality, capped by -b:v when a cap is set.
		out := []string{"-deadline", "realtime", "-cpu-used", "8", "-row-mt", "1", "-crf", "32", "-pix_fmt", "yuv420p"}
		if r.MaxBitrate > 0 {
			return append(append(out, "-b:v", fmt.Sprintf("%dk", r.MaxBitrate)), capArgs()...)
		}
		return append(out, "-b:v", "0")
	}
	switch pl.Accel {
	case "":
		preset := p.Preset
		if preset == "" {
			preset = DefaultPreset
		}
		out := []string{"-preset", preset, "-crf", "21", "-pix_fmt", "yuv420p"}
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
