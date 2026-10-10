package fswatch

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// seen holds paths already reported; a write is reported twice (created,
// then closed after writing), so repeats are allowed during silence checks.
var seen = map[string]bool{}

// expect waits for an event for want, skipping others; "" expects no new
// paths.
func expect(t *testing.T, w *Watcher, want string) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	if want == "" {
		deadline = time.After(300 * time.Millisecond)
	}
	for {
		select {
		case got := <-w.Events():
			if want == "" && !seen[got] {
				t.Fatalf("unexpected event %q", got)
			}
			seen[got] = true
			if got == want {
				return
			}
		case <-deadline:
			if want != "" {
				t.Fatalf("no event for %s", want)
			}
			return
		}
	}
}

func TestWatcher(t *testing.T) {
	root := t.TempDir()
	for _, d := range []string{"Heat (1995)", "Heat (1995)/Extras", ".hidden"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	w, err := New(func(name string) bool { return name == "Extras" })
	if err != nil {
		t.Fatal(err)
	}
	if err := w.AddTree(root); err != nil {
		t.Fatal(err)
	}
	write := func(rel string) string {
		p := filepath.Join(root, rel)
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}

	expect(t, w, write("Heat (1995)/heat.mkv"))
	expect(t, w, write("top.mkv"))
	// Skipped and hidden directories are not watched; hidden names are not
	// reported.
	write("Heat (1995)/Extras/making-of.mkv")
	write(".hidden/ghost.mkv")
	write(".partial.mkv")
	expect(t, w, "")

	// A new directory is reported and then watched.
	fresh := filepath.Join(root, "Alien (1979)")
	if err := os.Mkdir(fresh, 0o755); err != nil {
		t.Fatal(err)
	}
	expect(t, w, fresh)
	expect(t, w, write("Alien (1979)/alien.mkv"))

	// A rename into place (as rsync does) is reported under the new name.
	tmp := write(".heat.mkv.XyZ")
	final := filepath.Join(root, "Heat (1995)", "final.mkv")
	if err := os.Rename(tmp, final); err != nil {
		t.Fatal(err)
	}
	expect(t, w, final)

	// A directory moved out of the tree is no longer watched; moved in
	// under a new name, it is watched there.
	outside := t.TempDir()
	if err := os.Rename(fresh, filepath.Join(outside, "gone")); err != nil {
		t.Fatal(err)
	}
	expect(t, w, fresh)
	if err := os.WriteFile(filepath.Join(outside, "gone", "x.mkv"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	expect(t, w, "")
	back := filepath.Join(root, "Alien Returns (1986)")
	if err := os.Rename(filepath.Join(outside, "gone"), back); err != nil {
		t.Fatal(err)
	}
	expect(t, w, back)
	expect(t, w, write("Alien Returns (1986)/aliens.mkv"))

	if err := os.Remove(filepath.Join(root, "top.mkv")); err != nil {
		t.Fatal(err)
	}
	expect(t, w, filepath.Join(root, "top.mkv"))

	if err := w.AddTree(filepath.Join(root, "missing")); err == nil {
		t.Error("AddTree of a missing root succeeded")
	}
	w.Close()
	select {
	case _, ok := <-w.Events():
		for ok {
			_, ok = <-w.Events()
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Events not closed after Close")
	}
}

// TestCloseWhileBlocked closes a watcher whose events nobody reads.
func TestCloseWhileBlocked(t *testing.T) {
	root := t.TempDir()
	w, err := New(nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.AddTree(root); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 400; i++ { // more than the channel buffer
		os.WriteFile(filepath.Join(root, "f"+string(rune('a'+i%26))+".mkv"), nil, 0o644)
	}
	time.Sleep(100 * time.Millisecond)
	w.Close()
	done := make(chan struct{})
	go func() {
		for range w.Events() {
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("watcher goroutine stuck after Close")
	}
}
