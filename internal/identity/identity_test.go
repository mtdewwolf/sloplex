package identity

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadCreatesOnce(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")
	a, created, err := Load(dir)
	if err != nil || !created {
		t.Fatalf("first Load: created=%v err=%v", created, err)
	}
	if len(a.MachineID) != 40 || len(a.Token) != 48 {
		t.Errorf("identity = %+v", a)
	}
	fi, err := os.Stat(filepath.Join(dir, fileName))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, want 0600", fi.Mode().Perm())
	}
	b, created, err := Load(dir)
	if err != nil || created || b != a {
		t.Errorf("second Load = %+v created=%v err=%v; want %+v", b, created, err, a)
	}
}
