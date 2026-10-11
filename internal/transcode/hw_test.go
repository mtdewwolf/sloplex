package transcode

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mtdewwolf/sloplex/internal/probe"
)

func TestPipelines(t *testing.T) {
	enc := Plan{Width: 1280, Height: 720}
	for name, tc := range map[string]struct {
		hw   HW
		plan Plan
		want string
	}{
		"software":        {HW{}, enc, "software"},
		"copy ignores hw": {HW{Accel: AccelVAAPI}, Plan{CopyVideo: true}, "software"},
		"vaapi chain":     {HW{Accel: AccelVAAPI, Device: "/d"}, enc, "vaapi (full),vaapi (encode only),software"},
		"nvenc chain":     {HW{Accel: AccelNVENC}, enc, "nvenc (full),nvenc (encode only),software"},
		"videotoolbox":    {HW{Accel: AccelVideoToolbox}, enc, "videotoolbox (full),software"},
	} {
		var got []string
		for _, pl := range Pipelines(tc.hw, tc.plan) {
			got = append(got, pl.String())
		}
		if strings.Join(got, ",") != tc.want {
			t.Errorf("%s: %v, want %s", name, got, tc.want)
		}
	}
}

func TestArgsHW(t *testing.T) {
	src := probe.Info{VideoCodec: "hevc", Width: 3840, Height: 2160, AudioCodec: "aac", AudioChannels: 2}
	box := Request{Input: "in.mkv", Source: src, MaxWidth: 1920, MaxHeight: 1080}
	capped := box
	capped.MaxBitrate = 4000
	same := Request{Input: "in.mkv", Source: src} // no scaling needed
	const dev = "/dev/dri/renderD129"
	for name, tc := range map[string]struct {
		r    Request
		pl   Pipeline
		out  Output
		want []string
		not  []string
	}{
		"vaapi full": {box, Pipeline{Accel: AccelVAAPI, Device: dev, Full: true}, Output{HLSDir: "/s"}, []string{
			"-hwaccel vaapi -hwaccel_device " + dev + " -hwaccel_output_format vaapi -i in.mkv",
			"-vf scale_vaapi=w=1920:h=1080:format=nv12", "-c:v h264_vaapi -qp 23", "-force_key_frames",
		}, []string{"libx264", "-crf"}},
		"vaapi full no scale": {same, Pipeline{Accel: AccelVAAPI, Device: dev, Full: true}, Output{}, []string{"-vf scale_vaapi=format=nv12"}, nil},
		"vaapi encode-only capped": {capped, Pipeline{Accel: AccelVAAPI, Device: dev, Full: false}, Output{}, []string{
			"-vaapi_device " + dev + " -i in.mkv", "-vf format=nv12,hwupload,scale_vaapi=w=1920:h=1080",
			"-b:v 3200k -maxrate 4000k -bufsize 8000k",
		}, []string{"-hwaccel"}},
		"qsv full": {box, Pipeline{Accel: AccelQSV, Device: dev, Full: true}, Output{}, []string{
			"-hwaccel qsv -qsv_device " + dev + " -hwaccel_output_format qsv -c:v hevc_qsv -i in.mkv",
			"scale_qsv=w=1920:h=1080:format=nv12", "-c:v h264_qsv -preset veryfast -forced_idr 1 -global_quality 23",
		}, nil},
		"qsv encode-only": {box, Pipeline{Accel: AccelQSV, Device: dev, Full: false}, Output{}, []string{"-vf scale=1920:1080,format=nv12", "-c:v h264_qsv"}, []string{"-hwaccel"}},
		"nvenc full capped": {capped, Pipeline{Accel: AccelNVENC, Device: "", Full: true}, Output{HLSDir: "/s"}, []string{
			"-hwaccel cuda -hwaccel_output_format cuda", "scale_cuda=w=1920:h=1080:format=nv12",
			"-c:v h264_nvenc -preset p4 -rc vbr -cq 23 -b:v 0 -forced-idr 1 -maxrate 4000k -bufsize 8000k",
		}, nil},
		"nvenc encode-only": {box, Pipeline{Accel: AccelNVENC, Device: "", Full: false}, Output{}, []string{"-vf scale=1920:1080,format=yuv420p", "-c:v h264_nvenc"}, []string{"-hwaccel"}},
		"videotoolbox": {box, Pipeline{Accel: AccelVideoToolbox, Device: "", Full: true}, Output{}, []string{
			"-hwaccel videotoolbox", "-vf scale=1920:1080,format=nv12", "-c:v h264_videotoolbox -realtime 1 -b:v 6000k",
		}, nil},
		"null output": {box, Pipeline{}, Output{Null: true}, []string{"-c:v libx264", "-f null -"}, nil},
	} {
		got := strings.Join(Args(tc.r, Decide(tc.r), tc.out, tc.pl), " ")
		for _, w := range tc.want {
			if !strings.Contains(got, w) {
				t.Errorf("%s: missing %q in\n%s", name, w, got)
			}
		}
		for _, n := range tc.not {
			if strings.Contains(got, n) {
				t.Errorf("%s: unexpected %q in\n%s", name, n, got)
			}
		}
	}
	// Copying video never touches the GPU.
	cp := Request{Input: "in.mp4", Source: probe.Info{VideoCodec: "h264", Width: 1280, Height: 720}}
	if got := strings.Join(Args(cp, Decide(cp), Output{}, Pipeline{Accel: AccelVAAPI, Device: dev, Full: true}), " "); strings.Contains(got, "hwaccel") || !strings.Contains(got, "-c:v copy") {
		t.Errorf("copy with hw pipeline: %s", got)
	}
}

