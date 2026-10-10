//go:build unix

package transcode

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

// TestThrottle runs an HLS session that is throttled 8 s ahead of the
// player, then asks for a later segment and checks that it resumes.
func TestThrottle(t *testing.T) {
	ffmpeg, path := clip(t, "40")
	m, err := New(Options{FFmpeg: ffmpeg, Dir: filepath.Join(t.TempDir(), "tc"), MaxTranscodes: 1, Throttle: true})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	m.setThrottleLimits(8*time.Second, 4*time.Second, 10*time.Millisecond)
	ctx := context.Background()
	s, err := m.StartHLS(1, "", 1, Request{Input: path, Source: clipInfo, MaxWidth: 160, MaxHeight: 90})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Segment(ctx, s, "00000.ts"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "throttling", func() bool { return s.Throttled() })
	if list := m.List(); len(list) != 1 || !list[0].Throttled {
		t.Errorf("List = %+v, want a throttled session", list)
	}
	segs, _ := listedSegments(filepath.Join(s.dir, "index.m3u8"))
	// Paused just past 8 s ahead of segment 0: segment 3 (12 s) exists, and
	// a fast machine may finish another before the signal.
	if n := len(segs); n < 4 || n > 6 {
		t.Errorf("%d segments when throttled, want 4 to 6", n)
	}
	time.Sleep(300 * time.Millisecond)
	if later, _ := listedSegments(filepath.Join(s.dir, "index.m3u8")); len(later) != len(segs) {
		t.Errorf("ffmpeg kept working while throttled: %d then %d segments", len(segs), len(later))
	}

	// Asking for the newest segment brings it within the resume limit.
	if _, err := m.Segment(ctx, s, fmt.Sprintf("%05d.ts", len(segs)-1)); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "more segments", func() bool {
		more, _ := listedSegments(filepath.Join(s.dir, "index.m3u8"))
		return len(more) > len(segs)
	})
	// Requests far ahead keep it running to the end.
	if _, err := m.Segment(ctx, s, "00009.ts"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-s.done:
	case <-time.After(30 * time.Second):
		t.Fatal("throttled session never finished")
	}
	if s.err != nil || s.Throttled() {
		t.Errorf("finished session: err %v, throttled %v", s.err, s.Throttled())
	}
}

func TestNoThrottleByDefault(t *testing.T) {
	ffmpeg, path := clip(t, "20")
	m, _ := New(Options{FFmpeg: ffmpeg, Dir: filepath.Join(t.TempDir(), "tc"), MaxTranscodes: 1})
	defer m.Close()
	m.setThrottleLimits(4*time.Second, 2*time.Second, 10*time.Millisecond)
	s, err := m.StartHLS(1, "", 1, Request{Input: path, Source: clipInfo, MaxWidth: 160, MaxHeight: 90})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-s.done:
	case <-time.After(30 * time.Second):
		t.Fatal("session never finished")
	}
	if segs, _ := listedSegments(filepath.Join(s.dir, "index.m3u8")); len(segs) != 5 {
		t.Errorf("%d segments, want all 5", len(segs))
	}
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
