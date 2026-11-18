package fsx

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestWriteFileAtomicAndPermissions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "f.json")
	if err := WriteBytes(path, []byte("one"), 0o600); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(path)
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("stat = %v, %v", fi, err)
	}
	if fi, _ := os.Stat(filepath.Dir(path)); fi.Mode().Perm() != 0o700 {
		t.Errorf("new dir mode = %v", fi.Mode().Perm())
	}
	if leftovers, _ := filepath.Glob(filepath.Join(dir, "sub", ".f.json.tmp-*")); len(leftovers) != 0 {
		t.Errorf("temp files left: %v", leftovers)
	}
}

func TestConcurrentWritersNeverCorrupt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache.json")
	var wg sync.WaitGroup
	for i := range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = WriteBytes(path, []byte{byte('a' + i%26)}, 0o600)
		}()
	}
	wg.Wait()
	b, err := os.ReadFile(path)
	if err != nil || len(b) != 1 {
		t.Fatalf("content = %q, %v", b, err)
	}
}

func TestCopyTreeRejectsSymlinksAndKeepsTimes(t *testing.T) {
	src := filepath.Join(t.TempDir(), "src")
	if err := os.MkdirAll(filepath.Join(src, "a"), 0o755); err != nil {
		t.Fatal(err)
	}
	f := filepath.Join(src, "a", "x")
	if err := os.WriteFile(f, []byte("x"), 0o777); err != nil {
		t.Fatal(err)
	}
	mod := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := os.Chtimes(f, mod, mod); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(t.TempDir(), "dst")
	if err := CopyTree(src, dst, 0o755, true); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(filepath.Join(dst, "a", "x"))
	if err != nil || !fi.ModTime().Equal(mod) || fi.Mode().Perm() != 0o755 {
		t.Fatalf("copied = %v, %v", fi, err)
	}
	if err := os.Symlink("/etc/passwd", filepath.Join(src, "link")); err != nil {
		t.Fatal(err)
	}
	if err := CopyTree(src, filepath.Join(t.TempDir(), "d2"), 0o755, false); err == nil {
		t.Fatal("symlink copied")
	}
}

func TestReplaceDirAndCleanStale(t *testing.T) {
	root := t.TempDir()
	src, dst := filepath.Join(root, "src"), filepath.Join(root, "dst")
	for _, d := range []string{src, dst} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(src, "new"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dst, "stale"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ReplaceDir(src, dst, 0o755); err != nil {
		t.Fatal(err)
	}
	if !IsFile(filepath.Join(dst, "new")) || IsFile(filepath.Join(dst, "stale")) || IsDir(dst+".old") || IsDir(dst+".new") {
		t.Fatal("ReplaceDir result wrong")
	}

	// Simulate a crash between the two renames: dst missing, dst.old present.
	if err := os.Rename(dst, dst+".old"); err != nil {
		t.Fatal(err)
	}
	CleanStale(dst)
	if !IsFile(filepath.Join(dst, "new")) {
		t.Fatal("CleanStale did not restore dst")
	}
}
