// Package updates checks GitHub for newer Sloplex releases; see
// docs/spec/updates.md.
package updates

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// DefaultURL is the release lookup for the official repository.
const DefaultURL = "https://api.github.com/repos/mtdewwolf/sloplex/releases/latest"

const (
	minInterval = time.Minute // between network checks
	timeout     = 15 * time.Second
)

// Status is the result of the last check.
type Status struct {
	Current     string `json:"current"`
	Comparable  bool   `json:"comparable"`
	Latest      string `json:"latest,omitempty"`
	Available   bool   `json:"available"`
	ReleaseName string `json:"releaseName,omitempty"`
	ReleaseURL  string `json:"releaseUrl,omitempty"`
	PublishedAt int64  `json:"publishedAt,omitempty"`
	CheckedAt   int64  `json:"checkedAt,omitempty"`
	Error       string `json:"error"`
}

// Checker looks up the latest release and remembers the answer.
type Checker struct {
	current string
	url     string
	client  *http.Client
	log     *slog.Logger
	now     func() time.Time

	checking sync.Mutex // serialises network checks
	mu       sync.Mutex
	status   Status
	last     time.Time // last network attempt
}

// New returns a Checker for a server running version current, looking
// releases up at url (DefaultURL when empty).
func New(current, url string, log *slog.Logger) *Checker {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	_, ok := parse(current)
	return &Checker{
		current: current, url: cmp.Or(url, DefaultURL), client: &http.Client{}, log: log, now: time.Now,
		status: Status{Current: current, Comparable: ok},
	}
}

// Status returns the result of the last check.
func (c *Checker) Status() Status {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.status
}

// Check looks up the latest release, unless the last attempt was less than
// a minute ago, and returns the resulting status.
func (c *Checker) Check(ctx context.Context) Status {
	c.checking.Lock()
	defer c.checking.Unlock()
	c.mu.Lock()
	recent := !c.last.IsZero() && c.now().Sub(c.last) < minInterval
	c.mu.Unlock()
	if recent {
		return c.Status()
	}

	rel, err := c.fetch(ctx)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.last = c.now()
	c.status.CheckedAt = c.last.Unix()
	if err != nil {
		c.status.Error = err.Error()
		c.log.Warn("update check failed", "err", err)
		return c.status
	}
	latest := strings.TrimPrefix(rel.Tag, "v")
	c.status.Error = ""
	c.status.Latest, c.status.ReleaseName, c.status.ReleaseURL = latest, rel.Name, rel.URL
	c.status.PublishedAt = rel.Published.Unix()
	c.status.Available = newer(latest, c.current)
	if c.status.Available {
		c.log.Info("a newer release is available", "current", c.current, "latest", latest)
	}
	return c.status
}

// Run checks after first and then every interval until ctx is done.
func (c *Checker) Run(ctx context.Context, first, interval time.Duration) {
	t := time.NewTimer(first)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			c.Check(ctx)
			t.Reset(interval)
		}
	}
}

type release struct {
	Tag       string    `json:"tag_name"`
	Name      string    `json:"name"`
	URL       string    `json:"html_url"`
	Published time.Time `json:"published_at"`
}

func (c *Checker) fetch(ctx context.Context) (release, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url, nil)
	if err != nil {
		return release{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "sloplex/"+c.current)
	resp, err := c.client.Do(req)
	if err != nil {
		return release{}, fmt.Errorf("could not reach GitHub: %w", err)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return release{}, errors.New("no release has been published")
	case resp.StatusCode != http.StatusOK:
		return release{}, fmt.Errorf("GitHub answered %s", resp.Status)
	}
	var rel release
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&rel); err != nil {
		return release{}, fmt.Errorf("reading the release: %w", err)
	}
	if _, ok := parse(strings.TrimPrefix(rel.Tag, "v")); !ok {
		return release{}, fmt.Errorf("the latest release has an unexpected tag %q", rel.Tag)
	}
	return rel, nil
}

var versionRE = regexp.MustCompile(`^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(?:-([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?$`)

type version struct {
	n   [3]int
	pre []string
}

func parse(s string) (version, bool) {
	m := versionRE.FindStringSubmatch(s)
	if m == nil {
		return version{}, false
	}
	var v version
	for i := range 3 {
		n, err := strconv.Atoi(m[i+1])
		if err != nil {
			return version{}, false
		}
		v.n[i] = n
	}
	if m[4] != "" {
		v.pre = strings.Split(m[4], ".")
	}
	return v, true
}

// newer reports whether a is a higher semantic version than b; versions
// that do not parse are never newer, nor older.
func newer(a, b string) bool {
	va, ok1 := parse(a)
	vb, ok2 := parse(b)
	return ok1 && ok2 && compare(va, vb) > 0
}

func compare(a, b version) int {
	for i := range 3 {
		if c := cmp.Compare(a.n[i], b.n[i]); c != 0 {
			return c
		}
	}
	// A version without a prerelease is higher than one with.
	switch {
	case len(a.pre) == 0 && len(b.pre) == 0:
		return 0
	case len(a.pre) == 0:
		return 1
	case len(b.pre) == 0:
		return -1
	}
	for i := 0; i < len(a.pre) && i < len(b.pre); i++ {
		x, errX := strconv.Atoi(a.pre[i])
		y, errY := strconv.Atoi(b.pre[i])
		var c int
		switch {
		case errX == nil && errY == nil:
			c = cmp.Compare(x, y)
		case errX == nil:
			c = -1 // numeric identifiers are lower
		case errY == nil:
			c = 1
		default:
			c = strings.Compare(a.pre[i], b.pre[i])
		}
		if c != 0 {
			return c
		}
	}
	return cmp.Compare(len(a.pre), len(b.pre))
}
