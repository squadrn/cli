package claude

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestProjectKey(t *testing.T) {
	dir := t.TempDir()
	real, _ := filepath.EvalSymlinks(dir)
	sub := filepath.Join(dir, ".claude", "worktrees", "feat-a")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := ProjectKey(sub)
	if err != nil {
		t.Fatal(err)
	}
	want := nonAlnum.ReplaceAllString(filepath.Join(real, ".claude", "worktrees", "feat-a"), "-")
	if got != want {
		t.Fatalf("ProjectKey = %q, want %q", got, want)
	}
	if k, _ := ProjectKey("/Users/x/.claude-personal"); runtime.GOOS != "windows" && k != "-Users-x--claude-personal" {
		t.Errorf("encoding = %q", k)
	}
}

func writeSession(t *testing.T, configDir, key, id string, mod time.Time) string {
	t.Helper()
	dir := filepath.Join(configDir, "projects", key)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, id+".jsonl")
	if err := os.WriteFile(p, []byte(`{"id":"`+id+`"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(p, mod, mod); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLatestSessionAcrossConfigDirs(t *testing.T) {
	root := t.TempDir()
	a, b := filepath.Join(root, "a"), filepath.Join(root, "b")
	old := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	writeSession(t, a, "k", "s1", old)
	writeSession(t, b, "k", "s2", old.Add(time.Hour))
	writeSession(t, a, "other", "s3", old.Add(2*time.Hour))

	s, err := LatestSession([]string{a, b}, "k")
	if err != nil || s.ID != "s2" || s.Owner != 1 {
		t.Fatalf("LatestSession = %+v, %v", s, err)
	}
	if _, err := LatestSession([]string{a, b}, "missing"); !errors.Is(err, ErrNoSession) {
		t.Fatalf("err = %v", err)
	}
}

func TestCopySessionKeepsContentTimesAndSubagents(t *testing.T) {
	root := t.TempDir()
	src, dst := filepath.Join(root, "src"), filepath.Join(root, "dst")
	mod := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	p := writeSession(t, src, "k", "s1", mod)
	if err := os.MkdirAll(filepath.Join(src, "projects", "k", "s1", "subagents"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "projects", "k", "s1", "subagents", "x.jsonl"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := CopySession(Session{ID: "s1", Path: p}, dst, "k"); err != nil {
		t.Fatal(err)
	}
	copied := filepath.Join(dst, "projects", "k", "s1.jsonl")
	b, err := os.ReadFile(copied)
	if err != nil || string(b) != `{"id":"s1"}` {
		t.Fatalf("copied = %q, %v", b, err)
	}
	if fi, _ := os.Stat(copied); !fi.ModTime().Equal(mod) {
		t.Errorf("mtime = %v, want %v", fi.ModTime(), mod)
	}
	if b, _ := os.ReadFile(filepath.Join(dst, "projects", "k", "s1", "subagents", "x.jsonl")); string(b) != "x" {
		t.Error("subagent transcript not copied")
	}
	if leftovers, _ := filepath.Glob(filepath.Join(dst, "projects", "k", "*.sprawl-tmp")); len(leftovers) > 0 {
		t.Errorf("temp files left: %v", leftovers)
	}
}
