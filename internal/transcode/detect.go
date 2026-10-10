package transcode

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/mtdewwolf/sloplex/internal/probe"
)

// probe640x360 describes the lavfi test pattern: video only, not H.264, so
// it is always encoded.
var probe640x360 = probe.Info{VideoCodec: "rawvideo", Width: 640, Height: 360}

// DefaultDevice is the DRM render node used by VAAPI and QSV.
const DefaultDevice = "/dev/dri/renderD128"

// Hwaccel config values.
const (
	HwaccelNone = "none"
	HwaccelAuto = "auto"
)

// ValidHwaccel reports whether v is an accepted hwaccel config value.
func ValidHwaccel(v string) bool {
	switch v {
	case HwaccelNone, HwaccelAuto, AccelVAAPI, AccelQSV, AccelNVENC, AccelVideoToolbox:
		return true
	}
	return false
}

// candidates lists what "auto" tries on each OS, in order.
func candidates(requested, goos string) []string {
	switch requested {
	case HwaccelNone, "":
		return nil
	case HwaccelAuto:
		if goos == "darwin" {
			return []string{AccelVideoToolbox}
		}
		if goos == "linux" {
			return []string{AccelNVENC, AccelQSV, AccelVAAPI}
		}
		return nil
	}
	return []string{requested}
}

// Detect picks the hardware accelerator to use by test-encoding a short
// pattern with each candidate. It returns a zero HW (software) when nothing
// works; see "Hardware acceleration" in docs/spec/transcode.md.
func Detect(ctx context.Context, ffmpeg, requested, device, goos string, log *slog.Logger) HW {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	if device == "" {
		device = DefaultDevice
	}
	tried := candidates(requested, goos)
	for _, accel := range tried {
		if err := testEncode(ctx, ffmpeg, accel, device); err != nil {
			log.Debug("hardware encoder unavailable", "hwaccel", accel, "err", err)
			continue
		}
		hw := HW{Accel: accel, Device: device}
		log.Info("hardware transcoding enabled", "hwaccel", accel, "encoder", Encoder(accel), "device", device)
		return hw
	}
	if len(tried) > 0 {
		log.Warn("no working hardware encoder; transcoding in software", "requested", requested, "tried", tried)
	}
	return HW{}
}

// testEncode encodes a tenth of a second of test pattern with the
// encode-only pipeline of an accelerator, using the same argument builder
// as real transcodes.
func testEncode(ctx context.Context, ffmpeg, accel, device string) error {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	r := Request{Input: "testsrc=size=640x360:rate=10:duration=0.5", InputFormat: "lavfi",
		Source: probe640x360, MaxWidth: 320, MaxHeight: 180}
	p := Decide(r)
	pl := Pipeline{Accel: accel, Device: device, Full: accel == AccelVideoToolbox}
	cmd := exec.CommandContext(ctx, ffmpeg, Args(r, p, Output{Null: true}, pl)...)
	var stderr bytes.Buffer
	cmd.Stderr = &limitedWriter{w: &stderr, n: 4 << 10}
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%w: %s", err, bytes.TrimSpace(stderr.Bytes()))
	}
	return nil
}

// gpuToneMapping config values.
const (
	GPUToneMapAuto   = "auto"
	GPUToneMapOpenCL = "opencl"
	GPUToneMapOff    = "off"
)

// ValidGPUToneMapping reports whether v is an accepted gpuToneMapping value.
func ValidGPUToneMapping(v string) bool {
	return v == GPUToneMapAuto || v == GPUToneMapOpenCL || v == GPUToneMapOff
}

// toneMapCandidates lists the GPU tone mappers to check for an accelerator
// and a gpuToneMapping value, in order.
func toneMapCandidates(accel, mode string) []string {
	switch {
	case mode == GPUToneMapOff:
		return nil
	case accel == AccelVAAPI:
		return []string{ToneMapVAAPI}
	case accel == AccelQSV:
		return []string{ToneMapQSV}
	case mode == GPUToneMapOpenCL && (accel == AccelNVENC || accel == ""):
		return []string{ToneMapOpenCL}
	}
	return nil
}

// Startup check thresholds: average luma of the 100-nit and 1000-nit
// halves. Naive conversion gives about 129 and 192.
const (
	checkMidMin, checkMidMax = 60, 230
	checkHighMin             = 220
)

// checkArgs builds the ffmpeg arguments of the startup check for a tone
// mapper; tests replace it.
var checkArgs = func(r Request, p Plan, hw HW) ([]string, error) {
	pl, ok := gpuToneMapPipeline(hw, p)
	if !ok {
		return nil, fmt.Errorf("no %s pipeline for hwaccel %q", hw.ToneMap, hw.Accel)
	}
	return Args(r, p, Output{}, pl), nil
}

