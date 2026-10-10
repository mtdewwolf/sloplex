package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/mtdewwolf/sloplex/internal/library"
	"github.com/mtdewwolf/sloplex/internal/probe"
	"github.com/mtdewwolf/sloplex/internal/transcode"
)

type decisionResult struct {
	MediaContainer MediaContainer
}

func (f *fixture) decision(t *testing.T, key, params string) (MediaContainer, int) {
	t.Helper()
	req, _ := http.NewRequest("GET", f.srv.URL+"/video/:/transcode/universal/decision?path="+key+params, nil)
	req.Header.Set(tokenHeader, testToken)
	req.Header.Set("Accept", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var res decisionResult
	if resp.StatusCode == 200 {
		if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
			t.Fatal(err)
		}
	}
	return res.MediaContainer, resp.StatusCode
}

// summary renders the parts of a decision the tests compare.
func decisionSummary(mc MediaContainer) string {
	out := fmt.Sprintf("%d/%d/%d", mc.GeneralDecisionCode, mc.DirectPlayDecisionCode, mc.TranscodeDecisionCode)
	if len(mc.Metadata) != 1 || len(mc.Metadata[0].Media) == 0 {
		return out + " no media"
	}
	m := mc.Metadata[0].Media[0]
	p := m.Part[0]
	out += fmt.Sprintf(" %s %s %s/%s %s", m.Protocol, p.Container, m.VideoCodec, m.AudioCodec, p.Decision)
	for _, st := range p.Stream {
		out += fmt.Sprintf(" %d:%s", st.StreamType, st.Decision)
	}
	return out
}

func TestDecision(t *testing.T) {
	f, movie := newStreamsFixture(t)
	m := byTitle(streamsOf(t, f, movie.Key, testToken))
	fra, eng := m["French (AC3 5.1) – Commentary"], m["English (SRT)"]

	for _, c := range []struct {
		params, want, why string
	}{
		// Defaults: MP4/H.264/AAC client; the mkv must be remuxed.
		{"", "1001/3000/1001 hls mpegts h264/aac copy 1:copy 2:copy", "the container mkv is not supported"},
		{"&containers=mkv,mp4", "1000/1000/0  mkv h264/aac directplay", ""},
		{"&containers=MKV&directPlay=0", "1001/3000/1001 hls mpegts h264/aac copy 1:copy 2:copy", "the client asked for no direct play"},
		// The French track is AC3 5.1.
		{fmt.Sprintf("&containers=mkv&audioStreamID=%d", fra.ID), "1001/3000/1001 hls mpegts h264/aac copy 1:copy 2:transcode",
			"the audio codec ac3 is not supported; the audio has 6 channels, more than 2"},
		{fmt.Sprintf("&containers=mkv&audioStreamID=%d&audioCodecs=aac,ac3&maxAudioChannels=6", fra.ID), "1000/1000/0  mkv h264/aac directplay", ""},
		// Size and bitrate limits force an encode.
		{"&containers=mkv&videoResolution=160x120", "1001/3000/1001 hls mpegts h264/aac transcode 1:transcode 2:copy", "the video is larger than 160x120"},
		// A burned-in text subtitle rules out direct play.
		{fmt.Sprintf("&containers=mkv&subtitleStreamID=%d&subtitles=burn", eng.ID), "1001/3000/1001 hls mpegts h264/aac transcode 1:transcode 2:copy 3:burn",
			"a subtitle must be burned in"},
		// Other protocols.
		{"&protocol=http", "1001/3000/1001 http mp4 h264/aac copy 1:copy 2:copy", ""},
		{"&protocol=webm", "2000/3000/4000 no media", "the client does not decode vp9"},
		{"&protocol=webm&videoCodecs=vp9&audioCodecs=opus", "1001/3000/1001 webm webm vp9/opus transcode 1:transcode 2:transcode", ""},
		{"&videoCodecs=hevc", "2000/3000/4000 no media", "the client does not decode h264"},
	} {
		mc, code := f.decision(t, movie.Key, c.params)
		if got := decisionSummary(mc); code != 200 || got != c.want {
			t.Errorf("decision%s = %d %q, want %q", c.params, code, got, c.want)
		}
		text := mc.DirectPlayDecisionText + " | " + mc.TranscodeDecisionText
		if c.why != "" && !strings.Contains(text, c.why) {
			t.Errorf("decision%s texts = %q, want %q", c.params, text, c.why)
		}
	}

	// The direct-play answer keeps the stored part; conversions describe
	// the output.
	mc, _ := f.decision(t, movie.Key, "&containers=mkv")
	if p := mc.Metadata[0].Media[0].Part[0]; !mc.Metadata[0].Media[0].Selected || p.Key == "" || p.Container != "mkv" || mc.Metadata[0].Title != "Multi" {
		t.Errorf("direct play media = %+v", mc.Metadata[0].Media[0])
	}
	mc, _ = f.decision(t, movie.Key, "&videoResolution=160x120")
	if media := mc.Metadata[0].Media[0]; media.Width != 160 || media.Height != 120 || media.Bitrate <= 0 || media.Part[0].Key != "" {
		t.Errorf("conversion media = %+v", media)
	}

	for _, p := range []string{"&protocol=dash", "&hdr=yes", "&directPlay=2", "&maxAudioChannels=0", "&audioStreamID=1"} {
		if _, code := f.decision(t, movie.Key, p); code != 400 {
			t.Errorf("decision%s = %d, want 400", p, code)
		}
	}
	if _, code := f.decision(t, "/library/metadata/99999", ""); code != 404 {
		t.Errorf("unknown item = %d", code)
	}
	_, _, xml := f.raw(t, "/video/:/transcode/universal/decision?path="+movie.Key, testToken)
	for _, want := range []string{`generalDecisionCode="1001"`, `directPlayDecisionText="Direct play not available: the container mkv is not supported"`,
		`selected="1"`, `protocol="hls"`, `decision="copy"`} {
		if !strings.Contains(string(xml), want) {
			t.Errorf("XML lacks %s:\n%s", want, xml)
		}
	}
}

