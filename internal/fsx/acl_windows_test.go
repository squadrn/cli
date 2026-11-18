//go:build windows

package fsx

import (
	"os"
	"path/filepath"
	"testing"
)

func TestProtectDirOutsideProfile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")
	t.Setenv("USERPROFILE", filepath.Join(t.TempDir(), "profile"))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if Protected(dir) {
		t.Fatal("a fresh directory outside the profile reports protected")
	}
	if err := ProtectDir(dir); err != nil {
		t.Fatal(err)
	}
	if !Protected(dir) {
		t.Fatal("ProtectDir did not protect the DACL")
	}
	t.Setenv("USERPROFILE", filepath.Dir(dir))
	if !Protected(dir) {
		t.Fatal("inside the profile counts as protected")
	}
}
