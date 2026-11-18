package worktree

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestValidateName(t *testing.T) {
	for _, ok := range []string{"feat-a", "fix-otp-timeout", "0day", "a"} {
		if err := ValidateName(ok); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "Feat", "feat_a", "-x", "a/b", "pr-#12", "ls", "rm", string(make([]byte, 49))} {
		if err := ValidateName(bad); !errors.Is(err, ErrInvalidName) {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestParsePorcelain(t *testing.T) {
	out := "worktree /r\nHEAD abc\nbranch refs/heads/main\n\nworktree /r/.claude/worktrees/feat-a\nHEAD def\nbranch refs/heads/worktree-feat-a\n\nworktree /r/.claude/worktrees/x\nHEAD 123\ndetached\n"
	got := parsePorcelain(out)
	want := []Worktree{{"/r", "main"}, {"/r/.claude/worktrees/feat-a", "worktree-feat-a"}, {"/r/.claude/worktrees/x", ""}}
	if !slices.Equal(got, want) {
		t.Fatalf("parsePorcelain = %+v", got)
	}
}

func TestInstallCommandPriority(t *testing.T) {
	dir := t.TempDir()
	if _, err := InstallCommand(dir); err == nil {
		t.Fatal("expected an error without a lockfile")
	}
	for _, f := range []string{"package-lock.json", "yarn.lock", "bun.lockb", "pnpm-lock.yaml"} {
		if err := os.WriteFile(filepath.Join(dir, f), nil, 0o600); err != nil {
			t.Fatal(err)
		}
		argv, err := InstallCommand(dir)
		if err != nil {
			t.Fatal(err)
		}
		want := map[string]string{"package-lock.json": "npm", "yarn.lock": "yarn", "bun.lockb": "bun", "pnpm-lock.yaml": "pnpm"}[f]
		if argv[0] != want {
			t.Errorf("after adding %s: %v, want %s first", f, argv, want)
		}
	}
}
