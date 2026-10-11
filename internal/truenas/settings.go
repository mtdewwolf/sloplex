// Package truenas asks a TrueNAS server to pull and redeploy the app Sloplex
// runs as; see "One-click update through TrueNAS" in docs/spec/updates.md.
package truenas

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"

	"github.com/mtdewwolf/sloplex/internal/atomicfile"
)

// Settings say how to reach TrueNAS and which app to update.
type Settings struct {
	URL        string `json:"url"`
	App        string `json:"app"`
	APIKey     string `json:"apiKey,omitempty"`
	CertSHA256 string `json:"certSHA256,omitempty"`
}

// ErrKeyRequired is returned when a change needs the API key sent again.
var ErrKeyRequired = errors.New("enter the API key again: it is only kept while the address and certificate stay the same")

var appRE = regexp.MustCompile(`^[a-z][a-z0-9-]{0,39}$`)

// normalizeURL checks that u is https://host[:port] and returns it without a
// trailing slash.
func normalizeURL(u string) (string, error) {
	p, err := url.Parse(strings.TrimSpace(u))
	if err != nil || p.Host == "" || p.Hostname() == "" {
		return "", errors.New("the TrueNAS address must look like https://192.168.1.10")
	}
	if p.Scheme != "https" {
		return "", errors.New("the TrueNAS address must start with https://; TrueNAS revokes API keys sent over plain HTTP")
	}
	if (p.Path != "" && p.Path != "/") || p.RawQuery != "" || p.Fragment != "" || p.User != nil {
		return "", errors.New("the TrueNAS address must be just https:// and the host, with no path")
	}
	return "https://" + p.Host, nil
}

// normalizeFingerprint returns a SHA-256 fingerprint as 64 lower-case hex
// digits, ignoring colons, spaces and case; "" stays "".
func normalizeFingerprint(f string) (string, error) {
	f = strings.ToLower(strings.NewReplacer(":", "", " ", "").Replace(f))
	if f == "" {
		return "", nil
	}
	if b, err := hex.DecodeString(f); err != nil || len(b) != 32 {
		return "", errors.New("the certificate fingerprint must be 64 hex digits (SHA-256)")
	}
	return f, nil
}

// Apply returns the settings a change asks for. An empty key keeps the
// stored one, but only while the address and fingerprint are unchanged.
func Apply(old Settings, rawURL, app, apiKey, cert string) (Settings, error) {
	u, err := normalizeURL(rawURL)
	if err != nil {
		return Settings{}, err
	}
	if !appRE.MatchString(app) {
		return Settings{}, errors.New("the app name is the name the app was installed with in TrueNAS: lower-case letters, digits and -")
	}
	fp, err := normalizeFingerprint(cert)
	if err != nil {
		return Settings{}, err
	}
	s := Settings{URL: u, App: app, APIKey: strings.TrimSpace(apiKey), CertSHA256: fp}
	if s.APIKey == "" {
		if old.APIKey == "" {
			return Settings{}, errors.New("enter an API key")
		}
		if old.URL != s.URL || old.CertSHA256 != s.CertSHA256 {
			return Settings{}, ErrKeyRequired
		}
		s.APIKey = old.APIKey
	}
	return s, nil
}

// Store keeps the settings in a file only the server can read. With an
// empty path they are kept in memory.
type Store struct {
	path string
	mu   sync.Mutex
	s    *Settings
	// writeFile is an optional per-store writer; nil uses atomicfile.Write.
	writeFile func(string, []byte, os.FileMode) error
}

// Open loads the settings at path, if the file exists.
func Open(path string) (*Store, error) {
	st := &Store{path: path}
	if path == "" {
		return st, nil
	}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return st, nil
	}
	if err != nil {
		return nil, err
	}
	var s Settings
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	st.s = &s
	return st, nil
}

// Get returns the stored settings, if any.
func (st *Store) Get() (Settings, bool) {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.s == nil {
		return Settings{}, false
	}
	return *st.s, true
}

// Set saves s. An atomicfile.CommittedError still publishes the installed
// settings while returning the error to report unconfirmed durability.
func (st *Store) Set(s Settings) error {
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.path != "" {
		writeFile := st.writeFile
		if writeFile == nil {
			writeFile = atomicfile.Write
		}
		err = writeFile(st.path, append(b, '\n'), 0o600)
		var committed *atomicfile.CommittedError
		if err != nil && !errors.As(err, &committed) {
			return err
		}
	}
	st.s = &s
	return err
}

// Delete removes the settings.
func (st *Store) Delete() error {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.path != "" {
		if err := os.Remove(st.path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	st.s = nil
	return nil
}
