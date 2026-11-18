package fsx

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
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
	if err != nil || runtime.GOOS != "windows" && fi.Mode().Perm() != 0o600 {
		t.Fatalf("stat = %v, %v", fi, err)
	}
	if fi, _ := os.Stat(filepath.Dir(path)); runtime.GOOS != "windows" && fi.Mode().Perm() != 0o700 {
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
	if err != nil || !fi.ModTime().Equal(mod) || runtime.GOOS != "windows" && fi.Mode().Perm() != 0o755 {
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

func TestSameFileAndReplaceExecutable(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a")
	if err := os.WriteFile(a, []byte("v1"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(a, link); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(dir, "other")
	if err := os.WriteFile(other, []byte("v1"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !SameFile(a, link) || SameFile(a, other) || SameFile(a, filepath.Join(dir, "missing")) || SameFile(filepath.Join(dir, "missing"), a) {
		t.Fatal("SameFile")
	}
	dst := filepath.Join(dir, "bin", "tool")
	if err := ReplaceExecutable(a, dst); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(dst); string(b) != "v1" {
		t.Fatalf("content %q", b)
	}
	if fi, _ := os.Stat(dst); runtime.GOOS != "windows" && fi.Mode().Perm()&0o111 == 0 {
		t.Fatalf("not executable: %v", fi.Mode())
	}
}

func TestReplaceDirRecovery(t *testing.T) {
	base := t.TempDir()
	src, dst := filepath.Join(base, "src"), filepath.Join(base, "dst")
	if err := os.MkdirAll(filepath.Join(src, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "sub", "a"), []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dst, "stale"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ReplaceDir(src, dst, 0o755); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(dst, "sub", "a")); string(b) != "new" || IsFile(filepath.Join(dst, "stale")) {
		t.Fatalf("ReplaceDir result: %q", b)
	}
	if IsDir(dst+".new") || IsDir(dst+".old") || !IsDir(dst) || IsDir(filepath.Join(dst, "sub", "a")) {
		t.Fatal("leftovers or wrong kinds")
	}
	// A swap that died between the renames: dst is missing and .old holds it.
	if err := os.Rename(dst, dst+".old"); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dst+".new", 0o755); err != nil {
		t.Fatal(err)
	}
	CleanStale(dst)
	if !IsDir(dst) || IsDir(dst+".old") || IsDir(dst+".new") {
		t.Fatal("CleanStale did not restore the previous tree")
	}
	if err := ReplaceDir(filepath.Join(base, "missing"), dst, 0o755); err == nil || !IsDir(dst) {
		t.Fatalf("a failed copy must keep dst: %v", err)
	}
	// A new tree into a destination that does not exist yet.
	fresh := filepath.Join(base, "fresh")
	if err := ReplaceDir(src, fresh, 0o755); err != nil || !IsFile(filepath.Join(fresh, "sub", "a")) {
		t.Fatalf("fresh ReplaceDir: %v", err)
	}
}

func TestCleanOld(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	stale := now.Add(-time.Hour)
	for n, mod := range map[string]time.Time{
		"tool.exe.old-1": now, "tool.exe.new-2": stale, "keep": stale,
		"other.exe.old-1": stale, "other.exe.new-1": stale, "tool.exe.new-3": now,
	} {
		p := filepath.Join(dir, n)
		if err := os.WriteFile(p, nil, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, mod, mod); err != nil {
			t.Fatal(err)
		}
	}
	got := Leftovers(dir, []string{"TOOL.exe"}, now)
	want := []string{filepath.Join(dir, "tool.exe.new-2"), filepath.Join(dir, "tool.exe.old-1")}
	if !slices.Equal(got, want) {
		t.Fatalf("Leftovers = %v, want %v", got, want)
	}
	if Leftovers(filepath.Join(dir, "missing"), []string{"tool.exe"}, now) != nil {
		t.Fatal("leftovers in a missing dir")
	}
	if _, ok := leftoverKind("tool.exe.old-", []string{"tool.exe"}); ok {
		t.Fatal("a name without a stamp counted as a leftover")
	}
	CleanOld(dir, []string{"tool.exe"})
	for _, n := range []string{"keep", "other.exe.old-1", "other.exe.new-1", "tool.exe.new-3"} {
		if !IsFile(filepath.Join(dir, n)) {
			t.Fatalf("CleanOld removed %s, which sprawl does not own or is in flight", n)
		}
	}
	if runtime.GOOS == "windows" && (IsFile(filepath.Join(dir, "tool.exe.old-1")) || IsFile(filepath.Join(dir, "tool.exe.new-2"))) {
		t.Fatal("CleanOld left replaced executables")
	}
}

func TestCopyFileErrors(t *testing.T) {
	dir := t.TempDir()
	if err := CopyFile(filepath.Join(dir, "missing"), filepath.Join(dir, "x"), 0o644, false); err == nil {
		t.Fatal("copied a missing file")
	}
	if err := CopyFile(dir, filepath.Join(dir, "x"), 0o644, false); err == nil {
		t.Fatal("copied a directory")
	}
	if err := CopyTree(filepath.Join(dir, "missing"), filepath.Join(dir, "y"), 0o755, false); err == nil {
		t.Fatal("copied a missing tree")
	}
}

func TestAppendLine(t *testing.T) {
	p := filepath.Join(t.TempDir(), "d", "log.jsonl")
	for _, l := range []string{"a", "b"} {
		if err := AppendLine(p, []byte(l), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if b, _ := os.ReadFile(p); string(b) != "a\nb\n" {
		t.Fatalf("content = %q", b)
	}
	if err := AppendLine(filepath.Join(p, "x"), []byte("c"), 0o600); err == nil {
		t.Fatal("append under a file succeeded")
	}
}

func TestMoveDir(t *testing.T) {
	root := t.TempDir()
	src, dst := filepath.Join(root, "src"), filepath.Join(root, "dst")
	for _, f := range []string{filepath.Join(src, "new"), filepath.Join(dst, "old")} {
		if err := os.MkdirAll(filepath.Dir(f), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(f, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := MoveDir(src, dst); err != nil {
		t.Fatal(err)
	}
	if !IsFile(filepath.Join(dst, "new")) || IsFile(filepath.Join(dst, "old")) || IsDir(src) {
		t.Fatal("MoveDir did not replace dst")
	}
	if err := MoveDir(filepath.Join(root, "missing"), dst); err == nil || !IsFile(filepath.Join(dst, "new")) {
		t.Fatal("a missing source must fail and keep dst")
	}
}
