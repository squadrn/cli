package fsx

import (
	"os"
	"path/filepath"
	"testing"
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
