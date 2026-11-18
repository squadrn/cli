//go:build windows

package claude

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLookPathRefusesCmdShim(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PATH", dir)
	if _, err := lookPath(); !errors.Is(err, errNotInstalled) {
		t.Fatalf("empty PATH: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "claude.cmd"), []byte("@echo off\r\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := lookPath(); err == nil || !strings.Contains(err.Error(), "native claude.exe") {
		t.Fatalf("claude.cmd only: %v", err)
	}
	exe := filepath.Join(dir, "claude.exe")
	if err := os.WriteFile(exe, []byte("MZ"), 0o755); err != nil {
		t.Fatal(err)
	}
	if bin, err := lookPath(); err != nil || !strings.EqualFold(bin, exe) {
		t.Fatalf("claude.exe: %q, %v", bin, err)
	}
}
