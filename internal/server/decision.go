package server

import (
	"cmp"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/mtdewwolf/sloplex/internal/library"
	"github.com/mtdewwolf/sloplex/internal/probe"
	"github.com/mtdewwolf/sloplex/internal/transcode"
)

// Playback decisions; see the decision endpoint in docs/spec/transcode.md.

// clientCaps is what a client says it can play.
type clientCaps struct {
	protocol           string
	directPlay, hdr    bool
	containers, videos map[string]bool
	audios             map[string]bool
	maxChannels        int
}

func codecSet(v, def string) map[string]bool {
	if v == "" {
		v = def
	}
	out := map[string]bool{}
	for _, c := range strings.Split(v, ",") {
		if c = strings.ToLower(strings.TrimSpace(c)); c != "" {
			out[c] = true
		}
	}
	return out
}

func parseCaps(r *http.Request) (clientCaps, error) {
	q := r.URL.Query()
	c := clientCaps{
		protocol: q.Get("protocol"), directPlay: true, maxChannels: 2,
		containers: codecSet(q.Get("containers"), "mp4"),
		videos:     codecSet(q.Get("videoCodecs"), "h264"),
		audios:     codecSet(q.Get("audioCodecs"), "aac,mp3"),
	}
	switch c.protocol {
	case "":
		c.protocol = "hls"
	case "hls", "http", "webm":
	default:
		return c, fmt.Errorf("protocol must be hls, http or webm")
	}
	flag := func(name string, def bool) (bool, error) {
		switch q.Get(name) {
		case "":
			return def, nil
		case "0":
			return false, nil
		case "1":
			return true, nil
		}
		return false, fmt.Errorf("%s must be 0 or 1", name)
	}
	var err error
	if c.directPlay, err = flag("directPlay", true); err != nil {
		return c, err
	}
	if c.hdr, err = flag("hdr", false); err != nil {
		return c, err
	}
	if v := q.Get("maxAudioChannels"); v != "" {
		if c.maxChannels, err = strconv.Atoi(v); err != nil || c.maxChannels < 1 {
			return c, fmt.Errorf("maxAudioChannels must be a positive number")
		}
	}
	return c, nil
}

// directPlayProblems lists why a part cannot be played as it is.
func directPlayProblems(c clientCaps, part library.Part, req transcode.Request) []string {
	src := req.Source
	var out []string
	add := func(format string, args ...any) { out = append(out, fmt.Sprintf(format, args...)) }
	if !c.directPlay {
		add("the client asked for no direct play")
	}
	if container := strings.ToLower(part.Container); !c.containers[container] {
		add("the container %s is not supported", cmp.Or(container, "(unknown)"))
	}
	if src.VideoCodec != "" && !c.videos[strings.ToLower(src.VideoCodec)] {
		add("the video codec %s is not supported", src.VideoCodec)
	}
	if src.AudioCodec != "" {
		if !c.audios[strings.ToLower(src.AudioCodec)] {
			add("the audio codec %s is not supported", src.AudioCodec)
		}
		if src.AudioChannels > c.maxChannels {
			add("the audio has %d channels, more than %d", src.AudioChannels, c.maxChannels)
		}
	}
	if (req.MaxWidth > 0 && src.Width > req.MaxWidth) || (req.MaxHeight > 0 && src.Height > req.MaxHeight) {
		add("the video is larger than %dx%d", req.MaxWidth, req.MaxHeight)
	}
	if req.MaxBitrate > 0 && (src.Bitrate == 0 || src.Bitrate > req.MaxBitrate) {
		add("the bitrate is above %d kbps or unknown", req.MaxBitrate)
	}
	if src.HDR != "" && !c.hdr {
		add("the video is HDR")
	}
	if req.Burn != nil {
		add("a subtitle must be burned in")
	}
	return out
}

