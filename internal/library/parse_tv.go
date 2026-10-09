package library

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// Episode is what a TV file path says about itself.
type Episode struct {
	Show     string
	ShowYear int
	Season   int
	Episode  int
	Title    string
}

var (
	// seRE matches S01E02, S01 E02, S01E01E02 and S01E01-E02.
	seRE = regexp.MustCompile(`(?i)(?:^|[^a-z0-9])s(\d{1,3})[ .\-]?e(\d{1,4})(?:-?e\d{1,4})*`)
	// xRE matches 1x02.
	xRE = regexp.MustCompile(`(?i)(?:^|[^a-z0-9])(\d{1,2})x(\d{2,3})(?:[^a-z0-9]|$)`)
	// epOnlyRE matches E02, Ep02 and Episode 2, used inside season folders.
	epOnlyRE = regexp.MustCompile(`(?i)(?:^|[^a-z0-9])(?:e|ep|episode)\s?(\d{1,4})(?:[^a-z0-9]|$)`)
	// leadingNumRE matches a leading episode number such as "02 - Title".
	leadingNumRE = regexp.MustCompile(`^(\d{1,3})(?:[ .\-]|$)`)
	seasonDirRE  = regexp.MustCompile(`(?i)^(?:season|series|s)\s*(\d{1,3})$`)
)

// seasonDir reports whether a folder name denotes a season, and which.
func seasonDir(name string) (int, bool) {
	name = strings.TrimSpace(normalize(name))
	if strings.EqualFold(name, "specials") {
		return 0, true
	}
	if m := seasonDirRE.FindStringSubmatch(name); m != nil {
		n, _ := strconv.Atoi(m[1])
		return n, true
	}
	return 0, false
}

// ParseEpisode derives show, season and episode from a file path below
// root. ok is false when no episode number can be found. See the "Show
// sections" rules in docs/spec/http-api.md.
func ParseEpisode(root, path string) (ep Episode, ok bool) {
	var dirs []string
	if rel, err := filepath.Rel(root, path); err == nil && !strings.HasPrefix(rel, "..") {
		if d := filepath.Dir(rel); d != "." {
			dirs = strings.Split(filepath.ToSlash(d), "/")
		}
	}
	base := normalize(strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)))

	var prefix, rest string
	atoi := func(s string) int { n, _ := strconv.Atoi(s); return n }
	if m := seRE.FindStringSubmatchIndex(base); m != nil {
		ep.Season, ep.Episode = atoi(base[m[2]:m[3]]), atoi(base[m[4]:m[5]])
		prefix, rest, ok = base[:m[0]], base[m[1]:], true
	} else if m := xRE.FindStringSubmatchIndex(base); m != nil {
		ep.Season, ep.Episode = atoi(base[m[2]:m[3]]), atoi(base[m[4]:m[5]])
		prefix, rest, ok = base[:m[0]], base[m[1]:], true
	} else if len(dirs) > 0 {
		if season, isSeason := seasonDir(dirs[len(dirs)-1]); isSeason {
			if m := epOnlyRE.FindStringSubmatchIndex(base); m != nil {
				ep.Season, ep.Episode = season, atoi(base[m[2]:m[3]])
				prefix, rest, ok = base[:m[0]], base[m[1]:], true
			} else if m := leadingNumRE.FindStringSubmatchIndex(base); m != nil {
				ep.Season, ep.Episode = season, atoi(base[m[2]:m[3]])
				rest, ok = base[m[1]:], true
			}
		}
	}
	if !ok {
		return Episode{}, false
	}

	if len(dirs) > 0 {
		if _, isSeason := seasonDir(dirs[0]); !isSeason {
			ep.Show, ep.ShowYear = parseName(dirs[0])
		}
	}
	if ep.Show == "" {
		ep.Show, ep.ShowYear = parseName(prefix)
	}
	if ep.Show == "" {
		return Episode{}, false
	}
	ep.Title = episodeTitle(rest)
	return ep, true
}

func episodeTitle(s string) string {
	s = strings.TrimLeft(s, " -.")
	if loc := junkRE.FindStringIndex(s); loc != nil {
		s = s[:loc[0]]
	}
	return strings.TrimRight(strings.TrimSpace(s), " -([{")
}

// SeasonTitle is the display title of a season.
func SeasonTitle(index int) string {
	if index == 0 {
		return "Specials"
	}
	return fmt.Sprintf("Season %d", index)
}
