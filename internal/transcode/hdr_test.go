package transcode

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mtdewwolf/sloplex/internal/probe"
	"github.com/mtdewwolf/sloplex/internal/testutil"
)

func TestArgsHDR(t *testing.T) {
	hdr := probe.Info{VideoCodec: "h264", Width: 1280, Height: 720, HDR: probe.HDRPQ}
	r := Request{Input: "in.mkv", Source: hdr}
	p := Decide(r)
	if p.CopyVideo || !p.ToneMap {
		t.Fatalf("HDR plan = %+v, want encode with tone mapping", p)
	}
	got := strings.Join(Args(r, p, Output{}, Pipeline{}), " ")
	for _, want := range []string{
		"-vf zscale=tin=smpte2084:min=bt2020nc:pin=bt2020", "tonemap=tonemap=hable:desat=0", "zscale=t=bt709:m=bt709:r=tv,format=yuv420p",
		"-colorspace bt709 -color_primaries bt709 -color_trc bt709",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in\n%s", want, got)
		}
	}

	hlg := Request{Input: "in.mkv", Source: probe.Info{VideoCodec: "hevc", Width: 1920, Height: 1080, HDR: probe.HDRHLG},
		MaxWidth: 1280, MaxHeight: 720, Burn: &Burn{File: "/s/x.srt"}}
	got = strings.Join(Args(hlg, Decide(hlg), Output{}, Pipeline{}), " ")
	if !strings.Contains(got, "-vf zscale=w=1280:h=720:filter=bicubic,zscale=tin=arib-std-b67") ||
		!strings.Contains(got, "format=yuv420p,subtitles=filename=burn.srt,scale=") {
		t.Errorf("HLG + text burn: downscale and tone map must come first, then subtitles:\n%s", got)
	}
	img := Request{Input: "in.mkv", Source: hdr, Burn: &Burn{Index: 3}}
	got = strings.Join(Args(img, Decide(img), Output{}, Pipeline{}), " ")
	if !strings.Contains(got, "-filter_complex [0:v:0]zscale=") || !strings.Contains(got, "[sdr];[sdr][0:3]overlay=eof_action=pass[vout]") {
		t.Errorf("HDR + image burn graph:\n%s", got)
	}
	if strings.Contains(got, "zscale=w=") {
		t.Errorf("tone mapping without downscaling must not resize:\n%s", got)
	}
	small := Request{Input: "in.mkv", Source: hdr, MaxWidth: 640, MaxHeight: 640, Burn: &Burn{Index: 3}}
	got = strings.Join(Args(small, Decide(small), Output{}, Pipeline{}), " ")
	if !strings.Contains(got, "[0:v:0]zscale=w=640:h=360:filter=bicubic,zscale=tin=smpte2084") ||
		!strings.Contains(got, "[sdr];[0:3]scale=640:360[sub];[sdr][sub]overlay=eof_action=pass") {
		t.Errorf("downscaled HDR + image burn must scale the subtitle canvas too:\n%s", got)
	}
	if pls := Pipelines(HW{Accel: AccelNVENC}, p); len(pls) != 2 || pls[0].Full {
		t.Errorf("HDR pipelines = %v, want encode-only then software", pls)
	}
	sdr := Request{Input: "in.mkv", Source: probe.Info{VideoCodec: "hevc", Width: 1280, Height: 720}}
	if got := strings.Join(Args(sdr, Decide(sdr), Output{}, Pipeline{}), " "); strings.Contains(got, "tonemap") || strings.Contains(got, "-color_trc") {
		t.Errorf("SDR source got tone mapping:\n%s", got)
	}
}

// TestToneMapping transcodes real HDR10 and HLG clips and checks that the
// result is BT.709 SDR with sensible brightness: highlights near white and
// diffuse white in the upper mid-tones. Naive conversion fails both (it
// gives about 206 and 132 for the PQ clip).
func TestToneMapping(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not installed")
	}
	prober, err := probe.NewFFprobe("")
	if err != nil {
		t.Skip("ffprobe not installed")
	}
	dir := t.TempDir()
	m, _ := New(Options{FFmpeg: ffmpeg, Dir: filepath.Join(dir, "tc"), MaxTranscodes: 1})
	defer m.Close()

	for _, tc := range []struct {
		name, transfer, kind string
		left, right          int // 10-bit limited-range luma codes
		midMin, midMax       int
		// maxW scales the output down, which tone maps at the smaller size.
		maxW int
	}{
		{"HDR10", "smpte2084", probe.HDRPQ, 509, 723, 145, 215, 0}, // ~100 and ~1000 nits
		{"HLG", "arib-std-b67", probe.HDRHLG, 500, 800, 100, 200, 0},
		{"HDR10-downscaled", "smpte2084", probe.HDRPQ, 509, 723, 145, 215, 320},
	} {
		src := filepath.Join(dir, tc.name+".mkv")
		testutil.WriteHDRClip(t, ffmpeg, src, tc.transfer, tc.left, tc.right)
		info, err := prober.Probe(context.Background(), src)
		if err != nil || info.HDR != tc.kind {
			t.Fatalf("%s: probe HDR = %q, %v", tc.name, info.HDR, err)
		}
		var buf bytes.Buffer
		if err := m.Stream(context.Background(), 1, Request{Input: src, Source: info, MaxWidth: tc.maxW}, &buf, func(Plan) {}); err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		out := filepath.Join(dir, tc.name+".mp4")
		os.WriteFile(out, buf.Bytes(), 0o644)
		got, err := prober.Probe(context.Background(), out)
		if err != nil || got.HDR != "" || len(got.Streams) == 0 || got.Streams[0].ColorTransfer != "bt709" {
			t.Errorf("%s: output still HDR or untagged: %+v %v", tc.name, got.Streams, err)
		}
		midCrop, highCrop := "300:300:10:30", "300:300:330:30"
		if tc.maxW > 0 {
			if got.Width != tc.maxW {
				t.Errorf("%s: output width %d, want %d", tc.name, got.Width, tc.maxW)
			}
			midCrop, highCrop = "140:140:5:15", "140:140:165:15"
		}
		mid := testutil.AverageLuma(t, ffmpeg, out, "0.5", midCrop)
		high := testutil.AverageLuma(t, ffmpeg, out, "0.5", highCrop)
		if mid < tc.midMin || mid > tc.midMax || high < 230 || high <= mid {
			t.Errorf("%s: luma %d (diffuse) and %d (highlight); want %d-%d and >= 230", tc.name, mid, high, tc.midMin, tc.midMax)
		}
	}
}
