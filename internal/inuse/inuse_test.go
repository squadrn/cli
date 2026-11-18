package inuse

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAcquireActiveRelease(t *testing.T) {
	dir := t.TempDir()
	if got := Active(dir); len(got) != 0 {
		t.Fatalf("Active on an unused dir = %+v", got)
	}
	if err := Acquire(dir, Entry{Account: "claude/x", Worktree: "feat-a"}); err != nil {
		t.Fatal(err)
	}
	got := Active(dir)
	if len(got) != 1 || got[0].PID != os.Getpid() || got[0].Account != "claude/x" || got[0].Worktree != "feat-a" || got[0].Exe == "" {
		t.Fatalf("Active = %+v", got)
	}
	if err := Release(dir); err != nil {
		t.Fatal(err)
	}
	if len(Active(dir)) != 0 {
		t.Fatal("entry left after Release")
	}
	if _, err := os.Stat(filepath.Join(dir, Dir)); !os.IsNotExist(err) {
		t.Fatal("empty registry dir left behind")
	}
	if err := Release(dir); err != nil {
		t.Fatalf("second Release: %v", err)
	}
}

func TestActiveDropsStaleEntries(t *testing.T) {
	dir := t.TempDir()
	reg := filepath.Join(dir, Dir)
	if err := os.MkdirAll(reg, 0o700); err != nil {
		t.Fatal(err)
	}
	write := func(name string, e any) {
		b, _ := json.Marshal(e)
		if err := os.WriteFile(filepath.Join(reg, name), b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	self := os.Getpid()
	write("999999999.json", Entry{PID: 999999999})                        // no such process
	write("1.json", Entry{PID: 2})                                        // pid does not match the name
	write(itoa(self)+".json", Entry{PID: self, Started: "not the start"}) // pid reused
	write("junk.json", Entry{PID: self})                                  // foreign file, kept
	if got := Active(dir); len(got) != 0 {
		t.Fatalf("Active = %+v", got)
	}
	left, _ := os.ReadDir(reg)
	if len(left) != 1 || left[0].Name() != "junk.json" {
		t.Fatalf("left = %v", left)
	}
}

func itoa(n int) string { b, _ := json.Marshal(n); return string(b) }

func TestLive(t *testing.T) {
	if !Live(os.Getpid(), startTime(os.Getpid())) || !Live(os.Getpid(), "") {
		t.Fatal("this process is not live")
	}
	if Live(0, "") || Live(999999999, "") {
		t.Fatal("a missing process is live")
	}
}

func TestLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.lock")
	unlock, err := Lock(path, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Lock(path, 100*time.Millisecond); !errors.Is(err, ErrLocked) {
		t.Fatalf("second Lock: %v", err)
	}
	unlock()
	unlock, err = Lock(path, time.Second)
	if err != nil {
		t.Fatalf("Lock after unlock: %v", err)
	}
	unlock()

	// A lock left by a dead process is taken over.
	if err := os.WriteFile(path, []byte(`{"pid":999999999}`), 0o600); err != nil {
		t.Fatal(err)
	}
	unlock, err = Lock(path, time.Second)
	if err != nil {
		t.Fatalf("stale lock: %v", err)
	}
	unlock()
	if _, err := Lock(filepath.Join(t.TempDir(), "missing", "x.lock"), 0); err == nil {
		t.Fatal("Lock in a missing dir succeeded")
	}
}