// DetectToneMap returns the first GPU tone mapper for hw and a
// gpuToneMapping value that turns a synthetic HDR10 clip into a sensible
// SDR picture, or "" for the CPU; see "GPU tone mapping" in
// docs/spec/transcode.md.
func DetectToneMap(ctx context.Context, ffmpeg string, hw HW, mode string, log *slog.Logger) string {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	tried := toneMapCandidates(hw.Accel, mode)
	if len(tried) == 0 {
		return ""
	}
	dir, err := os.MkdirTemp("", "sloplex-tonemap-")
	if err != nil {
		log.Warn("GPU tone mapping check skipped", "err", err)
		return ""
	}
	defer os.RemoveAll(dir)
	clip := filepath.Join(dir, "hdr10.mkv")
	if err := writeCheckClip(ctx, ffmpeg, clip); err != nil {
		log.Info("GPU tone mapping check skipped; tone mapping on the CPU", "err", err)
		return ""
	}
	for _, tm := range tried {
		mid, high, err := checkToneMap(ctx, ffmpeg, HW{Accel: hw.Accel, Device: hw.Device, ToneMap: tm}, clip, dir)
		switch {
		case err != nil:
			log.Debug("GPU tone mapper unavailable", "toneMap", tm, "err", err)
		case mid < checkMidMin || mid > checkMidMax || high < checkHighMin:
			log.Warn("GPU tone mapper gave a wrong picture; not using it", "toneMap", tm, "diffuse", mid, "highlight", high)
		default:
			log.Info("GPU tone mapping enabled", "toneMap", tm, "diffuse", mid, "highlight", high)
			return tm
		}
	}
	log.Info("no working GPU tone mapper; tone mapping on the CPU", "tried", tried)
	return ""
}

// writeCheckClip encodes the startup check's HDR10 clip: 10-bit PQ BT.2020
// with mastering display and light level metadata, about 100 nits on the
// left and 1000 nits on the right.
func writeCheckClip(ctx context.Context, ffmpeg, path string) error {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, ffmpeg, "-nostdin", "-v", "error", "-y", "-f", "lavfi", "-i",
		"color=c=black:s=640x360:d=2:r=10,format=yuv420p10le,geq=lum='if(lt(X,320),509,723)':cb=512:cr=512",
		"-c:v", "libx265", "-pix_fmt", "yuv420p10le",
		"-x265-params", "colorprim=bt2020:transfer=smpte2084:colormatrix=bt2020nc:log-level=error:"+
			"master-display=G(13250,34500)B(7500,3000)R(34000,16000)WP(15635,16450)L(10000000,1):max-cll=1000,400",
		"-color_primaries", "bt2020", "-color_trc", "smpte2084", "-colorspace", "bt2020nc", path)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%w: %s", err, bytes.TrimSpace(out))
	}
	return nil
}

// checkToneMap transcodes the check clip through hw's tone-mapping pipeline
// at 320×180 and measures the average luma of both halves.
func checkToneMap(ctx context.Context, ffmpeg string, hw HW, clip, dir string) (mid, high int, err error) {
	r := Request{Input: clip, MaxWidth: 320, MaxHeight: 180,
		Source: probe.Info{VideoCodec: "hevc", Width: 640, Height: 360, HDR: probe.HDRPQ}}
	args, err := checkArgs(r, Decide(r), hw)
	if err != nil {
		return 0, 0, err
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	out := filepath.Join(dir, hw.ToneMap+".mp4")
	args[len(args)-1] = out // a file instead of pipe:1
	cmd := exec.CommandContext(ctx, ffmpeg, append([]string{"-y"}, args...)...)
	var stderr bytes.Buffer
	cmd.Stderr = &limitedWriter{w: &stderr, n: 4 << 10}
	if err := cmd.Run(); err != nil {
		return 0, 0, fmt.Errorf("%w: %s", err, bytes.TrimSpace(stderr.Bytes()))
	}
	if mid, err = averageLuma(ctx, ffmpeg, out, "120:120:20:30"); err != nil {
		return 0, 0, err
	}
	high, err = averageLuma(ctx, ffmpeg, out, "120:120:180:30")
	return mid, high, err
}

// averageLuma decodes one frame of file at 0.5 s and averages the luma of
// a crop.
func averageLuma(ctx context.Context, ffmpeg, file, crop string) (int, error) {
	out, err := exec.CommandContext(ctx, ffmpeg, "-nostdin", "-v", "error", "-ss", "0.5", "-i", file, "-frames:v", "1",
		"-vf", "crop="+crop+",format=gray", "-f", "rawvideo", "pipe:1").Output()
	if err != nil || len(out) == 0 {
		return 0, fmt.Errorf("measuring %s: %v (%d bytes)", file, err, len(out))
	}
	var sum int
	for _, v := range out {
		sum += int(v)
	}
	return (sum + len(out)/2) / len(out), nil
}
