package fsx

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPaths(t *testing.T) {
	dir := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(dir, link); err != nil {
		t.Skip("no symlinks:", err)
	}
	if !SamePath(dir, dir+"/") || !SamePath(link, dir) || SamePath(dir, dir+"x") {
		t.Fatal("SamePath")
	}
	if Real(link) != Real(dir) || Real(filepath.Join(link, "missing")) != filepath.Join(Real(dir), "missing") {
		t.Fatal("Real")
	}
	tests := []struct {
		path, dir string
		want      bool
	}{
		{dir, dir, true},
		{dir + "/sub", dir, true},
		{filepath.Join(link, "sub"), dir, true},
		{dir, dir + "/sub", false},
		{"/etc", "/", false},
		{"", dir, false},
		{dir + "x", dir, false},
	}
	for _, tt := range tests {
		if got := Within(tt.path, tt.dir); got != tt.want {
			t.Errorf("Within(%q, %q) = %v", tt.path, tt.dir, got)
		}
	}
}

func TestBackupName(t *testing.T) {
	p := filepath.Join(t.TempDir(), "srn")
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	if got := BackupName(p, now); got != p+".bak-20260102-030405" {
		t.Fatalf("BackupName = %s", got)
	}
}
