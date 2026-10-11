package transcode

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// fakeFFmpeg writes a script that stands in for a long-running ffmpeg: an
// HLS run writes one segment and a playlist listing it, a progressive run
// writes enough to fill the response buffers, and both then wait to be
// killed.
func fakeFFmpeg(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("needs a POSIX shell")
	}
	path := filepath.Join(t.TempDir(), "ffmpeg")
	script := `#!/bin/sh
for a; do last=$a; done
case "$last" in
*/index.m3u8)
	dir=${last%/index.m3u8}
	printf 'G' > "$dir/00000.ts"
	printf '#EXTM3U\n#EXT-X-TARGETDURATION:4\n#EXTINF:4.0,\n00000.ts\n' > "$dir/tmp.m3u8"
	mv "$dir/tmp.m3u8" "$last" ;;
*) head -c 131072 /dev/zero ;;
esac
exec sleep 60
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestRevoke(t *testing.T) {
	m, err := New(Options{FFmpeg: fakeFFmpeg(t), Dir: filepath.Join(t.TempDir(), "tc"), MaxTranscodes: 4})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	ctx := context.Background()
	req := Request{Input: filepath.Join(t.TempDir(), "in.mkv"), Source: clipInfo}
	alice, bob := Owner{Account: 2, Token: 5}, Owner{Account: 3}

	a, err := m.StartHLS(alice, "p", 1, req)
	if err != nil {
		t.Fatal(err)
	}
	b, err := m.StartHLS(bob, "p", 1, req)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Playlist(ctx, a, "base"); err != nil {
		t.Fatalf("playlist before revocation: %v", err)
	}
	started := make(chan struct{})
	streamed := make(chan error, 1)
	go func() {
		streamed <- m.Stream(ctx, alice, 2, req, io.Discard, func(Plan) { close(started) })
	}()
	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Fatal("progressive stream did not start")
	}

	var seen []Owner
	n := m.Revoke(func(o Owner, itemID int) bool {
		seen = append(seen, o)
		return o.Token != alice.Token
	})
	if n != 2 || len(seen) != 3 {
		t.Errorf("Revoke = %d after %d checks, want 2 after 3", n, len(seen))
	}
	if _, ok := m.Session(a.ID); ok {
		t.Error("revoked session still present")
	}
	if _, err := os.Stat(a.dir); !os.IsNotExist(err) {
		t.Error("revoked session directory not removed")
	}
	if _, ok := m.Session(b.ID); !ok {
		t.Error("unaffected session stopped")
	}
	select {
	case <-streamed:
	case <-time.After(10 * time.Second):
		t.Fatal("revoked progressive stream kept running")
	}
	if list := m.List(); len(list) != 1 || list[0].ID != b.ID {
		t.Errorf("List after revocation = %+v", list)
	}

	// A new credential of the same account replaces a labelled session
	// outright; the replacement carries only the new credential.
	c, err := m.StartHLS(Owner{Account: 3, Token: 9}, "p", 1, req)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := m.Session(b.ID); ok || c.Owner != (Owner{Account: 3, Token: 9}) {
		t.Errorf("replacement: old present=%v, owner=%+v", ok, c.Owner)
	}
	var nilManager *Manager
	if nilManager.Revoke(func(Owner, int) bool { return false }) != 0 {
		t.Error("nil manager revoked something")
	}
}

func TestRevokeSourcesKeepsHealthyVersion(t *testing.T) {
	m, err := New(Options{FFmpeg: fakeFFmpeg(t), Dir: filepath.Join(t.TempDir(), "tc"), MaxTranscodes: 4})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	missing := Request{Input: "/m/missing.mkv", PartID: 11, Source: clipInfo}
	healthy := Request{Input: "/m/healthy.mkv", PartID: 12, Source: clipInfo}
	owner := Owner{Account: 2}
	lost, err := m.StartHLS(owner, "lost", 1, missing)
	if err != nil {
		t.Fatal(err)
	}
	kept, err := m.StartHLS(owner, "kept", 1, healthy)
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- m.Stream(context.Background(), owner, 1, missing, io.Discard, func(Plan) { close(started) })
	}()
	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Fatal("progressive source did not start")
	}
	if n := m.RevokeSources(func(o Owner, itemID, partID int) bool { return partID != 11 }); n != 2 {
		t.Fatalf("source revocation stopped %d, want 2", n)
	}
	if _, ok := m.Session(lost.ID); ok {
		t.Fatal("missing source HLS still present")
	}
	if _, ok := m.Session(kept.ID); !ok {
		t.Fatal("healthy source HLS stopped")
	}
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("missing progressive source kept running")
	}
}
