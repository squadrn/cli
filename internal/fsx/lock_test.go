package fsx

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/squadrn/squadrn/internal/proc"
)

func TestLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "install.lock")
	unlock, err := Lock(path, 0)
	if err != nil {
		t.Fatal(err)
	}
	var le LockedError
	if _, err := Lock(path, 100*time.Millisecond); !errors.As(err, &le) || le.PID != os.Getpid() {
		t.Fatalf("a held lock was taken twice: %v", err)
	}
	unlock()
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("unlock left the lock file")
	}

	// A lock held by a live process (pid 1) is refused with its pid.
	if err := os.WriteFile(path, []byte(`{"pid":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if proc.Alive(1, "") {
		if _, err := Lock(path, 0); !errors.As(err, &le) || le.PID != 1 || le.Error() == "" {
			t.Fatalf("Lock = %v, want held by pid 1", err)
		}
	}

	// A dead pid, or a live pid with another start time, is stale and taken over.
	for _, body := range []string{`{"pid":999999999}`, `{"pid":1,"started":"never"}`} {
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if body != `{"pid":999999999}` && proc.StartTime(1) == "" {
			continue
		}
		unlock, err = Lock(path, 0)
		if err != nil {
			t.Fatalf("stale lock %s not taken over: %v", body, err)
		}
		unlock()
	}

	// An unreadable lock is trusted for a minute, then taken over.
	if err := os.WriteFile(path, []byte("garbage"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Lock(path, 0); !errors.As(err, &le) || le.PID != 0 || le.Error() == "" {
		t.Fatalf("a fresh unreadable lock was taken: %v", err)
	}
	if _, _, stale := lockState(path, time.Now().Add(2*time.Minute)); !stale {
		t.Fatal("an old unreadable lock was not stale")
	}
	if _, _, stale := lockState(filepath.Join(t.TempDir(), "none"), time.Now()); !stale {
		t.Fatal("a missing lock was not stale")
	}
	if _, err := Lock(filepath.Join(path, "below-a-file", "x.lock"), 0); err == nil {
		t.Fatal("Lock below a file succeeded")
	}
}
