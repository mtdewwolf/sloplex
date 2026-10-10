package server

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mtdewwolf/sloplex/internal/library"
	"github.com/mtdewwolf/sloplex/internal/probe"
)

var subsRenditionRE = regexp.MustCompile(`(?m)^#EXT-X-MEDIA:TYPE=SUBTITLES,.*URI="(session/[0-9a-f]{32}/subs/(\d+)/index\.m3u8)"$`)

func TestHLSSubtitleRenditions(t *testing.T) {
	f, movie := newStreamsFixture(t)
	m := byTitle(streamsOf(t, f, movie.Key, testToken))
	eng, fre, spa, audio := m["English (SRT)"], m["French Forced (SRT)"], m["Spanish (SRT)"], m["English (AAC Mono)"]
	if eng.ID == 0 || fre.ID == 0 || spa.ID == 0 || audio.ID == 0 {
		t.Fatalf("fixture streams: %v", m)
	}
	start := "/video/:/transcode/universal/start.m3u8?path=" + movie.Key

	code, _, master := f.raw(t, start+"&session=subs-1", testToken)
	if code != 200 {
		t.Fatalf("start = %d\n%s", code, master)
	}
	text := string(master)
	for _, want := range []string{
		fmt.Sprintf(`NAME="English (SRT)",LANGUAGE="en",DEFAULT=NO,AUTOSELECT=YES,FORCED=NO,URI="session/`),
		fmt.Sprintf(`NAME="French Forced (SRT)",LANGUAGE="fr",DEFAULT=YES,AUTOSELECT=YES,FORCED=YES,URI="session/`),
		fmt.Sprintf(`NAME="Spanish (SRT)",LANGUAGE="es",DEFAULT=NO,AUTOSELECT=YES,FORCED=NO,URI="session/`),
		`,SUBTITLES="subs"` + "\nsession/",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("master playlist lacks %q:\n%s", want, text)
		}
	}
	rends := subsRenditionRE.FindAllStringSubmatch(text, -1)
	if len(rends) != 3 {
		t.Fatalf("renditions = %d:\n%s", len(rends), text)
	}
	if strings.Index(text, "#EXT-X-MEDIA") > strings.Index(text, "#EXT-X-STREAM-INF") {
		t.Errorf("renditions must come before the variant:\n%s", text)
	}
	urls := map[int]string{}
	for _, r := range rends {
		id, _ := strconv.Atoi(r[2])
		urls[id] = "/video/:/transcode/universal/" + r[1]
	}

	// The rendition playlist is a finished VOD list with one WebVTT segment,
	// fetched without a token like the other session URLs.
	code, hdr, index := f.raw(t, urls[fre.ID], "")
	if code != 200 || hdr.Get("Content-Type") != "application/vnd.apple.mpegurl" {
		t.Fatalf("subs index = %d %q", code, hdr.Get("Content-Type"))
	}
	if !regexp.MustCompile(`#EXT-X-PLAYLIST-TYPE:VOD\n#EXTINF:[67]\.000,\nsubs\.vtt\n#EXT-X-ENDLIST\n$`).Match(index) {
		t.Errorf("subs index:\n%s", index)
	}
	vttURL := strings.TrimSuffix(urls[fre.ID], "index.m3u8") + "subs.vtt"
	code, hdr, vtt := f.raw(t, vttURL, "")
	if code != 200 || !strings.HasPrefix(hdr.Get("Content-Type"), "text/vtt") {
		t.Fatalf("subs.vtt = %d %q\n%s", code, hdr.Get("Content-Type"), vtt)
	}
	if !regexp.MustCompile(`^WEBVTT\nX-TIMESTAMP-MAP=MPEGTS:\d+,LOCAL:00:00:00\.000\n\n`).Match(vtt) ||
		!regexp.MustCompile(`(00:)?00:01\.000 --> (00:)?00:03\.000\nBonjour`).Match(vtt) {
		t.Errorf("subs.vtt:\n%s", vtt)
	}
	// The sidecar works the same way.
	_, _, vtt = f.raw(t, strings.TrimSuffix(urls[spa.ID], "index.m3u8")+"subs.vtt", "")
	if !strings.Contains(string(vtt), "Hola") {
		t.Errorf("sidecar subs.vtt:\n%s", vtt)
	}

	// Only text subtitles of the session's own part.
	base := strings.TrimSuffix(urls[eng.ID], fmt.Sprintf("%d/index.m3u8", eng.ID))
	for _, p := range []string{
		fmt.Sprintf("%s%d/index.m3u8", base, audio.ID),
		base + "999999/index.m3u8",
		base + "x/index.m3u8",
		fmt.Sprintf("%s%d/other.txt", base, eng.ID),
		"/video/:/transcode/universal/session/" + strings.Repeat("0", 32) + fmt.Sprintf("/subs/%d/index.m3u8", eng.ID),
	} {
		if code, _, _ := f.raw(t, p, ""); code != 404 {
			t.Errorf("GET %s = %d, want 404", p, code)
		}
	}

	// An explicit choice moves DEFAULT=YES; burning in or "none" drops the
	// renditions.
	_, _, master = f.raw(t, fmt.Sprintf("%s&session=subs-2&subtitleStreamID=%d", start, eng.ID), testToken)
	if !strings.Contains(string(master), `NAME="English (SRT)",LANGUAGE="en",DEFAULT=YES`) ||
		!strings.Contains(string(master), `NAME="French Forced (SRT)",LANGUAGE="fr",DEFAULT=NO`) {
		t.Errorf("explicit subtitle choice:\n%s", master)
	}
	// Free the transcode slots first.
	for _, id := range []string{"subs-1", "subs-2"} {
		f.raw(t, "/video/:/transcode/universal/stop?session="+id, testToken)
	}
	for _, q := range []string{"&subtitles=none", "&subtitles=burn"} {
		code, _, master := f.raw(t, start+"&session=subs-3"+q, testToken)
		if code != 200 || strings.Contains(string(master), "EXT-X-MEDIA") || strings.Contains(string(master), "SUBTITLES=") {
			t.Errorf("start%s = %d, want no renditions:\n%s", q, code, master)
		}
	}
}

