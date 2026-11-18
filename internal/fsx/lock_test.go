package fsx

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "install.lock")
	unlock, err := Lock(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Lock(path); err == nil {
		t.Fatal("a held lock was taken twice")
	}
	unlock()
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("unlock left the lock file")
	}

	// A lock held by a live process (our parent) is refused with its pid.
	if err := os.WriteFile(path, []byte("1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if processAlive(1) {
		var le LockedError
		if _, err := Lock(path); !errors.As(err, &le) || le.PID != 1 {
			t.Fatalf("Lock = %v, want held by pid 1", err)
		}
	}

	// A dead pid is stale and taken over.
	if err := os.WriteFile(path, []byte("999999999\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	unlock, err = Lock(path)
	if err != nil {
		t.Fatalf("stale lock not taken over: %v", err)
	}
	unlock()

	// An unreadable lock is trusted for a minute, then taken over.
	if err := os.WriteFile(path, []byte("garbage"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, stale := lockState(path, time.Now()); stale {
		t.Fatal("a fresh unreadable lock counted as stale")
	}
	if _, stale := lockState(path, time.Now().Add(2*time.Minute)); !stale {
		t.Fatal("an old unreadable lock was not stale")
	}
	if _, stale := lockState(filepath.Join(t.TempDir(), "none"), time.Now()); !stale {
		t.Fatal("a missing lock was not stale")
	}
}
