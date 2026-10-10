package search

import "testing"

func TestFold(t *testing.T) {
	for in, want := range map[string]string{
		"Amélie":                  "amelie",
		"  The Matrix: Reloaded!": "the matrix reloaded",
		"Straße":                  "strasse",
		"WALL·E":                  "wall e",
		"Spider-Man (2002)":       "spider man 2002",
		"Ærøskøbing":              "aeroskobing",
		"東京物語":                    "東京物語",
		"!!!":                     "",
	} {
		if got := Fold(in); got != want {
			t.Errorf("Fold(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParse(t *testing.T) {
	if _, ok := Parse("  ?? "); ok {
		t.Error("punctuation-only query accepted")
	}
	long := make([]byte, MaxQueryLen+1)
	for i := range long {
		long[i] = 'a'
	}
	if _, ok := Parse(string(long)); ok {
		t.Error("over-long query accepted")
	}
}

func TestScore(t *testing.T) {
	tests := []struct {
		query, title, context string
		year, want            int
	}{
		{"the matrix", "The Matrix", "", 1999, 100},
		{"matrix", "The Matrix", "", 1999, 100},
		{"MATRIX", "the matrix", "", 0, 100},
		{"matr", "The Matrix Reloaded", "", 2003, 80},
		{"reloaded matrix", "The Matrix Reloaded", "", 2003, 60},
		{"matrix 1999", "The Matrix", "", 1999, 60},
		{"matrix 2005", "The Matrix", "", 1999, 0},
		{"amelie", "Amélie", "", 2001, 100},
		{"lost pilot", "Pilot", "Lost Season 1", 0, 40},
		{"pilot", "Pilot", "Lost", 0, 100},
		{"atrix", "The Matrix", "", 0, 0},
		{"lost", "Pilot", "", 0, 0},
	}
	for _, tt := range tests {
		q, ok := Parse(tt.query)
		if !ok {
			t.Fatalf("Parse(%q) failed", tt.query)
		}
		if got := q.Score(tt.title, tt.context, tt.year); got != tt.want {
			t.Errorf("Score(%q, %q, %q, %d) = %d, want %d", tt.query, tt.title, tt.context, tt.year, got, tt.want)
		}
	}
}