func TestDetect(t *testing.T) {
	ffmpeg, _ := clip(t, "1") // skips without ffmpeg
	ctx := context.Background()
	// The software path of the same builder must work, or detection would
	// reject every accelerator for the wrong reason.
	if err := testEncode(ctx, ffmpeg, "", ""); err != nil {
		t.Fatalf("software test encode failed: %v", err)
	}
	if hw := Detect(ctx, ffmpeg, HwaccelNone, "", "linux", nil); hw != (HW{}) {
		t.Errorf("none = %+v", hw)
	}
	if hw := Detect(ctx, ffmpeg, AccelVAAPI, "/dev/dri/sloplex-missing", "linux", nil); hw != (HW{}) {
		t.Errorf("vaapi on a missing device = %+v", hw)
	}
	hw := Detect(ctx, ffmpeg, HwaccelAuto, "", "linux", nil)
	t.Logf("auto-detected on this machine: %q (%s)", hw.Accel, Encoder(hw.Accel))
	for _, v := range []string{"none", "auto", "vaapi", "qsv", "nvenc", "videotoolbox"} {
		if !ValidHwaccel(v) {
			t.Errorf("ValidHwaccel(%q) = false", v)
		}
	}
	if ValidHwaccel("cuda") || len(candidates(HwaccelAuto, "windows")) != 0 {
		t.Error("unexpected hwaccel acceptance")
	}
}

// brokenVAAPI points at a device that does not exist, so both hardware
// pipelines fail fast and the session must fall back to software.
var brokenVAAPI = HW{Accel: AccelVAAPI, Device: "/dev/dri/sloplex-missing"}

func TestHLSFallsBackToSoftware(t *testing.T) {
	ffmpeg, path := clip(t, "6")
	m, err := New(Options{FFmpeg: ffmpeg, Dir: filepath.Join(t.TempDir(), "tc"), MaxTranscodes: 1, HW: brokenVAAPI})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	s, err := m.StartHLS(Owner{Account: 1}, "x", 9, Request{Input: path, Source: clipInfo, MaxWidth: 320, MaxHeight: 180})
	if err != nil {
		t.Fatal(err)
	}
	seg, err := m.Segment(context.Background(), s, "base", "00000.ts")
	if err != nil {
		t.Fatalf("no segment after fallback: %v", err)
	}
	if b, _ := os.ReadFile(seg); len(b) == 0 || b[0] != 0x47 {
		t.Error("fallback segment is not MPEG-TS")
	}
	<-s.done
	if s.err != nil {
		t.Errorf("session error after fallback: %v", s.err)
	}
	if got := m.List()[0].Pipeline; got.Accel != "" {
		t.Errorf("final pipeline = %s, want software", got)
	}
}

func TestStreamFallsBackToSoftware(t *testing.T) {
	ffmpeg, path := clip(t, "2")
	m, err := New(Options{FFmpeg: ffmpeg, Dir: filepath.Join(t.TempDir(), "tc"), MaxTranscodes: 1, HW: brokenVAAPI})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	var buf bytes.Buffer
	readyCalls := 0
	if err := m.Stream(context.Background(), Owner{Account: 1}, 1, Request{Input: path, Source: clipInfo}, &buf, func(Plan) { readyCalls++ }); err != nil {
		t.Fatalf("stream after fallback: %v", err)
	}
	if readyCalls != 1 || buf.Len() < 1000 || !bytes.Equal(buf.Bytes()[4:8], []byte("ftyp")) {
		t.Errorf("ready called %d times, %d bytes", readyCalls, buf.Len())
	}
}
