//go:build windows

package release

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestLinkCommandsWindows(t *testing.T) {
	home := t.TempDir()
	l := Layout{DataDir: filepath.Join(home, "data"), BinDir: filepath.Join(home, "bin")}
	if err := os.MkdirAll(filepath.Dir(l.Binary()), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(l.Binary(), []byte("v1"), 0o755); err != nil {
		t.Fatal(err)
	}
	foreign := filepath.Join(l.BinDir, "sx.exe")
	if err := os.MkdirAll(l.BinDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(foreign, []byte("someone else's"), 0o755); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	res, err := l.LinkCommands([]string{"sprawl", "sx"}, nil, now)
	if err != nil || len(res.Created) != 2 || len(res.BackedUp) != 1 {
		t.Fatalf("first install: %+v, %v", res, err)
	}
	if b, _ := os.ReadFile(filepath.Join(l.BinDir, "sprawl.exe")); string(b) != "v1" {
		t.Fatalf("copy = %q", b)
	}
	// An update replaces the copies; a dropped alias goes away.
	if err := os.WriteFile(l.Binary(), []byte("v2"), 0o755); err != nil {
		t.Fatal(err)
	}
	res, err = l.LinkCommands([]string{"sprawl"}, nil, now)
	if err != nil || len(res.Removed) != 1 || len(res.Created) != 0 {
		t.Fatalf("update: %+v, %v", res, err)
	}
	if b, _ := os.ReadFile(filepath.Join(l.BinDir, "sprawl.exe")); string(b) != "v2" {
		t.Fatalf("updated copy = %q", b)
	}
	removed, err := l.Unlink()
	if err != nil || len(removed) != 1 {
		t.Fatalf("Unlink = %v, %v", removed, err)
	}
}

// A running command copy is replaced by moving it aside; the process keeps running.
func TestReplaceRunningCommand(t *testing.T) {
	home := t.TempDir()
	l := Layout{DataDir: filepath.Join(home, "data"), BinDir: filepath.Join(home, "bin")}
	ping, err := exec.LookPath("ping")
	if err != nil {
		t.Skip("ping not found")
	}
	if err := os.MkdirAll(filepath.Dir(l.Binary()), 0o755); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(ping)
	if err := os.WriteFile(l.Binary(), b, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := l.LinkCommands([]string{"sprawl"}, nil, time.Now()); err != nil {
		t.Fatal(err)
	}
	running := exec.Command(filepath.Join(l.BinDir, "sprawl.exe"), "-n", "30", "127.0.0.1")
	if err := running.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = running.Process.Kill(); _ = running.Wait() }()
	if _, err := l.LinkCommands([]string{"sprawl"}, nil, time.Now()); err != nil {
		t.Fatalf("replacing a running copy: %v", err)
	}
	if _, err := os.Stat(filepath.Join(l.BinDir, "sprawl.exe")); err != nil {
		t.Fatal(err)
	}
}
