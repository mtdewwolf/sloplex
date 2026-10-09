package atomicfile

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "f.json")
	for _, content := range []string{"one", "two"} {
		if err := Write(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		b, err := os.ReadFile(path)
		if err != nil || string(b) != content {
			t.Fatalf("read %q, %v; want %q", b, err, content)
		}
	}
	fi, _ := os.Stat(path)
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v", fi.Mode().Perm())
	}
	if entries, _ := os.ReadDir(filepath.Dir(path)); len(entries) != 1 {
		t.Errorf("temp files left behind: %v", entries)
	}
}
