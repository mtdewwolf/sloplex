package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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
	if cfg.FriendlyName != "den" || cfg.Listen != ":32400" || !cfg.Discovery || len(cfg.Libraries) != 1 ||
		cfg.X264Preset != "auto" || !cfg.TranscodeThrottle {
		t.Errorf("cfg = %+v", cfg)
	}
	if p, _ := cfg.Port(); p != 32400 {
		t.Errorf("port = %d", p)
	}
	if d, err := cfg.ScanEvery(); !cfg.Watch || d != time.Hour || err != nil {
		t.Errorf("watch = %v, scan interval = %v %v; want on and 1h by default", cfg.Watch, d, err)
	}
	for in, want := range map[string]time.Duration{"0": 0, "": 0, "30m": 30 * time.Minute} {
		if d, err := (Config{ScanInterval: in}).ScanEvery(); d != want || err != nil {
			t.Errorf("ScanEvery(%q) = %v %v", in, d, err)
		}
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
		"bad hwaccel": {func(c *Config) { c.Hwaccel = "cuda" }, "hwaccel"},
		"bad scan":    {func(c *Config) { c.ScanInterval = "hourly" }, "scanInterval"},
		"short scan":  {func(c *Config) { c.ScanInterval = "10s" }, "scanInterval"},
		"bad preset":  {func(c *Config) { c.X264Preset = "placebo" }, "x264Preset"},
	} {
		cfg := Default()
		tc.mut(&cfg)
		err := cfg.Validate()
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want %q", name, err, tc.want)
		}
	}
}