// TestHLSSubtitleAlignment checks the timing a player gets from RFC 8216:
// a cue at source time T must land on the video frame shown at T, after
// mapping its LOCAL time to MPEG-TS time with X-TIMESTAMP-MAP.
func TestHLSSubtitleAlignment(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not installed")
	}
	prober, err := probe.NewFFprobe("")
	if err != nil {
		t.Skip("ffprobe not installed")
	}
	root := t.TempDir()
	dir := filepath.Join(root, "movies", "Flash (2020)")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	srt := filepath.Join(root, "cue.srt")
	if err := os.WriteFile(srt, []byte("1\n00:00:03,000 --> 00:00:05,000\nNow\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Black until 3.0s, then white.
	if out, err := exec.Command(ffmpeg, "-v", "error",
		"-f", "lavfi", "-i", "color=black:s=320x180:r=24:d=3", "-f", "lavfi", "-i", "color=white:s=320x180:r=24:d=4",
		"-f", "lavfi", "-i", "sine=d=7", "-i", srt,
		"-filter_complex", "[0:v][1:v]concat=n=2:v=1[v]", "-map", "[v]", "-map", "2", "-map", "3",
		"-c:v", "mpeg4", "-c:a", "mp2", "-c:s", "srt", "-metadata:s:s:0", "language=eng",
		filepath.Join(dir, "Flash (2020).mkv")).CombinedOutput(); err != nil {
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

	segDir := t.TempDir()
	for _, offset := range []float64{0, 2} {
		t.Run(fmt.Sprint("offset=", offset), func(t *testing.T) {
			code, _, master := f.raw(t, fmt.Sprintf("/video/:/transcode/universal/start.m3u8?session=align-%v&offset=%v&path=%s", offset, offset, movie.Key), testToken)
			rend := subsRenditionRE.FindStringSubmatch(string(master))
			sess := variantRE.FindStringSubmatch(string(master))
			if code != 200 || rend == nil || sess == nil {
				t.Fatalf("start = %d\n%s", code, master)
			}
			base := "/video/:/transcode/universal/session/" + sess[1]

			_, _, vtt := f.raw(t, base+"/subs/"+rend[2]+"/subs.vtt", "")
			mm := regexp.MustCompile(`X-TIMESTAMP-MAP=MPEGTS:(\d+),LOCAL:00:00:00\.000`).FindSubmatch(vtt)
			cue := regexp.MustCompile(`(?m)^(\d\d:)?(\d\d):(\d\d)\.(\d\d\d) --> `).FindSubmatch(vtt)
			if mm == nil || cue == nil {
				t.Fatalf("subs.vtt:\n%s", vtt)
			}
			mpegts, _ := strconv.ParseInt(string(mm[1]), 10, 64)
			var local int64 // milliseconds
			for i, mul := range []int64{3600000, 60000, 1000, 1} {
				n, _ := strconv.ParseInt(strings.TrimSuffix(string(cue[i+1]), ":"), 10, 64)
				local += n * mul
			}
			if want := int64((3 - offset) * 1000); local != want {
				t.Errorf("cue starts at %d ms, want %d", local, want)
			}
			cuePTS := mpegts + local*90

			white := firstWhitePTS(t, f, base, filepath.Join(segDir, fmt.Sprint(offset*10)))
			f.raw(t, fmt.Sprintf("/video/:/transcode/universal/stop?session=align-%v", offset), testToken)
			// One frame at 24 fps is 3750 ticks.
			if d := cuePTS - white; d < -3750 || d > 3750 {
				t.Errorf("cue maps to PTS %d but the picture turns white at %d (%.3fs apart)", cuePTS, white, float64(d)/90000)
			}
		})
	}
}

// firstWhitePTS fetches a session's segments in order and returns the PTS of
// the first bright video frame.
// dir must be a path the movie filter can take unescaped.
func firstWhitePTS(t *testing.T, f *fixture, base, dir string) int64 {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10; i++ {
		name := fmt.Sprintf("%05d.ts", i)
		code, _, seg := f.raw(t, base+"/base/"+name, "")
		if code != 200 {
			break
		}
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, seg, 0o644); err != nil {
			t.Fatal(err)
		}
		// Raw PTS values from the segment, with the average luma of each frame.
		out, err := exec.Command("ffprobe", "-v", "error", "-f", "lavfi", "-i", "movie="+path+",signalstats",
			"-show_entries", "frame=pts:frame_tags=lavfi.signalstats.YAVG", "-of", "csv=p=0").CombinedOutput()
		if err != nil {
			t.Fatalf("ffprobe %s: %v %s", name, err, out)
		}
		for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			fields := strings.Split(line, ",")
			if len(fields) < 2 {
				continue
			}
			pts, err1 := strconv.ParseInt(fields[0], 10, 64)
			yavg, err2 := strconv.ParseFloat(fields[1], 64)
			if err1 == nil && err2 == nil && yavg > 128 {
				return pts
			}
		}
	}
	t.Fatal("no white frame found in the segments")
	return 0
}
