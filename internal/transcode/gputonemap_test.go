package transcode

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mtdewwolf/sloplex/internal/probe"
)

func TestGPUToneMapPipelines(t *testing.T) {
	const dev = "/dev/dri/renderD128"
	pq := Decide(Request{Source: probe.Info{VideoCodec: "hevc", Width: 3840, Height: 2160, HDR: probe.HDRPQ}})
	hlg := Decide(Request{Source: probe.Info{VideoCodec: "hevc", Width: 3840, Height: 2160, HDR: probe.HDRHLG}})
	burn := Decide(Request{Source: probe.Info{VideoCodec: "hevc", Width: 3840, Height: 2160, HDR: probe.HDRPQ}, Burn: &Burn{Index: 3}})
	sdr := Decide(Request{Source: probe.Info{VideoCodec: "hevc", Width: 1920, Height: 1080}})
	names := func(pls []Pipeline) string {
		var out []string
		for _, pl := range pls {
			out = append(out, pl.String())
		}
		return strings.Join(out, " | ")
	}
	for _, c := range []struct {
		name string
		hw   HW
		p    Plan
		want string
	}{
		{"vaapi PQ", HW{AccelVAAPI, dev, ToneMapVAAPI}, pq,
			"vaapi (full) with vaapi tone mapping | vaapi (encode only) | software"},
		{"vaapi HLG", HW{AccelVAAPI, dev, ToneMapVAAPI}, hlg, "vaapi (encode only) | software"},
		{"vaapi PQ burn-in", HW{AccelVAAPI, dev, ToneMapVAAPI}, burn, "vaapi (encode only) | software"},
		{"qsv PQ", HW{AccelQSV, dev, ToneMapQSV}, pq, "qsv (full) with qsv tone mapping | qsv (encode only) | software"},
		{"nvenc HLG burn-in", HW{AccelNVENC, "", ToneMapOpenCL}, Decide(Request{Source: hlg.srcForTest(), Burn: &Burn{Index: 3}}),
			"nvenc (encode only) with opencl tone mapping | nvenc (encode only) | software"},
		{"software + OpenCL", HW{"", "", ToneMapOpenCL}, pq, "software with opencl tone mapping | software"},
		{"SDR source", HW{AccelVAAPI, dev, ToneMapVAAPI}, sdr, "vaapi (full) | vaapi (encode only) | software"},
		{"no GPU tone mapper", HW{AccelVAAPI, dev, ""}, pq, "vaapi (encode only) | software"},
		{"mismatched tone mapper", HW{AccelVAAPI, dev, ToneMapOpenCL}, pq, "vaapi (encode only) | software"},
	} {
		if got := names(Pipelines(c.hw, c.p)); got != c.want {
			t.Errorf("%s: %s\n  want %s", c.name, got, c.want)
		}
	}
}

// srcForTest rebuilds a source from a plan, for building related plans.
func (p Plan) srcForTest() probe.Info {
	return probe.Info{VideoCodec: "hevc", Width: p.Width, Height: p.Height, HDR: p.HDR}
}

