//go:build windows

package fsx

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReplaceExecutableRestoresOnFailure(t *testing.T) {
	dir := t.TempDir()
	src, dst := filepath.Join(dir, "new.exe"), filepath.Join(dir, "tool.exe")
	if err := os.WriteFile(src, []byte("new"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	defer func() { rename = os.Rename }()
	rename = func(from, to string) error {
		if strings.Contains(from, ".new-") {
			return errors.New("injected")
		}
		return os.Rename(from, to)
	}
	if err := ReplaceExecutable(src, dst); err == nil {
		t.Fatal("ReplaceExecutable reported success")
	}
	if b, err := os.ReadFile(dst); err != nil || string(b) != "old" {
		t.Fatalf("dst = %q, %v; want the previous executable back", b, err)
	}
	if left, _ := filepath.Glob(filepath.Join(dir, "tool.exe.*")); len(left) != 0 {
		t.Fatalf("leftovers: %v", left)
	}
}
