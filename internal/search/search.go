// Package search folds text and scores titles against queries. See
// docs/spec/search.md.
package search

import (
	"strconv"
	"strings"
	"unicode"
)

// MaxQueryLen is the longest accepted query, in bytes.
const MaxQueryLen = 200

var folds = map[rune]string{
	'à': "a", 'á': "a", 'â': "a", 'ã': "a", 'ä': "a", 'å': "a", 'ā': "a",
	'ç': "c", 'č': "c", 'ć': "c",
	'è': "e", 'é': "e", 'ê': "e", 'ë': "e", 'ē': "e", 'ě': "e",
	'ì': "i", 'í': "i", 'î': "i", 'ï': "i", 'ī': "i",
	'ñ': "n", 'ń': "n", 'ň': "n",
	'ò': "o", 'ó': "o", 'ô': "o", 'õ': "o", 'ö': "o", 'ø': "o", 'ō': "o",
	'ù': "u", 'ú': "u", 'û': "u", 'ü': "u", 'ū': "u", 'ů': "u",
	'ý': "y", 'ÿ': "y",
	'š': "s", 'ś': "s", 'ž': "z", 'ź': "z", 'ż': "z", 'ř': "r", 'ł': "l", 'đ': "d",
	'ß': "ss", 'æ': "ae", 'œ': "oe", 'þ': "th",
}

// Fold lower-cases s, strips common Latin diacritics, and turns everything
// that is not a letter or digit into single spaces.
func Fold(s string) string {
	var b strings.Builder
	space := true // suppress leading spaces
	for _, r := range strings.ToLower(s) {
		if f, ok := folds[r]; ok {
			b.WriteString(f)
			space = false
			continue
		}
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			space = false
			continue
		}
		if !space {
			b.WriteByte(' ')
			space = true
		}
	}
	return strings.TrimRight(b.String(), " ")
}

// Query is a parsed search query.
type Query struct {
	folded string
	tokens []string
}

// Parse folds a query. ok is false if it is too long or folds to nothing.
func Parse(q string) (Query, bool) {
	if len(q) > MaxQueryLen {
		return Query{}, false
	}
	f := Fold(q)
	if f == "" {
		return Query{}, false
	}
	return Query{folded: f, tokens: strings.Fields(f)}, true
}

var articles = []string{"the ", "a ", "an "}

func stripArticle(folded string) string {
	for _, a := range articles {
		if rest, ok := strings.CutPrefix(folded, a); ok && rest != "" {
			return rest
		}
	}
	return folded
}

// matchesAll reports whether every token is a prefix of some word, or a
// digits-only token equal to year.
func (q Query) matchesAll(words []string, year int) bool {
	y := ""
	if year > 0 {
		y = strconv.Itoa(year)
	}
	for _, tok := range q.tokens {
		found := tok == y
		for _, w := range words {
			if found {
				break
			}
			found = strings.HasPrefix(w, tok)
		}
		if !found {
			return false
		}
	}
	return true
}

// Score rates how well an item matches; 0 means no match. context is extra
// text such as the show title of an episode.
func (q Query) Score(title, context string, year int) int {
	t := Fold(title)
	bare := stripArticle(t)
	switch {
	case t == q.folded || bare == q.folded:
		return 100
	case strings.HasPrefix(t, q.folded) || strings.HasPrefix(bare, q.folded):
		return 80
	}
	words := strings.Fields(t)
	if q.matchesAll(words, year) {
		return 60
	}
	if context != "" && q.matchesAll(append(words, strings.Fields(Fold(context))...), year) {
		return 40
	}
	return 0
}