func TestArgsGPUToneMap(t *testing.T) {
	const dev = "/dev/dri/renderD128"
	src := probe.Info{VideoCodec: "hevc", Width: 3840, Height: 2160, HDR: probe.HDRPQ}
	down := Request{Input: "in.mkv", Source: src, MaxWidth: 1920, MaxHeight: 1080}
	same := Request{Input: "in.mkv", Source: src}
	for name, c := range map[string]struct {
		r    Request
		pl   Pipeline
		want []string
		not  []string
	}{
		"vaapi": {down, Pipeline{Accel: AccelVAAPI, Device: dev, Full: true, ToneMap: ToneMapVAAPI}, []string{
			"-hwaccel vaapi -hwaccel_device " + dev + " -hwaccel_output_format vaapi -i in.mkv",
			"-vf scale_vaapi=w=1920:h=1080,tonemap_vaapi=format=nv12:t=bt709:m=bt709:p=bt709", "-c:v h264_vaapi",
			"-colorspace bt709 -color_primaries bt709 -color_trc bt709",
		}, []string{"zscale", "tonemap=hable"}},
		"vaapi same size": {same, Pipeline{Accel: AccelVAAPI, Device: dev, Full: true, ToneMap: ToneMapVAAPI}, []string{
			"-vf tonemap_vaapi=format=nv12:t=bt709:m=bt709:p=bt709 ",
		}, []string{"scale_vaapi"}},
		"qsv": {down, Pipeline{Accel: AccelQSV, Device: dev, Full: true, ToneMap: ToneMapQSV}, []string{
			"-hwaccel qsv -qsv_device " + dev + " -hwaccel_output_format qsv -c:v hevc_qsv -i in.mkv",
			"-vf vpp_qsv=tonemap=1:format=nv12:w=1920:h=1080", "-c:v h264_qsv",
		}, []string{"zscale"}},
		"opencl nvenc": {down, Pipeline{Accel: AccelNVENC, ToneMap: ToneMapOpenCL}, []string{
			"-init_hw_device opencl=ocl:,device_type=gpu -filter_hw_device ocl -hwaccel cuda -i in.mkv",
			"-vf scale=1920:1080,format=p010,hwupload,tonemap_opencl=tonemap=hable:desat=0:t=bt709:m=bt709:p=bt709:r=tv:format=nv12,hwdownload,format=nv12",
			"-c:v h264_nvenc",
		}, []string{"zscale", "hwaccel_output_format"}},
		"opencl software": {same, Pipeline{ToneMap: ToneMapOpenCL}, []string{
			"-init_hw_device opencl=ocl:,device_type=gpu -filter_hw_device ocl -i in.mkv",
			"-vf format=p010,hwupload,tonemap_opencl=", "-c:v libx264",
		}, []string{"-hwaccel", "zscale"}},
	} {
		got := strings.Join(Args(c.r, Decide(c.r), Output{}, c.pl), " ")
		for _, w := range c.want {
			if !strings.Contains(got, w) {
				t.Errorf("%s: missing %q in\n%s", name, w, got)
			}
		}
		for _, n := range c.not {
			if strings.Contains(got, n) {
				t.Errorf("%s: unexpected %q in\n%s", name, n, got)
			}
		}
	}

	// OpenCL brings frames back, so an image subtitle burns in after tone
	// mapping, scaled to the output size like on the CPU.
	b := down
	b.Burn = &Burn{Index: 3}
	got := strings.Join(Args(b, Decide(b), Output{}, Pipeline{Accel: AccelNVENC, ToneMap: ToneMapOpenCL}), " ")
	if !strings.Contains(got, "[0:v:0]scale=1920:1080,format=p010,hwupload,tonemap_opencl=") ||
		!strings.Contains(got, "[0:3]scale=1920:1080[sub];[sdr][sub]overlay=eof_action=pass") {
		t.Errorf("opencl burn-in:\n%s", got)
	}
}

func TestToneMapCandidates(t *testing.T) {
	for _, c := range []struct {
		accel, mode, want string
	}{
		{AccelVAAPI, GPUToneMapAuto, "vaapi"},
		{AccelQSV, GPUToneMapAuto, "qsv"},
		{AccelNVENC, GPUToneMapAuto, ""},
		{"", GPUToneMapAuto, ""},
		{AccelNVENC, GPUToneMapOpenCL, "opencl"},
		{"", GPUToneMapOpenCL, "opencl"},
		{AccelVAAPI, GPUToneMapOpenCL, "vaapi"},
		{AccelVideoToolbox, GPUToneMapOpenCL, ""},
		{AccelVAAPI, GPUToneMapOff, ""},
	} {
		if got := strings.Join(toneMapCandidates(c.accel, c.mode), ","); got != c.want {
			t.Errorf("toneMapCandidates(%q, %q) = %q, want %q", c.accel, c.mode, got, c.want)
		}
	}
}