func (s *Server) handleTranscodeDecision(w http.ResponseWriter, r *http.Request) {
	it, req, ok := s.transcodeTarget(w, r)
	if !ok {
		return
	}
	c, err := parseCaps(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	q := r.URL.Query()
	mi, _ := strconv.Atoi(q.Get("mediaIndex"))
	pi, _ := strconv.Atoi(q.Get("partIndex"))
	part := it.Media[mi].Parts[pi]
	acct := s.account(r)
	md := s.metadata(it, acct)
	media := md.Media[mi]
	media.Part = []Part{media.Part[pi]}
	media.Selected = true
	md.Media = nil

	mc := MediaContainer{Size: 1}
	problems := directPlayProblems(c, part, req)
	if len(problems) == 0 {
		mc.GeneralDecisionCode, mc.GeneralDecisionText = 1000, "Direct play OK."
		mc.DirectPlayDecisionCode, mc.DirectPlayDecisionText = 1000, "Direct play OK."
		media.Part[0].Decision = "directplay"
		md.Media = []Media{media}
		mc.Metadata = []Metadata{md}
		writeContainer(w, r, mc)
		return
	}
	mc.DirectPlayDecisionCode = 3000
	mc.DirectPlayDecisionText = "Direct play not available: " + strings.Join(problems, "; ")

	req.WebM = c.protocol == "webm"
	p := transcode.Decide(req)
	outVideo, outAudio, container := "h264", "aac", map[string]string{"hls": "mpegts", "http": "mp4", "webm": "webm"}[c.protocol]
	if req.WebM {
		outVideo, outAudio = "vp9", "opus"
	}
	if p.CopyAudio {
		outAudio = strings.ToLower(req.Source.AudioCodec)
	}
	var why string
	switch {
	case !s.tc.Available():
		why = "ffmpeg is not installed"
	case !c.videos[outVideo]:
		why = "the client does not decode " + outVideo
	case p.HasAudio && !c.audios[outAudio]:
		why = "the client does not decode " + outAudio
	}
	if why != "" {
		mc.GeneralDecisionCode, mc.GeneralDecisionText = 2000, "Neither direct play nor conversion is available."
		mc.TranscodeDecisionCode, mc.TranscodeDecisionText = 4000, "Conversion not available: "+why
		mc.Metadata = []Metadata{md}
		writeContainer(w, r, mc)
		return
	}
	mc.GeneralDecisionCode, mc.GeneralDecisionText = 1001, "Direct play not available; Conversion OK."
	mc.TranscodeDecisionCode, mc.TranscodeDecisionText = 1001, "Conversion OK."

	decision := func(copied bool) string {
		if copied {
			return "copy"
		}
		return "transcode"
	}
	media.Protocol, media.Container, media.VideoCodec, media.AudioCodec = c.protocol, container, outVideo, ""
	media.Width, media.Height, media.Bitrate = p.Width, p.Height, p.Bandwidth/1000
	media.VideoResolution = videoResolution(p.Width, p.Height)
	if p.HasAudio {
		media.AudioCodec = outAudio
		if !p.CopyAudio {
			media.AudioChannels = min(max(req.Source.AudioChannels, 1), 2)
		}
	}
	pt := &media.Part[0]
	pt.Key, pt.Container, pt.Decision = "", container, decision(p.CopyVideo)
	audio, _ := s.selected(acct, part)
	if v := q.Get("audioStreamID"); v != "" {
		id, _ := strconv.Atoi(v)
		audio, _ = findStream(part, id, probe.StreamAudio)
	}
	sub := s.subtitleChoice(r, part)
	videoDone := false
	for _, e := range s.streamElems(acct, part) {
		switch {
		case e.StreamType == streamTypes[probe.StreamVideo] && !videoDone:
			videoDone = true
			e.Decision = decision(p.CopyVideo)
		case e.StreamType == streamTypes[probe.StreamAudio] && audio != nil && e.ID == audio.ID && p.HasAudio:
			e.Decision = decision(p.CopyAudio)
		case e.StreamType == streamTypes[probe.StreamSubtitle] && req.Burn != nil && sub != nil && e.ID == sub.ID:
			e.Decision = "burn"
		default:
			continue
		}
		pt.Stream = append(pt.Stream, e)
	}
	md.Media = []Media{media}
	mc.Metadata = []Metadata{md}
	writeContainer(w, r, mc)
}
