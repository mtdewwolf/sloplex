// Package lang normalises language codes and names to ISO 639-2/B. See
// "Languages" in docs/spec/streams.md.
package lang

import "strings"

type language struct {
	code    string // ISO 639-2/B
	name    string
	aliases []string // ISO 639-1, 639-2/T and lower-case English names
}

var languages = []language{
	{"eng", "English", []string{"en", "english"}},
	{"fre", "French", []string{"fr", "fra", "french"}},
	{"ger", "German", []string{"de", "deu", "german"}},
	{"spa", "Spanish", []string{"es", "spanish"}},
	{"ita", "Italian", []string{"it", "italian"}},
	{"por", "Portuguese", []string{"pt", "portuguese"}},
	{"dut", "Dutch", []string{"nl", "nld", "dutch"}},
	{"swe", "Swedish", []string{"sv", "swedish"}},
	{"nor", "Norwegian", []string{"no", "nob", "nno", "nb", "nn", "norwegian"}},
	{"dan", "Danish", []string{"da", "danish"}},
	{"fin", "Finnish", []string{"fi", "finnish"}},
	{"ice", "Icelandic", []string{"is", "isl", "icelandic"}},
	{"pol", "Polish", []string{"pl", "polish"}},
	{"cze", "Czech", []string{"cs", "ces", "czech"}},
	{"slo", "Slovak", []string{"sk", "slk", "slovak"}},
	{"slv", "Slovenian", []string{"sl", "slovenian"}},
	{"hun", "Hungarian", []string{"hu", "hungarian"}},
	{"rum", "Romanian", []string{"ro", "ron", "romanian"}},
	{"bul", "Bulgarian", []string{"bg", "bulgarian"}},
	{"hrv", "Croatian", []string{"hr", "croatian"}},
	{"srp", "Serbian", []string{"sr", "serbian"}},
	{"ukr", "Ukrainian", []string{"uk", "ukrainian"}},
	{"rus", "Russian", []string{"ru", "russian"}},
	{"gre", "Greek", []string{"el", "ell", "greek"}},
	{"tur", "Turkish", []string{"tr", "turkish"}},
	{"ara", "Arabic", []string{"ar", "arabic"}},
	{"heb", "Hebrew", []string{"he", "hebrew"}},
	{"per", "Persian", []string{"fa", "fas", "persian", "farsi"}},
	{"hin", "Hindi", []string{"hindi"}}, // "hi" is reserved for hearing impaired in file names
	{"ben", "Bengali", []string{"bn", "bengali"}},
	{"tam", "Tamil", []string{"ta", "tamil"}},
	{"tel", "Telugu", []string{"te", "telugu"}},
	{"tha", "Thai", []string{"th", "thai"}},
	{"vie", "Vietnamese", []string{"vi", "vietnamese"}},
	{"ind", "Indonesian", []string{"id", "indonesian"}},
	{"may", "Malay", []string{"ms", "msa", "malay"}},
	{"chi", "Chinese", []string{"zh", "zho", "chinese"}},
	{"jpn", "Japanese", []string{"ja", "japanese"}},
	{"kor", "Korean", []string{"ko", "korean"}},
	{"cat", "Catalan", []string{"ca", "catalan"}},
}

var byKey = func() map[string]language {
	m := map[string]language{}
	for _, l := range languages {
		m[l.code] = l
		for _, a := range l.aliases {
			m[a] = l
		}
	}
	return m
}()

// Lookup resolves a language code or English name (any case) to its ISO
// 639-2/B code and English name. ok is false if it is unknown.
func Lookup(s string) (code, name string, ok bool) {
	l, ok := byKey[strings.ToLower(strings.TrimSpace(s))]
	return l.code, l.name, ok
}

// Tag returns the shortest language tag for an ISO 639-2/B code: its ISO
// 639-1 code when it has one (as HLS and BCP 47 prefer), else the code.
func Tag(code string) string {
	if l, ok := byKey[strings.ToLower(code)]; ok {
		for _, a := range l.aliases {
			if len(a) == 2 {
				return a
			}
		}
		return l.code
	}
	return strings.ToLower(code)
}

// Normalize returns the ISO 639-2/B code and name for s. Unknown
// three-letter codes are kept as the code with no name; anything else
// yields empty strings.
func Normalize(s string) (code, name string) {
	if c, n, ok := Lookup(s); ok {
		return c, n
	}
	s = strings.ToLower(strings.TrimSpace(s))
	if len(s) == 3 && strings.Trim(s, "abcdefghijklmnopqrstuvwxyz") == "" {
		return s, ""
	}
	return "", ""
}
