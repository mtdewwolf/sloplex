package lang

import "testing"

func TestNormalize(t *testing.T) {
	for in, want := range map[string][2]string{
		"en": {"eng", "English"}, "ENG": {"eng", "English"}, "fra": {"fre", "French"}, "fre": {"fre", "French"},
		"de": {"ger", "German"}, "deu": {"ger", "German"}, "French": {"fre", "French"}, "pt": {"por", "Portuguese"},
		"hindi": {"hin", "Hindi"}, "xyz": {"xyz", ""}, "hi": {"", ""}, "": {"", ""}, "e1g": {"", ""},
	} {
		if c, n := Normalize(in); c != want[0] || n != want[1] {
			t.Errorf("Normalize(%q) = %q, %q; want %q, %q", in, c, n, want[0], want[1])
		}
	}
}