func TestDecisionWithoutFFmpeg(t *testing.T) {
	f := newFixture(t, Options{})
	items := f.getJSON(t, "/library/sections/1/all").Metadata
	var mkv, mp4 Metadata
	for _, it := range items {
		switch it.Media[0].Container {
		case "mkv":
			mkv = it
		case "mp4":
			mp4 = it
		}
	}
	mc, _ := f.decision(t, mp4.Key, "")
	if got := decisionSummary(mc); got != "1000/1000/0  mp4 h264/aac directplay" {
		t.Errorf("mp4 = %s", got)
	}
	mc, _ = f.decision(t, mkv.Key, "")
	if got := decisionSummary(mc); got != "2000/3000/4000 no media" || !strings.Contains(mc.TranscodeDecisionText, "ffmpeg is not installed") {
		t.Errorf("mkv = %s %q", got, mc.TranscodeDecisionText)
	}
}

func TestDirectPlayProblemsHDR(t *testing.T) {
	c := clientCaps{directPlay: true, containers: codecSet("mkv", ""), videos: codecSet("hevc", ""), audios: codecSet("aac", ""), maxChannels: 2}
	part := library.Part{Container: "mkv"}
	req := transcode.Request{Source: probe.Info{VideoCodec: "hevc", HDR: probe.HDRPQ}, MaxBitrate: 4000}
	if got := strings.Join(directPlayProblems(c, part, req), "; "); got != "the bitrate is above 4000 kbps or unknown; the video is HDR" {
		t.Errorf("problems = %q", got)
	}
	c.hdr, req.MaxBitrate = true, 0
	if got := directPlayProblems(c, part, req); len(got) != 0 {
		t.Errorf("HDR client: %q", got)
	}
}
