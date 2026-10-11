package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadLibraryIdentity(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"libraries":[{"id":"movies-main","title":"Movies","type":"movie","paths":["/m"]}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil || cfg.Libraries[0].ID != "movies-main" {
		t.Fatalf("Load = %+v, %v", cfg, err)
	}
}

func TestValidateLibraryIdentities(t *testing.T) {
	for name, tc := range map[string]struct {
		libs []Library
		bad  bool
	}{
		"duplicate ids across types": {[]Library{
			{ID: "same", Title: "Movies", Type: "movie", Paths: []string{"/m"}},
			{ID: "same", Title: "TV", Type: "show", Paths: []string{"/tv"}},
		}, true},
		"same names distinct ids": {[]Library{
			{ID: "a", Title: "Movies", Type: "movie", Paths: []string{"/a"}},
			{ID: "b", Title: "Movies", Type: "movie", Paths: []string{"/b"}},
		}, false},
		"ambiguous legacy name": {[]Library{
			{ID: "a", Title: "Movies", Type: "movie", Paths: []string{"/a"}},
			{Title: "Movies", Type: "movie", Paths: []string{"/b"}},
		}, true},
		"case sensitive ids": {[]Library{
			{ID: "a", Title: "Movies", Type: "movie", Paths: []string{"/a"}},
			{ID: "A", Title: "Movies", Type: "movie", Paths: []string{"/b"}},
		}, false},
	} {
		t.Run(name, func(t *testing.T) {
			cfg := Default()
			cfg.Libraries = tc.libs
			if err := cfg.Validate(); (err != nil) != tc.bad {
				t.Errorf("Validate = %v, want failure %v", err, tc.bad)
			}
		})
	}
	for _, id := range []string{"", "movies", "Movies.4k-main_1", strings.Repeat("x", 64)} {
		cfg := Default()
		cfg.Libraries = []Library{{ID: id, Title: "Movies", Type: "movie", Paths: []string{"/m"}}}
		if err := cfg.Validate(); err != nil {
			t.Errorf("valid identity %q: %v", id, err)
		}
	}
	for _, id := range []string{"bad id", " leading", "trailing ", "/movies", "m:1", "é", "-movies", strings.Repeat("x", 65)} {
		cfg := Default()
		cfg.Libraries = []Library{{ID: id, Title: "Movies", Type: "movie", Paths: []string{"/m"}}}
		if err := cfg.Validate(); err == nil {
			t.Errorf("invalid identity %q accepted", id)
		}
	}
}
