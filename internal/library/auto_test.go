package library

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/mtdewwolf/sloplex/internal/probe"
)

func newAutoFixture(t *testing.T, pr probe.Prober) (*Store, *Scanner, int, string) {
	t.Helper()
	root := t.TempDir()
	s, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SyncSections([]SectionDef{{Title: "Movies", Type: TypeMovie, Paths: []string{root}}}, t0); err != nil {
		t.Fatal(err)
	}
	return s, NewScanner(s, pr, nil), s.Sections()[0].ID, root
}

func titles(s *Store, sec int) string {
	var out []string
	for _, it := range s.Items(sec, TypeMovie) {
		out = append(out, it.Title)
	}
	sort.Strings(out)
	return strings.Join(out, ",")
}

func waitTitles(t *testing.T, s *Store, sec int, want string, within time.Duration) {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if titles(s, sec) == want {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("titles = %q, want %q", titles(s, sec), want)
}

func TestAutoWatch(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("watching needs inotify")
	}
	s, sc, sec, root := newAutoFixture(t, &countingProber{})
	ctx, cancel := context.WithCancel(context.Background())
	defer func() { cancel(); sc.Wait() }()
	sc.Auto(ctx, AutoOptions{Watch: true, Quiet: 150 * time.Millisecond, MaxDelay: time.Second})

	writeFile(t, filepath.Join(root, "Heat (1995)", "Heat (1995).mkv"), "x")
	waitTitles(t, s, sec, "Heat", 3*time.Second)

	// Changes keep postponing the scan until they settle, but no longer
	// than MaxDelay.
	start := time.Now()
	for i := 0; time.Since(start) < 1500*time.Millisecond; i++ {
		writeFile(t, filepath.Join(root, "busy.part"), strings.Repeat("x", i))
		time.Sleep(50 * time.Millisecond)
		if i == 2 {
			writeFile(t, filepath.Join(root, "Alien (1979).mkv"), "x")
		}
	}
	if got := titles(s, sec); got != "Alien,Heat" {
		t.Errorf("after MaxDelay of constant changes, titles = %q", got)
	}

	if err := os.RemoveAll(filepath.Join(root, "Heat (1995)")); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		missing := false
		for _, it := range s.Items(sec, TypeMovie) {
			if it.Title == "Heat" && it.Unavailable {
				missing = true
			}
		}
		if missing {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("watch did not mark Heat unavailable")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if _, err := s.EmptyTrash(sec, time.Now()); err != nil {
		t.Fatal(err)
	}
	waitTitles(t, s, sec, "Alien", 3*time.Second)
}

func TestAutoInterval(t *testing.T) {
	s, sc, sec, root := newAutoFixture(t, &countingProber{})
	ctx, cancel := context.WithCancel(context.Background())
	defer func() { cancel(); sc.Wait() }()
	sc.Auto(ctx, AutoOptions{Interval: 200 * time.Millisecond})
	writeFile(t, filepath.Join(root, "Heat (1995).mkv"), "x")
	waitTitles(t, s, sec, "Heat", 2*time.Second)
}

// blockingProber holds the probe of one file until released.
type blockingProber struct {
	countingProber
	hold    string
	entered chan struct{}
	release chan struct{}
}

func (b *blockingProber) Probe(ctx context.Context, path string) (probe.Info, error) {
	if filepath.Base(path) == b.hold {
		b.hold = ""
		close(b.entered)
		<-b.release
	}
	return b.countingProber.Probe(ctx, path)
}

func TestStartQueuesRescan(t *testing.T) {
	pr := &blockingProber{hold: "Alien (1979).mkv", entered: make(chan struct{}), release: make(chan struct{})}
	s, sc, sec, root := newAutoFixture(t, pr)
	writeFile(t, filepath.Join(root, "Alien (1979).mkv"), "x")
	ctx := context.Background()
	sc.Start(ctx, sec)
	<-pr.entered
	// The running scan has already listed the folder; a file added now
	// needs the queued scan. Several requests merge into one.
	writeFile(t, filepath.Join(root, "Heat (1995).mkv"), "x")
	sc.Start(ctx, sec)
	sc.Start(ctx, sec)
	if !sc.Refreshing(sec) {
		t.Error("not refreshing during the scan")
	}
	close(pr.release)
	sc.Wait()
	if got := titles(s, sec); got != "Alien,Heat" {
		t.Errorf("titles = %q, want the file added during the scan", got)
	}
	if sc.Refreshing(sec) {
		t.Error("still refreshing after Wait")
	}
	pr.mu.Lock()
	defer pr.mu.Unlock()
	if n := pr.calls[filepath.Join(root, "Heat (1995).mkv")]; n != 1 {
		t.Errorf("Heat probed %d times; the queued requests should merge into one scan", n)
	}
}
