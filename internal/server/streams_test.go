package server

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mtdewwolf/sloplex/internal/lang"
	"github.com/mtdewwolf/sloplex/internal/library"
	"github.com/mtdewwolf/sloplex/internal/probe"
)

// newStreamsFixture: section 1 Movies with "Multi (2020).mkv" holding
// video, English AAC mono, French AC3 5.1 "Commentary", English SRT and a
// forced French SRT, plus a Spanish sidecar.
func newStreamsFixture(t *testing.T) (*fixture, Metadata) {
	t.Helper()
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not installed")
	}
	prober, err := probe.NewFFprobe("")
	if err != nil {
		t.Skip("ffprobe not installed")
	}
	root := t.TempDir()
	dir := filepath.Join(root, "movies", "Multi (2020)")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	en := filepath.Join(root, "en.srt")
	fr := filepath.Join(root, "fr.srt")
	os.WriteFile(en, []byte("1\n00:00:01,000 --> 00:00:03,000\nHello there\n\n2\n00:00:04,000 --> 00:00:05,500\nGeneral Kenobi\n"), 0o644)
	os.WriteFile(fr, []byte("1\n00:00:01,000 --> 00:00:03,000\nBonjour\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "Multi (2020).es.srt"), []byte("1\n00:00:01,000 --> 00:00:02,000\nHola\n"), 0o644)
	video := filepath.Join(dir, "Multi (2020).mkv")
	if out, err := exec.Command(ffmpeg, "-v", "error", "-f", "lavfi", "-i", "color=black:s=320x240:r=10:d=6",
		"-f", "lavfi", "-i", "sine=f=440:d=6", "-f", "lavfi", "-i", "sine=f=880:d=6", "-i", en, "-i", fr,
		"-map", "0", "-map", "1", "-map", "2", "-map", "3", "-map", "4",
		"-c:v", "libx264", "-c:a:0", "aac", "-c:a:1", "ac3", "-ac:a:1", "6", "-c:s", "srt",
		"-metadata:s:a:0", "language=eng", "-metadata:s:a:1", "language=fra", "-metadata:s:a:1", "title=Commentary",
		"-metadata:s:s:0", "language=eng", "-metadata:s:s:1", "language=fre", "-disposition:s:0", "0", "-disposition:s:1", "forced",
		video).CombinedOutput(); err != nil {
		t.Skipf("cannot build test video: %v %s", err, out)
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
	return f, f.getJSON(t, "/library/sections/1/all").Metadata[0]
}

func streamsOf(t *testing.T, f *fixture, key, token string) []StreamElem {
	t.Helper()
	var body struct{ MediaContainer MediaContainer }
	f.api(t, "GET", key, token, nil, &body)
	return body.MediaContainer.Metadata[0].Media[0].Part[0].Stream
}

func byTitle(streams []StreamElem) map[string]StreamElem {
	m := map[string]StreamElem{}
	for _, s := range streams {
		m[s.ExtendedDisplayTitle] = s
	}
	return m
}

func TestStreamListing(t *testing.T) {
	f, movie := newStreamsFixture(t)
	if movie.Media[0].Part[0].Stream != nil {
		t.Error("streams must not appear in lists")
	}
	streams := streamsOf(t, f, movie.Key, testToken)
	var titles []string
	for _, s := range streams {
		titles = append(titles, fmt.Sprintf("%d:%s", s.StreamType, s.ExtendedDisplayTitle))
	}
	want := "1:SD (H264)|2:English (AAC Mono)|2:French (AC3 5.1) – Commentary|3:English (SRT)|3:French Forced (SRT)|3:Spanish (SRT)"
	if got := strings.Join(titles, "|"); got != want {
		t.Fatalf("streams =\n%s\nwant\n%s", got, want)
	}
	m := byTitle(streams)
	eng, fre, spa := m["English (SRT)"], m["French Forced (SRT)"], m["Spanish (SRT)"]
	if eng.Index == nil || spa.Index != nil || spa.LanguageCode != "spa" || fre.LanguageCode != "fre" || fre.Language != "French" {
		t.Errorf("subtitle details: eng=%+v fre=%+v spa=%+v", eng, fre, spa)
	}
	if eng.Key != fmt.Sprintf("/library/streams/%d", eng.ID) || m["SD (H264)"].Key != "" {
		t.Errorf("keys: eng=%q video=%q", eng.Key, m["SD (H264)"].Key)
	}
	// Defaults: the first (default-flagged) audio and the forced subtitle.
	if !m["English (AAC Mono)"].Selected || m["French (AC3 5.1) – Commentary"].Selected || !fre.Selected || eng.Selected {
		t.Errorf("default selection wrong: %+v", streams)
	}
	if a := m["French (AC3 5.1) – Commentary"]; a.Channels != 6 || a.AudioChannelLayout == "" {
		t.Errorf("audio details = %+v", a)
	}
}

func TestStreamSelection(t *testing.T) {
	f, movie := newStreamsFixture(t)
	m := byTitle(streamsOf(t, f, movie.Key, testToken))
	partID := movie.Media[0].Part[0].ID
	fra, eng := m["French (AC3 5.1) – Commentary"], m["English (SRT)"]

	put := func(token, q string) int {
		return f.api(t, "PUT", fmt.Sprintf("/library/parts/%d?%s", partID, q), token, nil, nil).StatusCode
	}
	if code := put(testToken, fmt.Sprintf("audioStreamID=%d&subtitleStreamID=0", fra.ID)); code != 200 {
		t.Fatalf("select = %d", code)
	}
	m = byTitle(streamsOf(t, f, movie.Key, testToken))
	if !m["French (AC3 5.1) – Commentary"].Selected || m["French Forced (SRT)"].Selected {
		t.Errorf("after choosing French audio and no subtitles: %+v", m)
	}
	if code := put(testToken, fmt.Sprintf("subtitleStreamID=%d", eng.ID)); code != 200 {
		t.Fatal(code)
	}
	m = byTitle(streamsOf(t, f, movie.Key, testToken))
	if !m["English (SRT)"].Selected || !m["French (AC3 5.1) – Commentary"].Selected {
		t.Errorf("partial update lost the audio choice: %+v", m)
	}

	// Choices are per account.
	f.createUser(t, "alice", "wonderland", false)
	alice := f.login(t, "alice", "wonderland")
	if am := byTitle(streamsOf(t, f, movie.Key, alice)); !am["English (AAC Mono)"].Selected || !am["French Forced (SRT)"].Selected {
		t.Errorf("alice sees owner's choice: %+v", am)
	}

	for q, want := range map[string]int{
		fmt.Sprintf("audioStreamID=%d", eng.ID):    400, // a subtitle, not audio
		fmt.Sprintf("subtitleStreamID=%d", fra.ID): 400,
		"subtitleStreamID=x":                       400,
	} {
		if code := put(testToken, q); code != want {
			t.Errorf("PUT ?%s = %d, want %d", q, code, want)
		}
	}
	if code := f.api(t, "PUT", "/library/parts/99999?audioStreamID=1", testToken, nil, nil).StatusCode; code != 404 {
		t.Errorf("unknown part = %d", code)
	}
}

func TestSubtitleFiles(t *testing.T) {
	f, movie := newStreamsFixture(t)
	m := byTitle(streamsOf(t, f, movie.Key, testToken))
	eng, spa := m["English (SRT)"], m["Spanish (SRT)"]

	code, hdr, body := f.raw(t, eng.Key, testToken)
	if code != 200 || hdr.Get("Content-Type") != "application/x-subrip" || !strings.Contains(string(body), "General Kenobi") {
		t.Errorf("embedded srt = %d %q\n%s", code, hdr.Get("Content-Type"), body)
	}
	code, hdr, body = f.raw(t, eng.Key+"?format=vtt", testToken)
	if code != 200 || !strings.HasPrefix(hdr.Get("Content-Type"), "text/vtt") || !strings.HasPrefix(string(body), "WEBVTT") ||
		!strings.Contains(string(body), "00:01.000 --> 00:03.000") {
		t.Errorf("embedded vtt = %d\n%s", code, body)
	}
	_, _, body = f.raw(t, eng.Key+"?format=vtt&offset=2", testToken)
	if !strings.Contains(string(body), "00:00:00.000 --> 00:00:01.000\nHello there") || !strings.Contains(string(body), "00:00:02.000 --> 00:00:03.500") {
		t.Errorf("shifted vtt:\n%s", body)
	}
	_, _, body = f.raw(t, spa.Key, testToken)
	if string(body) != "1\n00:00:01,000 --> 00:00:02,000\nHola\n" {
		t.Errorf("sidecar raw = %q", body)
	}
	_, _, body = f.raw(t, spa.Key+"?format=ass", testToken)
	if !strings.Contains(string(body), "[Script Info]") || !strings.Contains(string(body), "Hola") {
		t.Errorf("sidecar as ass:\n%s", body)
	}

	video := m["SD (H264)"]
	for path, want := range map[string]int{
		fmt.Sprintf("/library/streams/%d", video.ID): 400,
		eng.Key + "?format=sub":                      400,
		eng.Key + "?offset=2":                        400,
		"/library/streams/99999":                     404,
	} {
		if code, _, _ := f.raw(t, path, testToken); code != want {
			t.Errorf("GET %s = %d, want %d", path, code, want)
		}
	}

	// Restricted accounts cannot reach streams of hidden libraries.
	f.api(t, "POST", "/sloplex/accounts", testToken, map[string]any{"name": "kid", "password": "cartoons1", "libraries": []int{}}, nil)
	kid := f.login(t, "kid", "cartoons1")
	if code, _, _ := f.raw(t, eng.Key, kid); code != 404 {
		t.Errorf("kid subtitle = %d", code)
	}
	if code := f.api(t, "PUT", fmt.Sprintf("/library/parts/%d?subtitleStreamID=0", movie.Media[0].Part[0].ID), kid, nil, nil).StatusCode; code != 404 {
		t.Errorf("kid select = %d", code)
	}
}

func TestTranscodeWithStreams(t *testing.T) {
	f, movie := newStreamsFixture(t)
	m := byTitle(streamsOf(t, f, movie.Key, testToken))
	fra, eng := m["French (AC3 5.1) – Commentary"], m["English (SRT)"]
	base := "/video/:/transcode/universal/start.mp4?path=" + movie.Key

	code, _, body := f.raw(t, fmt.Sprintf("%s&audioStreamID=%d&subtitleStreamID=%d&subtitles=burn", base, fra.ID, eng.ID), testToken)
	if code != 200 || len(body) < 1000 {
		t.Fatalf("transcode = %d, %d bytes", code, len(body))
	}
	out := filepath.Join(t.TempDir(), "out.mp4")
	os.WriteFile(out, body, 0o644)
	js, err := exec.Command("ffprobe", "-v", "error", "-show_entries", "stream=codec_type,codec_name,channels:stream_tags=language", "-of", "json", out).Output()
	if err != nil {
		t.Fatal(err)
	}
	var res struct {
		Streams []struct {
			Type     string            `json:"codec_type"`
			Codec    string            `json:"codec_name"`
			Channels int               `json:"channels"`
			Tags     map[string]string `json:"tags"`
		}
	}
	json.Unmarshal(js, &res)
	got := fmt.Sprintf("%+v", res.Streams)
	if code, _ := lang.Normalize(res.Streams[len(res.Streams)-1].Tags["language"]); len(res.Streams) != 2 || code != "fre" || res.Streams[1].Channels != 2 || res.Streams[0].Codec != "h264" {
		t.Errorf("output streams = %s", got)
	}

	for q, want := range map[string]int{
		fmt.Sprintf("&audioStreamID=%d", eng.ID):    400,
		fmt.Sprintf("&subtitleStreamID=%d", fra.ID): 400,
		"&subtitles=sidecar":                        400,
	} {
		if code, _, _ := f.raw(t, base+q, testToken); code != want {
			t.Errorf("start.mp4%s = %d, want %d", q, code, want)
		}
	}
}
