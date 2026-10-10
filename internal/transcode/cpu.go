package transcode

import (
	"cmp"
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"slices"
	"strconv"
	"time"

	"github.com/mtdewwolf/sloplex/internal/probe"
)

// Software encoding presets; see "CPU transcoding" in
// docs/spec/transcode.md.

// PresetAuto picks the libx264 preset of each transcode from the measured
// capacity of the machine and the transcodes already running.
const PresetAuto = "auto"

// DefaultPreset is used when the capacity is unknown.
const DefaultPreset = "veryfast"

// Presets lists the libx264 presets the x264Preset setting accepts, fastest
// first.
var Presets = []string{"ultrafast", "superfast", "veryfast", "faster", "fast", "medium"}

// autoPresets are the presets auto mode chooses from, best quality first.
var autoPresets = []string{"faster", "veryfast", "superfast", "ultrafast"}

// ValidPreset reports whether v is an accepted x264Preset value.
func ValidPreset(v string) bool { return v == PresetAuto || slices.Contains(Presets, v) }

// Capacity is the libx264 speed of this machine: frames per second at
// benchW×benchH for each preset auto mode uses, with every core. A nil
// Capacity is unknown.
type Capacity map[string]float64

const benchW, benchH = 1280, 720

// benchFrames is how many frames the benchmark encodes; tests lower it.
var benchFrames = 240

// presetSpeeds are libx264 speeds relative to veryfast. They vary little
// between CPUs, and measuring one preset for longer is steadier than
// measuring each briefly; see "Load model" in docs/spec/transcode.md.
var presetSpeeds = map[string]float64{"faster": 0.7, "veryfast": 1, "superfast": 1.4, "ultrafast": 2.2}

// Benchmark measures libx264 veryfast on a test pattern and derives the
// other presets auto mode uses. It returns nil if the run fails.
func Benchmark(ctx context.Context, ffmpeg string, log *slog.Logger) Capacity {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	fps, err := benchPreset(ctx, ffmpeg, DefaultPreset)
	if err != nil {
		log.Warn("software encoding benchmark failed; using the default preset", "err", err)
		return nil
	}
	out := Capacity{}
	for preset, speed := range presetSpeeds {
		out[preset] = fps * speed
	}
	log.Info("software encoding benchmark", "preset", DefaultPreset, "fps720p", strconv.FormatFloat(fps, 'f', 1, 64))
	return out
}

func benchPreset(ctx context.Context, ffmpeg, preset string) (float64, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, ffmpeg, "-nostdin", "-hide_banner", "-loglevel", "error",
		"-f", "lavfi", "-i", fmt.Sprintf("testsrc2=size=%dx%d:rate=24", benchW, benchH),
		"-frames:v", strconv.Itoa(benchFrames), "-c:v", "libx264", "-preset", preset, "-crf", "21",
		"-pix_fmt", "yuv420p", "-f", "null", "-")
	start := time.Now()
	if out, err := cmd.CombinedOutput(); err != nil {
		return 0, fmt.Errorf("%w: %s", err, out)
	}
	return float64(benchFrames) / time.Since(start).Seconds(), nil
}

// Cost weights of the work around the encoder, relative to libx264
// veryfast encoding the same number of pixels. They were measured with
// ffmpeg 6.1 on x86-64; see "Load model" in docs/spec/transcode.md.
var decodeWeights = map[string]float64{"h264": 0.45, "hevc": 0.7, "vp9": 0.6, "av1": 0.8}

const (
	defaultDecodeWeight = 0.4
	toneMapWeight       = 2.0 // per output pixel, tone mapping at the output size
	burnWeight          = 0.1 // per output pixel
	defaultFrameRate    = 30
	// targetLoad is the share of the machine auto mode plans software
	// encodes to use, leaving room for audio, the server and estimate error.
	targetLoad = 0.75
)

// Load estimates the share of the whole machine a software encode needs to
// keep up with real time at a preset. It is 0 when video is copied or the
// capacity is unknown. VP9 encoding instead reserves the planning budget.
func (c Capacity) Load(src probe.Info, p Plan, preset string) float64 {
	return c.load(src, p, preset, true)
}

// filterLoad estimates CPU decode/filter work for encode-only GPU pipelines.
func (c Capacity) filterLoad(src probe.Info, p Plan) float64 {
	return c.load(src, p, DefaultPreset, false)
}

func (c Capacity) load(src probe.Info, p Plan, preset string, encode bool) float64 {
	if p.CopyVideo {
		return 0
	}
	// VP9 is not calibrated by the x264 benchmark. Reserve the whole
	// planning budget rather than reporting an invented x264 estimate.
	if p.WebM {
		return targetLoad
	}
	ref, enc := c[DefaultPreset], c[preset]
	if ref <= 0 || enc <= 0 {
		return 0
	}
	bench := float64(benchW * benchH)
	out := float64(p.Width * p.Height)
	if out <= 0 {
		out = bench
	}
	in := float64(src.Width * src.Height)
	if in <= 0 {
		in = out
	}
	decode, ok := decodeWeights[src.VideoCodec]
	if !ok {
		decode = defaultDecodeWeight
	}
	extra := 0.0
	if p.ToneMap {
		extra += toneMapWeight
	}
	if p.Burn {
		extra += burnWeight
	}
	fps := src.FrameRate
	if fps <= 0 {
		fps = defaultFrameRate
	}
	// Seconds of the whole machine per frame, times frames per second.
	encoding := 0.0
	if encode {
		encoding = out / (enc * bench)
	}
	return fps * (encoding + (in*decode+out*extra)/(ref*bench))
}

// LevelLoad estimates the encoding load of one lower quality level at a
// preset; decoding and filters are shared with the main output. It is 0
// when the capacity is unknown.
func (c Capacity) LevelLoad(src probe.Info, l Level, preset string) float64 {
	enc := c[cmp.Or(preset, DefaultPreset)]
	if enc <= 0 {
		return 0
	}
	fps := src.FrameRate
	if fps <= 0 {
		fps = defaultFrameRate
	}
	return fps * float64(l.Width*l.Height) / (enc * float64(benchW*benchH))
}

// LevelsLoad is the total load of a plan's lower quality levels.
func (c Capacity) LevelsLoad(src probe.Info, p Plan) float64 {
	total := 0.0
	for _, l := range p.Levels {
		total += c.LevelLoad(src, l, p.Preset)
	}
	return total
}

// ChoosePreset returns the best-quality preset whose load, added to the
// load of the software encodes already running, stays within the target,
// and that load. With unknown capacity it returns DefaultPreset and 0.
func (c Capacity) ChoosePreset(running float64, src probe.Info, p Plan) (string, float64) {
	if p.WebM {
		return "", c.Load(src, p, "")
	}
	if len(c) == 0 || p.CopyVideo {
		return DefaultPreset, 0
	}
	for _, preset := range autoPresets {
		if load := c.Load(src, p, preset); running+load <= targetLoad {
			return preset, load
		}
	}
	last := autoPresets[len(autoPresets)-1]
	return last, c.Load(src, p, last)
}
