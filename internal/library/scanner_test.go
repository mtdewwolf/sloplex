package library

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mtdewwolf/sloplex/internal/probe"
)

type countingProber struct {
	mu    sync.Mutex
	calls map[string]int
}

func (c *countingProber) Probe(_ context.Context, path string) (probe.Info, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.calls == nil {
		c.calls = map[string]int{}
	}
	c.calls[path]++
	return probe.Info{Container: probe.ContainerForPath(path), Duration: 5000, Width: 1920, Height: 1080, ProbeVersion: probe.Version}, nil
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestScan(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "The Matrix (1999).mkv"), "x")
	writeFile(t, filepath.Join(root, "Heat (1995)", "heat.mp4"), "x")
	writeFile(t, filepath.Join(root, "Heat (1995)", "Extras", "Making Of.mkv"), "x")
	writeFile(t, filepath.Join(root, "Heat (1995)", "heat-trailer.mp4"), "x")
	writeFile(t, filepath.Join(root, "Alien.1979.sample.mkv"), "x")
	writeFile(t, filepath.Join(root, ".hidden", "Ghost (2000).mkv"), "x")
	writeFile(t, filepath.Join(root, "notes.txt"), "x")

	s, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SyncSections([]SectionDef{{Title: "Movies", Type: TypeMovie, Paths: []string{root}}}, t0); err != nil {
		t.Fatal(err)
	}
	sec := s.Sections()[0].ID
	pr := &countingProber{}
	sc := NewScanner(s, pr, nil)

	stats, err := sc.Scan(context.Background(), sec)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Files != 2 || stats.Added != 2 {
		t.Fatalf("stats = %+v, want 2 files added", stats)
	}
	titles := map[string]int{}
	for _, it := range s.Items(sec) {
		titles[it.Title] = it.Year
		if it.Media[0].Duration != 5000 || it.Media[0].Container == "" {
			t.Errorf("%s: media info not stored: %+v", it.Title, it.Media[0])
		}
	}
	if titles["The Matrix"] != 1999 || titles["Heat"] != 1995 {
		t.Errorf("titles = %v", titles)
	}

	// Unchanged files are not re-probed; modified files are.
	modified := filepath.Join(root, "The Matrix (1999).mkv")
	writeFile(t, modified, "longer content")
	if err := os.Chtimes(modified, time.Now(), time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := sc.Scan(context.Background(), sec); err != nil {
		t.Fatal(err)
	}
	if n := pr.calls[filepath.Join(root, "Heat (1995)", "heat.mp4")]; n != 1 {
		t.Errorf("unchanged file probed %d times, want 1", n)
	}
	if n := pr.calls[modified]; n != 2 {
		t.Errorf("modified file probed %d times, want 2", n)
	}
}

func TestScanMissingRootKeepsItems(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "Heat (1995).mkv"), "x")
	s, _ := Open("")
	if err := s.SyncSections([]SectionDef{{Title: "Movies", Type: TypeMovie, Paths: []string{root}}}, t0); err != nil {
		t.Fatal(err)
	}
	sec := s.Sections()[0].ID
	sc := NewScanner(s, nil, nil)
	if _, err := sc.Scan(context.Background(), sec); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}
	if _, err := sc.Scan(context.Background(), sec); err == nil {
		t.Fatal("scan of missing root succeeded")
	}
	if n := len(s.Items(sec)); n != 1 {
		t.Errorf("items after failed scan = %d, want 1", n)
	}
}

func TestScanShows(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "Lost (2004)", "Season 01", "Lost - S01E01 - Pilot.mkv"), "x")
	writeFile(t, filepath.Join(root, "Lost (2004)", "Season 01", "Lost - S01E02.mkv"), "x")
	writeFile(t, filepath.Join(root, "Lost (2004)", "Season 01", "Lost - S01E02 - sample.mkv"), "x")
	writeFile(t, filepath.Join(root, "Lost (2004)", "Featurettes", "Lost - S01E01 - Making Of.mkv"), "x")
	writeFile(t, filepath.Join(root, "Lost (2004)", "Season 01", "behind the camera.mkv"), "x")

	s, _ := Open("")
	if err := s.SyncSections([]SectionDef{{Title: "TV", Type: TypeShow, Paths: []string{root}}}, t0); err != nil {
		t.Fatal(err)
	}
	sec := s.Sections()[0].ID
	stats, err := NewScanner(s, &countingProber{}, nil).Scan(context.Background(), sec)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Files != 2 || stats.Added != 2 {
		t.Fatalf("stats = %+v, want 2 episodes", stats)
	}
	shows := s.Items(sec, TypeShow)
	if len(shows) != 1 || shows[0].Title != "Lost" || shows[0].Year != 2004 {
		t.Fatalf("shows = %+v", shows)
	}
	eps := s.Leaves(shows[0].ID)
	if len(eps) != 2 || eps[0].Title != "Pilot" || eps[1].Title != "Episode 2" || eps[0].Media[0].Duration != 5000 {
		t.Errorf("episodes = %+v", eps)
	}
}

func TestScanDateShows(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "The Daily Show", "The Daily Show - 2024-03-15 - Guest.mkv"), "x")
	writeFile(t, filepath.Join(root, "The Daily Show", "The.Daily.Show.2024.03.15.1080p.mkv"), "x")
	writeFile(t, filepath.Join(root, "The Daily Show", "The Daily Show - 2024-01-02.mkv"), "x")
	writeFile(t, filepath.Join(root, "The Daily Show", "The Daily Show - 2023-12-31.mkv"), "x")

	s, _ := Open("")
	if err := s.SyncSections([]SectionDef{{Title: "TV", Type: TypeShow, Paths: []string{root}}}, t0); err != nil {
		t.Fatal(err)
	}
	sec := s.Sections()[0].ID
	if _, err := NewScanner(s, &countingProber{}, nil).Scan(context.Background(), sec); err != nil {
		t.Fatal(err)
	}
	shows := s.Items(sec, TypeShow)
	if len(shows) != 1 {
		t.Fatalf("shows = %+v", shows)
	}
	var got []string
	for _, season := range s.Children(shows[0].ID) {
		got = append(got, season.Title)
		for _, ep := range s.Children(season.ID) {
			got = append(got, fmt.Sprintf("%d %s %s x%d", ep.Index, ep.Aired, ep.Title, len(ep.Media)))
		}
	}
	want := "2023|1231 2023-12-31 2023-12-31 x1|2024|102 2024-01-02 2024-01-02 x1|315 2024-03-15 Guest x2"
	if strings.Join(got, "|") != want {
		t.Errorf("seasons and episodes =\n%s\nwant\n%s", strings.Join(got, "|"), want)
	}
}
