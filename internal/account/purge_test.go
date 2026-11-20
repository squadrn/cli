package account

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestPurge(t *testing.T) {
	s := newStore(t, "old")
	a, _ := s.Get("old")
	outside := filepath.Join(s.Home, "outside")
	must(t, os.MkdirAll(filepath.Join(outside, "sub"), 0o755))
	must(t, os.WriteFile(filepath.Join(outside, "sub", "keep"), []byte("x"), 0o600))
	must(t, os.MkdirAll(filepath.Join(a.Dir, "projects", "p"), 0o755))
	must(t, os.WriteFile(filepath.Join(a.Dir, "projects", "p", "t.jsonl"), []byte("{}"), 0o400))
	must(t, os.Chmod(filepath.Join(a.Dir, "projects", "p"), 0o500))
	if err := os.Symlink(outside, filepath.Join(a.Dir, "agents")); err == nil {
		must(t, os.Symlink(filepath.Join(outside, "sub", "keep"), filepath.Join(a.Dir, "CLAUDE.md")))
	} else {
		t.Logf("no symbolic links here: %v", err)
	}
	must(t, s.Purge(a))
	if _, err := os.Lstat(a.Dir); !os.IsNotExist(err) {
		t.Fatalf("account dir left: %v", err)
	}
	if b, err := os.ReadFile(filepath.Join(outside, "sub", "keep")); err != nil || string(b) != "x" {
		t.Fatalf("a link target outside the account was touched: %q %v", b, err)
	}
	if s.Exists("old") {
		t.Error("account still exists")
	}
	m, _ := s.Get("main")
	if err := s.Purge(m); !errors.Is(err, ErrReserved) {
		t.Errorf("purging main err = %v", err)
	}
}

func TestPurgeRefusesLinkedDir(t *testing.T) {
	s := newStore(t)
	real := filepath.Join(s.Home, "elsewhere")
	mkAccount(t, real)
	if err := os.Symlink(real, filepath.Join(s.Home, ".claude-linked")); err != nil {
		t.Skipf("no symbolic links here: %v", err)
	}
	a, err := s.Get("linked")
	must(t, err)
	if err := s.Purge(a); !errors.Is(err, ErrLinkedDir) {
		t.Fatalf("Purge of a linked dir = %v", err)
	}
	if !s.Exists("linked") {
		t.Fatal("the linked account was removed")
	}
	if _, err := os.Stat(filepath.Join(real, Marker)); err != nil {
		t.Fatalf("link target touched: %v", err)
	}
}

func TestPurgeKeepsMarkerOnFailure(t *testing.T) {
	s := newStore(t, "old")
	a, _ := s.Get("old")
	busy := filepath.Join(a.Dir, "sessions", "busy.json")
	must(t, os.MkdirAll(filepath.Dir(busy), 0o755))
	must(t, os.WriteFile(busy, nil, 0o600))
	must(t, os.WriteFile(filepath.Join(a.Dir, "other"), nil, 0o600))
	errBusy := errors.New("used by another process")
	osRemove = func(p string) error {
		if p == busy {
			return &os.PathError{Op: "remove", Path: p, Err: errBusy}
		}
		return os.Remove(p)
	}
	t.Cleanup(func() { osRemove = os.Remove })
	err := s.Purge(a)
	var pe *PurgeError
	if !errors.As(err, &pe) || pe.Dir != a.Dir || len(pe.Failed) != 1 || !errors.Is(err, errBusy) {
		t.Fatalf("Purge = %v", err)
	}
	if !s.Exists("old") {
		t.Fatal("marker removed after a failure; the account cannot be named to retry")
	}
	if _, err := os.Stat(filepath.Join(a.Dir, "other")); !os.IsNotExist(err) {
		t.Error("the rest was not deleted")
	}
	osRemove = os.Remove
	must(t, s.Purge(a))
	if s.Exists("old") {
		t.Error("retry left the account")
	}
}