// TestDetectToneMap runs the startup check with the CPU's filters standing
// in for a GPU tone mapper: proper tone mapping passes, while naive
// conversion and a failing pipeline are ruled out.
func TestDetectToneMap(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not installed")
	}
	if err := writeCheckClip(context.Background(), ffmpeg, filepath.Join(t.TempDir(), "c.mkv")); err != nil {
		t.Skipf("cannot write the check clip (libx265 missing?): %v", err)
	}
	old := checkArgs
	defer func() { checkArgs = old }()
	vaapi := HW{Accel: AccelVAAPI, Device: "/dev/dri/renderD128"}

	// Proper tone mapping, on the CPU.
	checkArgs = func(r Request, p Plan, hw HW) ([]string, error) { return Args(r, p, Output{}, Pipeline{}), nil }
	if got := DetectToneMap(context.Background(), ffmpeg, vaapi, GPUToneMapAuto, nil); got != ToneMapVAAPI {
		t.Errorf("a tone mapper that works was rejected: %q", got)
	}
	if got := DetectToneMap(context.Background(), ffmpeg, vaapi, GPUToneMapOff, nil); got != "" {
		t.Errorf("gpuToneMapping off: %q", got)
	}

	// Naive conversion: PQ squeezed into BT.709 without tone mapping.
	checkArgs = func(r Request, p Plan, hw HW) ([]string, error) {
		p.ToneMap = false
		return Args(r, p, Output{}, Pipeline{}), nil
	}
	if got := DetectToneMap(context.Background(), ffmpeg, vaapi, GPUToneMapAuto, nil); got != "" {
		t.Errorf("naive conversion passed the check")
	}
	dir := t.TempDir()
	clip := filepath.Join(dir, "c.mkv")
	if err := writeCheckClip(context.Background(), ffmpeg, clip); err != nil {
		t.Fatal(err)
	}
	if mid, high, err := checkToneMap(context.Background(), ffmpeg, HW{ToneMap: "naive"}, clip, dir); err != nil ||
		mid < 119 || mid > 139 || high < 182 || high > 202 {
		t.Errorf("naive conversion measured %d and %d (%v); the spec says about 129 and 192", mid, high, err)
	}

	// A tone mapper whose pipeline fails.
	checkArgs = func(r Request, p Plan, hw HW) ([]string, error) {
		args := Args(r, p, Output{}, Pipeline{})
		for i, a := range args {
			if a == "-vf" {
				args[i+1] = "nosuchfilter"
			}
		}
		return args, nil
	}
	if got := DetectToneMap(context.Background(), ffmpeg, vaapi, GPUToneMapAuto, nil); got != "" {
		t.Errorf("a failing tone mapper passed: %q", got)
	}

	// Without the hook, a GPU that is not there fails.
	checkArgs = old
	if got := DetectToneMap(context.Background(), ffmpeg, HW{Accel: AccelVAAPI, Device: "/nonexistent"}, GPUToneMapAuto, nil); got != "" {
		t.Errorf("VAAPI without a device: %q", got)
	}
}

func TestGPUToneMapLoad(t *testing.T) {
	src := probe.Info{VideoCodec: "hevc", Width: 3840, Height: 2160, HDR: probe.HDRPQ, FrameRate: 24}
	c := Capacity{"veryfast": 240, "faster": 168, "superfast": 336, "ultrafast": 528}
	load := func(hw HW) float64 {
		m, _ := New(Options{FFmpeg: "ffmpeg", Dir: t.TempDir(), MaxTranscodes: 1, HW: hw})
		defer m.Close()
		m.SetCapacity(c)
		m.mu.Lock()
		defer m.mu.Unlock()
		p := Decide(Request{Source: src, MaxWidth: 1920, MaxHeight: 1080})
		return m.choosePresetLocked(Request{Source: src}, &p)
	}
	cpu, gpu := load(HW{}), load(HW{ToneMap: ToneMapOpenCL})
	if gpu <= 0 || gpu >= cpu {
		t.Errorf("load with OpenCL tone mapping %v, on the CPU %v; want lower but positive", gpu, cpu)
	}
}
