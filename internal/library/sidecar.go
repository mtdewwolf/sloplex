package library

import (
	"path/filepath"
	"sort"
	"strings"

	"github.com/mtdewwolf/sloplex/internal/lang"
	"github.com/mtdewwolf/sloplex/internal/probe"
)

// sidecarCodecs maps subtitle file extensions to codec names.
var sidecarCodecs = map[string]string{".srt": "srt", ".vtt": "vtt", ".ass": "ass", ".ssa": "ssa"}

// findSidecars returns the subtitle files next to a video, named
// "<stem>.<tags>.<ext>"; see "Sidecar subtitles" in docs/spec/streams.md.
func findSidecars(dirs dirCache, video string) []Stream {
	dir := filepath.Dir(video)
	st := stem(video)
	var names []string
	for _, name := range dirs.files(dir) {
		names = append(names, name)
	}
	sort.Strings(names)

	var out []Stream
	for _, name := range names {
		if s, ok := parseSidecar(st, name); ok {
			s.File = filepath.Join(dir, name)
			out = append(out, s)
		}
	}
	return out
}

// parseSidecar interprets a file name as a subtitle of the video with the
// given stem.
func parseSidecar(videoStem, name string) (Stream, bool) {
	ext := strings.ToLower(filepath.Ext(name))
	codec, ok := sidecarCodecs[ext]
	if !ok || len(name) <= len(videoStem) || !strings.EqualFold(name[:len(videoStem)], videoStem) {
		return Stream{}, false
	}
	rest := strings.TrimSuffix(name[len(videoStem):], filepath.Ext(name))
	if rest != "" && rest[0] != '.' {
		return Stream{}, false // e.g. "Heat (1995) extended.srt" for "Heat (1995)"
	}
	s := Stream{Stream: probe.Stream{Index: -1, Type: probe.StreamSubtitle, Codec: codec}}
	for _, tok := range strings.Split(strings.Trim(rest, "."), ".") {
		switch t := strings.ToLower(tok); t {
		case "":
		case "forced":
			s.Forced = true
		case "sdh", "cc", "hi":
			s.HearingImpaired = true
		case "default":
			s.Default = true
		default:
			if code, _, ok := lang.Lookup(t); ok && s.Language == "" {
				s.Language = code
			}
		}
	}
	return s, true
}
