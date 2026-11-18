package githook

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

func TestInstall(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "hooks")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(dir, "reference-transaction")
	if err := os.WriteFile(stale, []byte("#!/bin/sh\n"+marker+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	foreign := filepath.Join(dir, "mine")
	if err := os.WriteFile(foreign, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	const bin, cfg = "/opt/it's/sprawl", "/home/u/.config/sprawl/config.toml"
	if Installed(dir, bin, cfg) {
		t.Fatal("Installed before Install")
	}
	if err := Install(dir, bin, cfg); err != nil {
		t.Fatal(err)
	}
	if !Installed(dir, bin, cfg) || Installed(dir, "/other", cfg) || Installed(dir, bin, "/other.toml") {
		t.Fatal("Installed")
	}
	b, err := os.ReadFile(filepath.Join(dir, "pre-push"))
	if err != nil || !strings.Contains(string(b), `exec '/opt/it'\''s/sprawl' hook git --config '/home/u/.config/sprawl/config.toml' pre-push "$@"`) {
		t.Fatalf("pre-push shim = %q, %v", b, err)
	}
	// A shim that was edited, or lost its execute bit, is not the guard.
	commitMsg := filepath.Join(dir, "commit-msg")
	if err := os.WriteFile(commitMsg, append(shim("commit-msg", bin, cfg), '#'), 0o755); err != nil {
		t.Fatal(err)
	}
	if Installed(dir, bin, cfg) {
		t.Fatal("edited shim accepted")
	}
	if err := os.WriteFile(commitMsg, shim("commit-msg", bin, cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(commitMsg, 0o644); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && Installed(dir, bin, cfg) {
		t.Fatal("shim without execute bit accepted")
	}
	if err := Install(dir, bin, cfg); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(filepath.Join(dir, "commit-msg")); runtime.GOOS != "windows" && fi.Mode().Perm() != 0o755 {
		t.Errorf("shim mode = %v", fi.Mode())
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Error("stale shim kept")
	}
	if _, err := os.Stat(foreign); err != nil {
		t.Error("foreign file removed")
	}
}

func TestEnv(t *testing.T) {
	got := Env([]string{"PATH=/bin"}, "/h")
	want := []string{"PATH=/bin", "GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=core.hooksPath", "GIT_CONFIG_VALUE_0=/h"}
	if !slices.Equal(got, want) {
		t.Fatalf("Env = %v", got)
	}
	if again := Env(got, "/h"); !slices.Equal(again, got) {
		t.Fatalf("Env is not idempotent: %v", again)
	}
	user := []string{"GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=user.name", "GIT_CONFIG_VALUE_0=x"}
	got = Env(user, "/h")
	want = []string{"GIT_CONFIG_KEY_0=user.name", "GIT_CONFIG_VALUE_0=x", "GIT_CONFIG_COUNT=2", "GIT_CONFIG_KEY_1=core.hooksPath", "GIT_CONFIG_VALUE_1=/h"}
	if !slices.Equal(got, want) {
		t.Fatalf("Env with user entries = %v", got)
	}
	if got := Env([]string{"GIT_CONFIG_COUNT=junk"}, "/h"); !slices.Contains(got, "GIT_CONFIG_COUNT=1") {
		t.Fatalf("Env with junk count = %v", got)
	}
}

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func TestRepoHook(t *testing.T) {
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	ctx := context.Background()
	repo := t.TempDir()
	git(t, repo, "init", "-q")
	ours := t.TempDir()
	for _, k := range []string{"GIT_CONFIG_COUNT", "GIT_CONFIG_KEY_0", "GIT_CONFIG_VALUE_0"} {
		t.Setenv(k, "")
	}
	for _, kv := range Env(nil, ours) {
		k, v, _ := strings.Cut(kv, "=")
		t.Setenv(k, v)
	}
	if p, err := RepoHook(ctx, repo, ours, "pre-commit"); err != nil || p != "" {
		t.Fatalf("no hook: %q, %v", p, err)
	}
	def := filepath.Join(repo, ".git", "hooks", "pre-commit")
	if err := os.WriteFile(def, []byte("#!/bin/sh\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Git for Windows has no execute bits: a #! script runs regardless.
	if p, _ := RepoHook(ctx, repo, ours, "pre-commit"); runtime.GOOS != "windows" && p != "" {
		t.Fatalf("non-executable hook returned: %q", p)
	}
	if err := os.Chmod(def, 0o755); err != nil {
		t.Fatal(err)
	}
	if p, err := RepoHook(ctx, repo, ours, "pre-commit"); err != nil || !strings.HasSuffix(filepath.ToSlash(p), ".git/hooks/pre-commit") {
		t.Fatalf("default hooks dir: %q, %v", p, err)
	}
	husky := filepath.Join(repo, ".husky")
	if err := os.MkdirAll(husky, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(husky, "pre-commit"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	git(t, repo, "config", "core.hooksPath", ".husky")
	if p, err := RepoHook(ctx, repo, ours, "pre-commit"); err != nil || p != filepath.Join(repo, ".husky", "pre-commit") {
		t.Fatalf("repo core.hooksPath: %q, %v", p, err)
	}
	if p, _ := RepoHook(ctx, repo, ours, "commit-msg"); p != "" {
		t.Fatalf("missing hook: %q", p)
	}
	git(t, repo, "config", "core.hooksPath", ours)
	if p, _ := RepoHook(ctx, repo, ours, "pre-commit"); !strings.HasSuffix(filepath.ToSlash(p), ".git/hooks/pre-commit") {
		t.Fatalf("our own dir is never chained to: %q", p)
	}
	if _, err := RepoHook(ctx, t.TempDir(), ours, "pre-commit"); err == nil {
		t.Fatal("outside a repository: no error")
	}
}
