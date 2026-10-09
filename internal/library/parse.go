package library

import (
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

var (
	// yearRE matches a plausible release year delimited by separators.
	yearRE = regexp.MustCompile(`(?:^|[\s(\[])((?:19|20)\d{2})(?:[\s)\]]|$)`)
	// junkRE matches the first release-tag token; everything after it is noise.
	junkRE  = regexp.MustCompile(`(?i)(?:^|\s)(?:2160p|1080p|1080i|720p|576p|480p|4k|uhd|bluray|blu-ray|bdrip|brrip|webrip|web-dl|webdl|web|hdtv|dvdrip|dvd|remux|x264|x265|h264|h265|hevc|xvid|hdr|10bit)(?:\s|$)`)
	spaceRE = regexp.MustCompile(`\s+`)
)

// ParseMovie derives a title and year from a movie file path. It looks at
// the file name first and falls back to the parent directory when the file
// name carries no year.
func ParseMovie(path string) (title string, year int) {
	base := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	title, year = parseName(base)
	if year == 0 {
		if dt, dy := parseName(filepath.Base(filepath.Dir(path))); dy != 0 && dt != "" {
			return dt, dy
		}
	}
	if title == "" {
		title = strings.TrimSpace(base)
	}
	return title, year
}

// normalize turns dot- or underscore-separated release names into spaced
// text. Dots are kept when the name already contains spaces ("Mr. Smith").
func normalize(s string) string {
	if !strings.Contains(s, " ") {
		s = strings.ReplaceAll(s, ".", " ")
	}
	s = strings.ReplaceAll(s, "_", " ")
	return spaceRE.ReplaceAllString(strings.TrimSpace(s), " ")
}

func parseName(s string) (string, int) {
	s = normalize(s)

	// Take the last year that is not at the very start, so that titles like
	// "2001 A Space Odyssey (1968)" and "1917 (2019)" parse correctly.
	year := 0
	cut := len(s)
	for _, m := range yearRE.FindAllStringSubmatchIndex(s, -1) {
		if m[2] == 0 {
			continue
		}
		year, _ = strconv.Atoi(s[m[2]:m[3]])
		cut = m[0]
	}
	if year == 0 {
		if loc := junkRE.FindStringIndex(s); loc != nil {
			cut = loc[0]
		}
	}
	return strings.TrimRight(strings.TrimSpace(s[:cut]), " -([{"), year
}

// SortTitle returns the key used to sort titles: lower case, without one
// leading English article.
func SortTitle(title string) string {
	t := strings.ToLower(strings.TrimSpace(title))
	for _, a := range []string{"the ", "a ", "an "} {
		if rest, ok := strings.CutPrefix(t, a); ok && rest != "" {
			return rest
		}
	}
	return t
}

var (
	sampleRE       = regexp.MustCompile(`(?i)(?:^|[\s._-])sample(?:[\s._-]|$)`)
	extrasSuffixRE = regexp.MustCompile(`(?i)-(?:trailer|featurette|behindthescenes|deleted|interview|scene|short|other)$`)
	extrasDirs     = map[string]bool{
		"extras": true, "featurettes": true, "trailers": true, "behind the scenes": true,
		"deleted scenes": true, "interviews": true, "scenes": true, "shorts": true,
		"other": true, "samples": true,
	}
)

// skipFile reports whether a video file is a sample or a local extra.
func skipFile(name string) bool {
	base := strings.TrimSuffix(name, filepath.Ext(name))
	return sampleRE.MatchString(base) || extrasSuffixRE.MatchString(base)
}

// skipDir reports whether a directory holds extras rather than main features.
func skipDir(name string) bool {
	return strings.HasPrefix(name, ".") || extrasDirs[strings.ToLower(name)]
}
