package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.json")
	if err := os.WriteFile(path, []byte(`{"friendlyName":"den","libraries":[{"title":"Movies","type":"movie","paths":["/m"]}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.FriendlyName != "den" || cfg.Listen != ":32400" || !cfg.Discovery || len(cfg.Libraries) != 1 {
		t.Errorf("cfg = %+v", cfg)
	}
	if p, _ := cfg.Port(); p != 32400 {
		t.Errorf("port = %d", p)
	}
}

func TestValidate(t *testing.T) {
	lib := Library{Title: "Movies", Type: "movie", Paths: []string{"/m"}}
	for name, tc := range map[string]struct {
		mut  func(*Config)
		want string
	}{
		"bad type":    {func(c *Config) { c.Libraries = []Library{{Title: "M", Type: "podcast", Paths: []string{"/m"}}} }, "unsupported type"},
		"no paths":    {func(c *Config) { c.Libraries = []Library{{Title: "M", Type: "movie"}} }, "no paths"},
		"duplicate":   {func(c *Config) { c.Libraries = []Library{lib, lib} }, "duplicate"},
		"bad listen":  {func(c *Config) { c.Listen = "32400" }, "listen"},
		"bad network": {func(c *Config) { c.AllowedNetworks = []string{"10.0.0.0"} }, "allowedNetworks"},
	} {
		cfg := Default()
		tc.mut(&cfg)
		err := cfg.Validate()
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want %q", name, err, tc.want)
		}
	}
}
