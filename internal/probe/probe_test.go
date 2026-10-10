package probe

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
)

func TestParseFFprobe(t *testing.T) {
	out := []byte(`{
		"streams": [
			{"codec_type": "video", "codec_name": "hevc", "width": 3840, "height": 2160, "avg_frame_rate": "24000/1001", "r_frame_rate": "24000/1001"},
			{"codec_type": "audio", "codec_name": "eac3", "channels": 6},
			{"codec_type": "audio", "codec_name": "aac", "channels": 2},
			{"codec_type": "subtitle", "codec_name": "subrip"}
		],
		"format": {"duration": "7265.123000", "bit_rate": "15000000"}
	}`)
	got, err := parseFFprobe(out)
	if err != nil {
		t.Fatal(err)
	}
	want := Info{Duration: 7265123, Bitrate: 15000, Width: 3840, Height: 2160, VideoCodec: "hevc", AudioCodec: "eac3", AudioChannels: 6, FrameRate: 23.976}
	streams := got.Streams
	got.Streams = nil
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
	if len(streams) != 4 || streams[1].Channels != 6 || streams[3].Type != StreamSubtitle || streams[3].Codec != "subrip" {
		t.Errorf("streams = %+v", streams)
	}
}

func TestContainerForPath(t *testing.T) {
	if c := ContainerForPath("/a/B.MKV"); c != "mkv" {
		t.Errorf("mkv: %q", c)
	}
	if c := ContainerForPath("/a/b.m4v"); c != "mp4" {
		t.Errorf("m4v: %q", c)
	}
	if IsVideo("/a/b.srt") {
		t.Error("srt reported as video")
	}
}

// TestFFprobeReal exercises the real binary on a tiny generated clip when
// ffmpeg and ffprobe are installed.
func TestFFprobeReal(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not installed")
	}
	p, err := NewFFprobe("")
	if err != nil {
		t.Skip("ffprobe not installed")
	}
	path := filepath.Join(t.TempDir(), "clip.mkv")
	cmd := exec.Command(ffmpeg, "-v", "error", "-f", "lavfi", "-i", "testsrc=size=320x240:duration=1:rate=10",
		"-f", "lavfi", "-i", "sine=duration=1", "-shortest", "-c:v", "mpeg4", "-c:a", "mp2", path)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("ffmpeg could not generate clip: %v\n%s", err, out)
	}
	info, err := p.Probe(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Width != 320 || info.Height != 240 || info.Container != "mkv" || info.Duration < 900 || info.AudioCodec == "" || info.FrameRate != 10 {
		t.Errorf("info = %+v", info)
	}
}

func TestParseFFprobeStreamDetails(t *testing.T) {
	out := []byte(`{"format": {}, "streams": [
		{"index": 0, "codec_type": "video", "codec_name": "h264", "width": 1920, "height": 1080},
		{"index": 1, "codec_type": "audio", "codec_name": "ac3", "channels": 6, "channel_layout": "5.1(side)",
		 "tags": {"language": "fra", "title": "Commentary"}, "disposition": {"default": 0}},
		{"index": 2, "codec_type": "subtitle", "codec_name": "hdmv_pgs_subtitle",
		 "tags": {"language": "und"}, "disposition": {"forced": 1, "hearing_impaired": 1}},
		{"index": 3, "codec_type": "video", "codec_name": "mjpeg", "disposition": {"attached_pic": 1}},
		{"index": 4, "codec_type": "attachment", "codec_name": "ttf"}
	]}`)
	got, err := parseFFprobe(out)
	if err != nil {
		t.Fatal(err)
	}
	want := []Stream{
		{Index: 0, Type: StreamVideo, Codec: "h264", Width: 1920, Height: 1080},
		{Index: 1, Type: StreamAudio, Codec: "ac3", Language: "fra", Title: "Commentary", Channels: 6, ChannelLayout: "5.1(side)"},
		{Index: 2, Type: StreamSubtitle, Codec: "hdmv_pgs_subtitle", Forced: true, HearingImpaired: true},
	}
	if !reflect.DeepEqual(got.Streams, want) {
		t.Errorf("streams =\n%+v\nwant\n%+v", got.Streams, want)
	}
}

func TestParseRate(t *testing.T) {
	for in, want := range map[string]float64{
		"NaN": 0, "NaN/1": 0, "24/NaN": 0, "+Inf": 0, "+Inf/+Inf": 0, "24/+Inf": 0, "-Inf": 0, "1e308/1e-308": 0,
		"24000/1001": 23.976, "25/1": 25, "30": 30, "0/0": 0, "": 0, "1/0": 0, "x/1": 0, "90000/1": 0,
	} {
		if got := parseRate(in); got != want {
			t.Errorf("parseRate(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestProbeFrameRateFallbackIsFinite(t *testing.T) {
	for _, tc := range []struct {
		avg, fallback string
		want          float64
	}{
		{"NaN", "24000/1001", 23.976}, {"25", "NaN", 25}, {"NaN", "+Inf", 0},
	} {
		info, err := parseFFprobe([]byte(fmt.Sprintf(`{"format":{},"streams":[{"codec_type":"video","codec_name":"h264","avg_frame_rate":%q,"r_frame_rate":%q}]}`, tc.avg, tc.fallback)))
		if err != nil {
			t.Fatal(err)
		}
		if info.FrameRate != tc.want {
			t.Errorf("frame rate %v, want %v", info.FrameRate, tc.want)
		}
		if _, err := json.Marshal(info); err != nil {
			t.Fatal(err)
		}
	}
}
