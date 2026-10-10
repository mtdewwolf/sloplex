package server

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/mtdewwolf/sloplex/internal/library"
	"github.com/mtdewwolf/sloplex/internal/probe"
)

func TestHLSQualityLevels(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not installed")
	}
	prober, err := probe.NewFFprobe("")
	if err != nil {
		t.Skip("ffprobe not installed")
	}
	root := t.TempDir()
	dir := filepath.Join(root, "movies", "Big (2020)")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	srt := filepath.Join(root, "en.srt")
	os.WriteFile(srt, []byte("1\n00:00:01,000 --> 00:00:02,000\nHi\n"), 0o644)
	if out, err := exec.Command(ffmpeg, "-v", "error", "-f", "lavfi", "-i", "testsrc2=size=1920x1080:rate=10:duration=5",
		"-f", "lavfi", "-i", "sine=duration=5", "-i", srt, "-map", "0", "-map", "1", "-map", "2", "-shortest",
		"-c:v", "mpeg4", "-q:v", "8", "-c:a", "mp2", "-c:s", "srt", "-metadata:s:s:0", "language=eng",
		filepath.Join(dir, "Big (2020).mkv")).CombinedOutput(); err != nil {
		t.Skipf("cannot make clip: %v %s", err, out)
	}
	store, _ := library.Open("")
	if err := store.SyncSections([]library.SectionDef{{Title: "Movies", Type: library.TypeMovie, Paths: []string{filepath.Join(root, "movies")}}}, time.Now()); err != nil {
		t.Fatal(err)
	}
	sc := library.NewScanner(store, prober, nil)
	if _, err := sc.Scan(context.Background(), store.Sections()[0].ID); err != nil {
		t.Fatal(err)
	}
	opts := Options{FriendlyName: "test", MachineID: "abc123", Version: "0.0.1", Tokens: []string{testToken}}
	f := &fixture{srv: startServer(t, opts, store, sc, ffmpeg), store: store, root: root}
	movie := f.getJSON(t, "/library/sections/1/all").Metadata[0]
	start := "/video/:/transcode/universal/start.m3u8?path=" + movie.Key

	code, _, master := f.raw(t, start+"&session=lv&abr=1", testToken)
	if code != 200 {
		t.Fatalf("start = %d\n%s", code, master)
	}
	variants := regexp.MustCompile(`(?m)^#EXT-X-STREAM-INF:BANDWIDTH=(\d+),RESOLUTION=(\d+x\d+),SUBTITLES="subs"\nsession/([0-9a-f]{32})/(\w+)/index\.m3u8$`).
		FindAllStringSubmatch(string(master), -1)
	var got []string
	for _, v := range variants {
		got = append(got, v[4]+"="+v[2]+"@"+v[1])
	}
	if strings.Join(got, " ") != "base=1920x1080@5136640 720p=1280x720@3160000 480p=852x480@1360000" {
		t.Fatalf("variants = %v\n%s", got, master)
	}
	if !strings.Contains(string(master), "#EXT-X-MEDIA:TYPE=SUBTITLES") {
		t.Errorf("subtitle rendition missing:\n%s", master)
	}
	base := "/video/:/transcode/universal/session/" + variants[0][3] + "/"
	for _, level := range []string{"base", "720p", "480p"} {
		code, _, index := f.raw(t, base+level+"/index.m3u8", "")
		if code != 200 || !strings.Contains(string(index), "00000.ts") {
			t.Errorf("%s index = %d\n%s", level, code, index)
		}
		code, hdr, seg := f.raw(t, base+level+"/00000.ts", "")
		if code != 200 || hdr.Get("Content-Type") != "video/mp2t" || len(seg) < 188 || seg[0] != 0x47 {
			t.Errorf("%s segment = %d %q, %d bytes", level, code, hdr.Get("Content-Type"), len(seg))
		}
	}
	for _, p := range []string{"1080p/index.m3u8", "720p/00000.mp4", "..%2fbase/index.m3u8", "subs/index.m3u8"} {
		if code, _, _ := f.raw(t, base+p, ""); code != 404 && code != 401 {
			t.Errorf("GET %s = %d, want 404 or 401", p, code)
		}
	}
	// The subtitle rendition still lines up from the main output.
	subs := regexp.MustCompile(`URI="(session/[0-9a-f]{32}/subs/\d+/)index\.m3u8"`).FindStringSubmatch(string(master))
	if subs == nil {
		t.Fatalf("no subtitle URI in:\n%s", master)
	}
	if code, _, vtt := f.raw(t, "/video/:/transcode/universal/"+subs[1]+"subs.vtt", ""); code != 200 || !strings.Contains(string(vtt), "X-TIMESTAMP-MAP=MPEGTS:") {
		t.Errorf("subs.vtt = %d\n%s", code, vtt)
	}
	f.raw(t, "/video/:/transcode/universal/stop?session=lv", testToken)

	// Without a measured capacity no levels are added by default; abr=0
	// never adds them.
	for _, q := range []string{"&session=lv", "&session=lv&abr=0"} {
		code, _, master := f.raw(t, start+q, testToken)
		if code != 200 || strings.Count(string(master), "#EXT-X-STREAM-INF") != 1 || !strings.Contains(string(master), "/base/index.m3u8") {
			t.Errorf("start%s = %d, want one variant:\n%s", q, code, master)
		}
		f.raw(t, "/video/:/transcode/universal/stop?session=lv", testToken)
	}
	if code, _, _ := f.raw(t, start+"&abr=yes", testToken); code != 400 {
		t.Errorf("abr=yes = %d", code)
	}
}
