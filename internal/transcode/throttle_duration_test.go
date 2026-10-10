package transcode

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAheadUsesPlaylistDurations(t *testing.T) {
	s := &Session{dir: t.TempDir(), nudge: make(chan struct{}, 1)}
	data := "#EXTM3U\n#EXTINF:20.0,\n00000.ts\n#EXTINF:50.5,\n00001.ts\n#EXTINF:45.0,\n00002.ts\n"
	if err := os.WriteFile(filepath.Join(s.dir, "index.m3u8"), []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	if got := s.ahead(); got != 95500*time.Millisecond {
		t.Fatalf("lead %v", got)
	}
	s.requestSegment(1)
	if got := s.ahead(); got != 45*time.Second {
		t.Fatalf("lead %v", got)
	}
	s.requestSegment(0)
	if got := s.ahead(); got != 45*time.Second {
		t.Fatalf("backward request changed lead: %v", got)
	}
	s.requestSegment(9)
	if got := s.ahead(); got != 0 {
		t.Fatalf("forward request lead %v", got)
	}
}
