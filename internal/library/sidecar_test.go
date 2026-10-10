package library

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/mtdewwolf/sloplex/internal/probe"
)

func TestParseSidecar(t *testing.T) {
	const stem = "Heat (1995)"
	for name, want := range map[string]*probe.Stream{
		"Heat (1995).srt":                {Index: -1, Type: "subtitle", Codec: "srt"},
		"Heat (1995).en.srt":             {Index: -1, Type: "subtitle", Codec: "srt", Language: "eng"},
		"heat (1995).ENG.forced.ass":     {Index: -1, Type: "subtitle", Codec: "ass", Language: "eng", Forced: true},
		"Heat (1995).fr.sdh.vtt":         {Index: -1, Type: "subtitle", Codec: "vtt", Language: "fre", HearingImpaired: true},
		"Heat (1995).french.default.ssa": {Index: -1, Type: "subtitle", Codec: "ssa", Language: "fre", Default: true},
		"Heat (1995).hi.srt":             {Index: -1, Type: "subtitle", Codec: "srt", HearingImpaired: true},
		"Heat (1995).unknowntag.srt":     {Index: -1, Type: "subtitle", Codec: "srt"},
		"Heat (1995) extended.srt":       nil,
		"Heat (1995).en.txt":             nil,
		"Heat (1995).mkv":                nil,
		"Other (2000).en.srt":            nil,
	} {
		got, ok := parseSidecar(stem, name)
		switch {
		case want == nil && ok:
			t.Errorf("%q accepted as %+v", name, got)
		case want != nil && (!ok || got.Stream != *want):
			t.Errorf("%q = %+v, %v; want %+v", name, got.Stream, ok, *want)
		}
	}
}

func TestScanStreamsAndSidecars(t *testing.T) {
	root := t.TempDir()
	video := filepath.Join(root, "Heat (1995)", "Heat (1995).mkv")
	writeFile(t, video, "x")
	writeFile(t, filepath.Join(root, "Heat (1995)", "Heat (1995).en.srt"), "1\n")
	s, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SyncSections([]SectionDef{{Title: "M", Type: TypeMovie, Paths: []string{root}}}, t0); err != nil {
		t.Fatal(err)
	}
	sec := s.Sections()[0].ID
	pr := &streamProber{}
	sc := NewScanner(s, pr, nil)
	if _, err := sc.Scan(context.Background(), sec); err != nil {
		t.Fatal(err)
	}
	part := s.Items(sec)[0].Media[0].Parts[0]
	if len(part.Streams) != 3 || part.Streams[2].File == "" || part.Streams[2].Language != "eng" {
		t.Fatalf("streams = %+v", part.Streams)
	}
	ids := []int{part.Streams[0].ID, part.Streams[1].ID, part.Streams[2].ID}
	if got, gotPart, gotSec, err := s.Stream(ids[2]); err != nil || got.File == "" || gotPart.ID != part.ID || gotSec != sec {
		t.Errorf("Stream(%d) = %+v, part %d, section %d, %v", ids[2], got, gotPart.ID, gotSec, err)
	}

	// Rescan: same IDs, no re-probe, and a new sidecar gets a new ID.
	writeFile(t, filepath.Join(root, "Heat (1995)", "Heat (1995).fr.forced.srt"), "1\n")
	if _, err := sc.Scan(context.Background(), sec); err != nil {
		t.Fatal(err)
	}
	part = s.Items(sec)[0].Media[0].Parts[0]
	if len(part.Streams) != 4 || part.Streams[0].ID != ids[0] || part.Streams[1].ID != ids[1] {
		t.Fatalf("after rescan = %+v (old IDs %v)", part.Streams, ids)
	}
	var en, fr Stream
	for _, st := range part.Streams[2:] {
		if st.Language == "eng" {
			en = st
		} else {
			fr = st
		}
	}
	if en.ID != ids[2] || fr.ID == 0 || !fr.Forced || fr.ID == ids[2] {
		t.Errorf("sidecars en=%+v fr=%+v", en, fr)
	}
	if pr.calls != 1 {
		t.Errorf("probed %d times, want 1", pr.calls)
	}
	// Media.Info does not duplicate the streams.
	if n := len(s.Items(sec)[0].Media[0].Streams); n != 0 {
		t.Errorf("media info kept %d streams", n)
	}
}

func TestOldProbeVersionIsReprobed(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "Heat (1995).mkv"), "x")
	s, _ := Open("")
	_ = s.SyncSections([]SectionDef{{Title: "M", Type: TypeMovie, Paths: []string{root}}}, t0)
	sec := s.Sections()[0].ID
	if _, err := NewScanner(s, oldProber{}, nil).Scan(context.Background(), sec); err != nil {
		t.Fatal(err)
	}
	pr := &streamProber{}
	sc := NewScanner(s, pr, nil)
	for range 2 {
		if _, err := sc.Scan(context.Background(), sec); err != nil {
			t.Fatal(err)
		}
	}
	if pr.calls != 1 || len(s.Items(sec)[0].Media[0].Parts[0].Streams) != 2 {
		t.Errorf("re-probe calls = %d, streams = %+v", pr.calls, s.Items(sec)[0].Media[0].Parts[0].Streams)
	}
}

// oldProber returns ProbeVersion 0, like versions before stream details.
type oldProber struct{}

func (oldProber) Probe(_ context.Context, path string) (probe.Info, error) {
	return probe.Info{Container: probe.ContainerForPath(path), VideoCodec: "h264"}, nil
}

type streamProber struct{ calls int }

func (p *streamProber) Probe(_ context.Context, path string) (probe.Info, error) {
	p.calls++
	return probe.Info{Container: probe.ContainerForPath(path), VideoCodec: "h264", AudioCodec: "aac", ProbeVersion: probe.Version,
		Streams: []probe.Stream{
			{Index: 0, Type: probe.StreamVideo, Codec: "h264", Width: 1920, Height: 1080},
			{Index: 1, Type: probe.StreamAudio, Codec: "aac", Channels: 2, Language: "eng"},
		}}, nil
}
