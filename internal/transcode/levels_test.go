package transcode

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/mtdewwolf/sloplex/internal/probe"
)

func levelNames(ls []Level) string {
	var out []string
	for _, l := range ls {
		out = append(out, fmt.Sprintf("%s=%dx%d@%d", l.Name, l.Width, l.Height, l.MaxBitrate))
	}
	return strings.Join(out, " ")
}

func TestLevelCandidates(t *testing.T) {
	for _, c := range []struct {
		name string
		r    Request
		want string
	}{
		{"1080p source", Request{Source: probe.Info{VideoCodec: "hevc", Width: 1920, Height: 1080}},
			"720p=1280x720@3000 480p=852x480@1200"},
		{"4K source", Request{Source: probe.Info{VideoCodec: "hevc", Width: 3840, Height: 2160}},
			"1080p=1920x1080@8000 720p=1280x720@3000 480p=852x480@1200"},
		{"scope 2.39:1", Request{Source: probe.Info{VideoCodec: "hevc", Width: 1920, Height: 804}},
			"720p=1280x536@3000 480p=854x356@1200"},
		{"capped at 720p", Request{Source: probe.Info{VideoCodec: "hevc", Width: 1920, Height: 1080}, MaxWidth: 1280, MaxHeight: 720},
			"480p=852x480@1200"},
		{"bitrate cap below 720p's", Request{Source: probe.Info{VideoCodec: "hevc", Width: 1920, Height: 1080}, MaxBitrate: 2000},
			"480p=852x480@1200"},
		{"480p source", Request{Source: probe.Info{VideoCodec: "mpeg4", Width: 854, Height: 480}}, ""},
		{"copied video", Request{Source: probe.Info{VideoCodec: "h264", Width: 1920, Height: 1080}}, ""},
		{"WebM", Request{Source: probe.Info{VideoCodec: "hevc", Width: 1920, Height: 1080}, WebM: true}, ""},
	} {
		if got := levelNames(LevelCandidates(c.r, Decide(c.r))); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}

func TestArgsLevels(t *testing.T) {
	r := Request{Input: "in.mkv", MaxBitrate: 6000,
		Source: probe.Info{VideoCodec: "hevc", Width: 1920, Height: 1080, AudioCodec: "ac3", AudioChannels: 6}}
	p := Decide(r)
	p.Levels = LevelCandidates(r, p)
	got := strings.Join(Args(r, p, Output{HLSDir: "/s"}, Pipeline{}), " ")
	for _, want := range []string{
		"-filter_complex [0:v:0]split=3[lv0][lv1][lv2];[lv1]scale=1280:720[lo1];[lv2]scale=852:480[lo2]",
		"-map [lv0] -map 0:a:0 -map [lo1] -map 0:a:0 -map [lo2] -map 0:a:0 -sn -dn",
		"-maxrate:v:0 6000k -bufsize:v:0 12000k -maxrate:v:1 3000k -bufsize:v:1 6000k -maxrate:v:2 1200k -bufsize:v:2 2400k",
		"-force_key_frames expr:gte(t,n_forced*4)",
		"-c:a aac -ac 2",
		"-var_stream_map v:0,a:0,name:base v:1,a:1,name:720p v:2,a:2,name:480p -hls_segment_filename /s/%v/%05d.ts /s/%v/index.m3u8",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("level args lack %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "-maxrate 6000k") {
		t.Errorf("the main cap applies to every level:\n%s", got)
	}

	// Filters run once, before the split; no audio means no audio maps.
	hdr := Request{Input: "in.mkv", Source: probe.Info{VideoCodec: "hevc", Width: 3840, Height: 2160, HDR: probe.HDRPQ}}
	hp := Decide(hdr)
	hp.Levels = LevelCandidates(hdr, hp)[:1]
	got = strings.Join(Args(hdr, hp, Output{HLSDir: "/s"}, Pipeline{}), " ")
	if !strings.Contains(got, "tonemap=") || !strings.Contains(got, "[vout];[vout]split=2[lv0][lv1];[lv1]scale=1920:1080[lo1]") ||
		strings.Contains(got, "0:a") || !strings.Contains(got, "-var_stream_map v:0,name:base v:1,name:1080p") {
		t.Errorf("HDR level args:\n%s", got)
	}

	// Levels are ignored outside HLS and on hardware pipelines.
	for _, c := range []struct {
		out Output
		pl  Pipeline
	}{{Output{}, Pipeline{}}, {Output{HLSDir: "/s"}, Pipeline{Accel: AccelVAAPI, Device: "/d"}}} {
		if got := strings.Join(Args(r, p, c.out, c.pl), " "); strings.Contains(got, "split=") || strings.Contains(got, "var_stream_map") {
			t.Errorf("levels used for %+v:\n%s", c, got)
		}
	}
}

func TestChooseLevels(t *testing.T) {
	m, err := New(Options{FFmpeg: "ffmpeg", Dir: t.TempDir(), MaxTranscodes: 2})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	r := Request{Source: probe.Info{VideoCodec: "hevc", Width: 1920, Height: 1080, FrameRate: 24}}
	choose := func(r Request, load float64) (string, float64) {
		m.mu.Lock()
		defer m.mu.Unlock()
		p := Decide(r)
		p.Preset = "veryfast"
		extra := m.chooseLevelsLocked(r, &p, load)
		return levelNames(p.Levels), extra
	}
	// Unknown capacity: none, unless forced.
	if got, _ := choose(r, 0.2); got != "" {
		t.Errorf("unknown capacity added %q", got)
	}
	forced := r
	forced.ABR = ABROn
	if got, _ := choose(forced, 0.2); got != "720p=1280x720@3000 480p=852x480@1200" {
		t.Errorf("abr=1 = %q", got)
	}

	// 240 fps at 720p veryfast: a 720p level at 24 fps costs 0.1, 480p
	// about 0.044.
	m.SetCapacity(Capacity{"veryfast": 240, "faster": 168, "superfast": 336, "ultrafast": 528})
	for _, c := range []struct {
		load float64
		want string
	}{
		{0.2, "720p=1280x720@3000 480p=852x480@1200"},
		{0.66, "480p=852x480@1200"}, // 720p would pass 0.75; 480p still fits
		{0.74, ""},
	} {
		got, extra := choose(r, c.load)
		if got != c.want {
			t.Errorf("main load %.2f: levels %q, want %q", c.load, got, c.want)
		}
		if c.want == "" && extra != 0 {
			t.Errorf("no levels but extra load %v", extra)
		}
	}
	if _, extra := choose(r, 0.2); extra < 0.14 || extra > 0.15 {
		t.Errorf("level load = %v, want about 0.144", extra)
	}
	off := r
	off.ABR = ABROff
	if got, _ := choose(off, 0.2); got != "" {
		t.Errorf("abr=0 added %q", got)
	}
	m.hw = HW{Accel: AccelVAAPI, Device: "/d"}
	if got, _ := choose(r, 0.2); got != "" {
		t.Errorf("hardware encoding added %q", got)
	}
}

// TestHLSLevels runs a real session with every level and checks that the
// levels line up segment for segment.
func TestHLSLevels(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not installed")
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "big.mkv")
	if out, err := exec.Command(ffmpeg, "-v", "error", "-f", "lavfi", "-i", "testsrc2=size=1920x1080:rate=10:duration=9",
		"-f", "lavfi", "-i", "sine=duration=9", "-shortest", "-c:v", "mpeg4", "-q:v", "8", "-c:a", "mp2", src).CombinedOutput(); err != nil {
		t.Skipf("cannot make clip: %v %s", err, out)
	}
	m, err := New(Options{FFmpeg: ffmpeg, Dir: filepath.Join(dir, "tc"), MaxTranscodes: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	r := Request{Input: src, ABR: ABROn,
		Source: probe.Info{VideoCodec: "mpeg4", Width: 1920, Height: 1080, AudioCodec: "mp2", AudioChannels: 1, Duration: 9000}}
	s, err := m.StartHLS(Owner{Account: 1}, "abr", 1, r)
	if err != nil {
		t.Fatal(err)
	}
	if got := levelNames(s.Plan.Levels); got != "720p=1280x720@3000 480p=852x480@1200" {
		t.Fatalf("levels = %q", got)
	}
	if _, ok := s.LevelDir("1080p"); ok {
		t.Error("a level the session does not have resolved")
	}
	if _, err := m.Playlist(context.Background(), s, "1080p"); err != ErrNotFound {
		t.Errorf("unknown level playlist: %v", err)
	}
	// A capacity measured later counts the levels in the session's load.
	c := Capacity{"veryfast": 240, "faster": 168, "superfast": 336, "ultrafast": 528}
	m.SetCapacity(c)
	m.mu.Lock()
	load, want := s.load, c.Load(r.Source, s.Plan, s.Plan.Preset)+c.LevelsLoad(r.Source, s.Plan)
	m.mu.Unlock()
	if load != want || c.LevelsLoad(r.Source, s.Plan) <= 0 {
		t.Errorf("session load = %v, want %v including levels", load, want)
	}
	<-s.done
	if s.err != nil {
		t.Fatal(s.err)
	}
	firstPTS := func(path string) string {
		out, err := exec.Command("ffprobe", "-v", "error", "-select_streams", "v", "-show_entries", "packet=pts:stream=width",
			"-read_intervals", "%+#1", "-of", "csv=p=0", path).Output()
		if err != nil {
			t.Fatalf("ffprobe %s: %v", path, err)
		}
		return strings.Join(strings.Fields(strings.ReplaceAll(string(out), ",", " ")), " ")
	}
	count := map[string]int{}
	for _, level := range []string{"base", "720p", "480p"} {
		pl, err := m.Playlist(context.Background(), s, level)
		if err != nil {
			t.Fatalf("%s playlist: %v", level, err)
		}
		b, _ := os.ReadFile(pl)
		count[level] = strings.Count(string(b), ".ts")
		if !strings.Contains(string(b), "#EXT-X-ENDLIST") {
			t.Errorf("%s playlist not finished:\n%s", level, b)
		}
	}
	if count["base"] != 3 || count["720p"] != 3 || count["480p"] != 3 {
		t.Fatalf("segment counts = %v, want 3 each", count)
	}
	for n := 0; n < 3; n++ {
		var pts []string
		for level, width := range map[string]int{"base": 1920, "720p": 1280, "480p": 852} {
			seg, err := m.Segment(context.Background(), s, level, fmt.Sprintf("%05d.ts", n))
			if err != nil {
				t.Fatalf("%s segment %d: %v", level, n, err)
			}
			fields := strings.Fields(firstPTS(seg)) // the first packet's pts, then the width
			if len(fields) < 2 || fields[len(fields)-1] != strconv.Itoa(width) {
				t.Errorf("%s segment %d: %v, want width %d", level, n, fields, width)
				continue
			}
			pts = append(pts, fields[0])
		}
		if len(pts) == 3 && (pts[0] != pts[1] || pts[1] != pts[2]) {
			t.Errorf("segment %d starts at different times per level: %v", n, pts)
		}
	}
}
