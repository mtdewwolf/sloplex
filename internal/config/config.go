// Package config loads the Sloplex server configuration file.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"strconv"
)

// Library describes one library section to serve.
type Library struct {
	Title string   `json:"title"`
	Type  string   `json:"type"`
	Paths []string `json:"paths"`
}

// Config is the on-disk server configuration. Fields omitted from the file
// keep the values from Default.
type Config struct {
	FriendlyName string `json:"friendlyName"`
	Listen       string `json:"listen"`
	DataDir      string `json:"dataDir"`
	Discovery    bool   `json:"discovery"`
	// Tokens are accepted in addition to the server's generated token.
	Tokens []string `json:"tokens"`
	// AllowedNetworks are CIDRs whose clients skip token authentication.
	AllowedNetworks []string `json:"allowedNetworks"`
	// FFprobe is the ffprobe binary: "" looks it up on PATH, "-" disables probing.
	FFprobe string `json:"ffprobe"`
	// FFmpeg is the ffmpeg binary used for thumbnails: "" looks it up on
	// PATH, "-" disables it.
	FFmpeg    string    `json:"ffmpeg"`
	Libraries []Library `json:"libraries"`
}

// SupportedTypes lists the library types the server can scan.
var SupportedTypes = map[string]bool{"movie": true, "show": true}

// Default returns the configuration used when no file is given.
func Default() Config {
	name, err := os.Hostname()
	if err != nil || name == "" {
		name = "sloplex"
	}
	return Config{
		FriendlyName: name,
		Listen:       ":32400",
		DataDir:      "data",
		Discovery:    true,
	}
}

// Load reads the config file at path on top of Default. An empty path
// returns the defaults.
func Load(path string) (Config, error) {
	cfg := Default()
	if path != "" {
		b, err := os.ReadFile(path)
		if err != nil {
			return Config{}, err
		}
		if err := json.Unmarshal(b, &cfg); err != nil {
			return Config{}, fmt.Errorf("parse %s: %w", path, err)
		}
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// Validate reports the first problem found in the configuration.
func (c Config) Validate() error {
	if c.FriendlyName == "" {
		return errors.New("friendlyName must not be empty")
	}
	if c.DataDir == "" {
		return errors.New("dataDir must not be empty")
	}
	if _, err := c.Port(); err != nil {
		return err
	}
	if _, err := c.Networks(); err != nil {
		return err
	}
	seen := map[string]bool{}
	for i, lib := range c.Libraries {
		if lib.Title == "" {
			return fmt.Errorf("libraries[%d]: title must not be empty", i)
		}
		if !SupportedTypes[lib.Type] {
			return fmt.Errorf("libraries[%d] %q: unsupported type %q", i, lib.Title, lib.Type)
		}
		if len(lib.Paths) == 0 {
			return fmt.Errorf("libraries[%d] %q: no paths", i, lib.Title)
		}
		key := lib.Type + "\x00" + lib.Title
		if seen[key] {
			return fmt.Errorf("libraries[%d]: duplicate %s library %q", i, lib.Type, lib.Title)
		}
		seen[key] = true
	}
	return nil
}

// Port returns the TCP port from Listen.
func (c Config) Port() (int, error) {
	_, p, err := net.SplitHostPort(c.Listen)
	if err != nil {
		return 0, fmt.Errorf("listen %q: %w", c.Listen, err)
	}
	port, err := strconv.Atoi(p)
	if err != nil || port < 1 || port > 65535 {
		return 0, fmt.Errorf("listen %q: invalid port", c.Listen)
	}
	return port, nil
}

// Networks parses AllowedNetworks.
func (c Config) Networks() ([]netip.Prefix, error) {
	out := make([]netip.Prefix, 0, len(c.AllowedNetworks))
	for _, s := range c.AllowedNetworks {
		p, err := netip.ParsePrefix(s)
		if err != nil {
			return nil, fmt.Errorf("allowedNetworks: %w", err)
		}
		out = append(out, p.Masked())
	}
	return out, nil
}
