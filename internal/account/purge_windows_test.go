package account

import (
	"os"
	"path/filepath"
	"testing"
)

// A shared dir is a junction on Windows without symlink privilege: purging removes the junction
// and never its target. Read-only files go too.
func TestPurgeJunction(t *testing.T) {
	s := newStore(t, "old")
	a, _ := s.Get("old")
	outside := filepath.Join(s.Home, "outside")
	must(t, os.MkdirAll(outside, 0o755))
	must(t, os.WriteFile(filepath.Join(outside, "keep"), []byte("x"), 0o600))
	must(t, junction(outside, filepath.Join(a.Dir, "agents")))
	must(t, os.WriteFile(filepath.Join(a.Dir, "ro.json"), nil, 0o400))
	must(t, s.Purge(a))
	if _, err := os.Lstat(a.Dir); !os.IsNotExist(err) {
		t.Fatalf("account dir left: %v", err)
	}
	if b, err := os.ReadFile(filepath.Join(outside, "keep")); err != nil || string(b) != "x" {
		t.Fatalf("junction target touched: %q %v", b, err)
	}

	mkAccount(t, filepath.Join(s.Home, "real"))
	must(t, junction(filepath.Join(s.Home, "real"), filepath.Join(s.Home, ".claude-linked")))
	l, err := s.Get("linked")
	must(t, err)
	if err := s.Purge(l); err == nil {
		t.Fatal("purged an account dir that is a junction")
	}
	if _, err := os.Stat(filepath.Join(s.Home, "real", Marker)); err != nil {
		t.Fatalf("junction target touched: %v", err)
	}
}
