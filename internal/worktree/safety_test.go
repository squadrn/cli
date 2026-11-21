package worktree

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/squadrn/squadrn-cli/internal/inuse"
)

func revParse(t *testing.T, dir, rev string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", dir, "rev-parse", rev).Output()
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(out))
}

// fakeGH writes a gh that prints the given PR head oids, one per line.
func fakeGH(t *testing.T, oids ...string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell-script fake gh")
	}
	gh := filepath.Join(t.TempDir(), "gh")
	script := "#!/bin/sh\ncat <<'X'\n" + strings.Join(oids, "\n") + "\nX\n"
	if err := os.WriteFile(gh, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return gh
}

func TestMergedPRNeedsTheBranchTip(t *testing.T) {
	ctx := context.Background()
	root := newRepoWithOrigin(t)
	repo := Repo{Root: root, Subdir: ".claude/worktrees"}
	run(t, root, "worktree", "add", "-q", "-b", "worktree-feat-a", ".claude/worktrees/feat-a")
	wt := repo.Dir("feat-a")
	run(t, wt, "commit", "-q", "--allow-empty", "-m", "one")
	merged := revParse(t, wt, "HEAD")

	if !repo.mergedPR(fakeGH(t, merged))(ctx, "worktree-feat-a") {
		t.Fatal("tip == merged PR head: not squash-merged")
	}
	run(t, wt, "commit", "-q", "--allow-empty", "-m", "two")
	head := revParse(t, wt, "HEAD")
	run(t, wt, "reset", "-q", "--hard", "HEAD~1")
	if repo.mergedPR(fakeGH(t, head))(ctx, "worktree-feat-a") != true {
		t.Fatal("tip is an ancestor of the merged PR head: not squash-merged")
	}
	run(t, wt, "commit", "-q", "--allow-empty", "-m", "new work after the merge")
	if repo.mergedPR(fakeGH(t, merged))(ctx, "worktree-feat-a") {
		t.Fatal("a reused branch with new commits was classified as merged")
	}
	if repo.mergedPR(fakeGH(t, "0123456789abcdef0123456789abcdef01234567"))(ctx, "worktree-feat-a") {
		t.Fatal("a PR head missing locally was trusted")
	}
	if repo.mergedPR(fakeGH(t))(ctx, "worktree-feat-a") || repo.mergedPR(fakeGH(t, merged))(ctx, "no-such-branch") {
		t.Fatal("no PR or no branch: merged")
	}
}

func TestDirtyIgnoresStatusConfigAndCountsIgnored(t *testing.T) {
	ctx := context.Background()
	root := newRepoWithOrigin(t)
	run(t, root, "config", "status.showUntrackedFiles", "no")
	if err := os.WriteFile(filepath.Join(root, "new.txt"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if dirty, err := Dirty(ctx, root); err != nil || !dirty {
		t.Fatalf("untracked file hidden by status.showUntrackedFiles=no: dirty=%v, %v", dirty, err)
	}
	if err := os.Remove(filepath.Join(root, "new.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".git", "info", "exclude"), []byte("build/\n*.log\n"+inuse.Dir+"/\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"build/a", "build/b", "x.log", inuse.Dir + "/1.json"} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(root, f)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, f), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if dirty, _ := Dirty(ctx, root); dirty {
		t.Fatal("ignored files made the worktree dirty")
	}
	if n, err := Ignored(ctx, root); err != nil || n != 2 {
		t.Fatalf("Ignored = %d, %v; want 2 (build/, x.log)", n, err)
	}
}

func TestOpenSubmoduleBareAndSeparateGitDir(t *testing.T) {
	ctx := context.Background()
	base := filepath.Dir(newRepoWithOrigin(t))
	real := func(p string) string {
		r, err := filepath.EvalSymlinks(p)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}

	run(t, base, "init", "-q", "-b", "main", "sub")
	run(t, filepath.Join(base, "sub"), "commit", "-q", "--allow-empty", "-m", "s")
	run(t, base, "init", "-q", "-b", "main", "super")
	super := filepath.Join(base, "super")
	run(t, super, "commit", "-q", "--allow-empty", "-m", "s")
	run(t, super, "-c", "protocol.file.allow=always", "submodule", "add", "-q", "../sub", "x")
	sub := filepath.Join(super, "x")
	if r, err := Open(ctx, sub, ""); err != nil || r.Root != real(sub) {
		t.Fatalf("submodule: %+v, %v", r, err)
	}
	run(t, sub, "worktree", "add", "-q", "-b", "wt-x", filepath.Join(base, "subwt"))
	if r, err := Open(ctx, filepath.Join(base, "subwt"), ""); err != nil || r.Root != real(sub) {
		t.Fatalf("worktree of a submodule: %+v, %v", r, err)
	}

	run(t, base, "init", "-q", "--bare", "-b", "main", "bare.git")
	run(t, filepath.Join(base, "sub"), "push", "-q", filepath.Join(base, "bare.git"), "main")
	run(t, filepath.Join(base, "bare.git"), "worktree", "add", "-q", filepath.Join(base, "barewt"), "main")
	if r, err := Open(ctx, filepath.Join(base, "barewt"), ""); err != nil || r.Root != real(filepath.Join(base, "barewt")) {
		t.Fatalf("linked worktree of a bare repository: %+v, %v", r, err)
	}
	if _, err := Open(ctx, filepath.Join(base, "bare.git"), ""); err == nil || !strings.Contains(err.Error(), "bare repository") {
		t.Fatalf("bare repository itself: %v", err)
	}

	run(t, base, "init", "-q", "-b", "main", "--separate-git-dir="+filepath.Join(base, "sep.git"), "sepwt")
	sep := filepath.Join(base, "sepwt")
	run(t, sep, "commit", "-q", "--allow-empty", "-m", "s")
	if r, err := Open(ctx, sep, ""); err != nil || r.Root != real(sep) {
		t.Fatalf("separate git dir: %+v, %v", r, err)
	}
	run(t, sep, "worktree", "add", "-q", "-b", "other", filepath.Join(base, "sepother"))
	if _, err := Open(ctx, filepath.Join(base, "sepother"), ""); err == nil || !strings.Contains(err.Error(), "main checkout") {
		t.Fatalf("linked worktree of a separate git dir: %v", err)
	}
}

func TestGCKeepsInUseAndForeignBranches(t *testing.T) {
	ctx := context.Background()
	root := newRepoWithOrigin(t)
	repo := Repo{Root: root, Subdir: ".claude/worktrees"}
	run(t, root, "worktree", "add", "-q", "-b", "worktree-feat-used", ".claude/worktrees/feat-used")
	run(t, root, "worktree", "add", "-q", "-b", "worktree-feat-pending", ".claude/worktrees/feat-pending")
	run(t, root, "worktree", "add", "-q", "-b", "mine", ".claude/worktrees/feat-mine")
	if err := Exclude(ctx, root, inuse.Dir+"/"); err != nil {
		t.Fatal(err)
	}
	if err := Exclude(ctx, root, inuse.Dir+"/"); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(root, ".git", "info", "exclude"))
	if strings.Count(string(b), inuse.Dir) != 1 {
		t.Fatalf("exclude = %q", b)
	}
	if err := inuse.Acquire(repo.Dir("feat-used"), inuse.Entry{Account: "claude/x"}); err != nil {
		t.Fatal(err)
	}
	if err := inuse.Acquire(root, inuse.Entry{Worktree: "feat-pending"}); err != nil {
		t.Fatal(err)
	}
	cands, err := repo.Classify(ctx, "origin/main", nil)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]Candidate{}
	for _, c := range cands {
		got[filepath.Base(c.Path)] = c
	}
	if c := got["feat-used"]; c.Action != Keep || len(c.InUse) != 1 || c.InUse[0].Account != "claude/x" {
		t.Fatalf("feat-used = %+v", c)
	}
	if c := got["feat-pending"]; c.Action != Keep || !strings.HasPrefix(c.Reason, "in use") {
		t.Fatalf("feat-pending = %+v", c)
	}
	if got["feat-mine"].Action != Remove {
		t.Fatalf("feat-mine = %+v", got["feat-mine"])
	}
	// Marked for removal, then a session starts: RemoveAll re-checks.
	forced := []Candidate{{Worktree: got["feat-used"].Worktree, Action: Remove}, got["feat-mine"]}
	removed, errs := repo.RemoveAll(ctx, forced, false)
	if len(removed) != 1 || len(errs) != 1 || !errors.Is(errs[0], ErrInUse) {
		t.Fatalf("RemoveAll = %v, %v", removed, errs)
	}
	if err := exec.Command("git", "-C", root, "rev-parse", "--verify", "-q", "refs/heads/mine").Run(); err != nil {
		t.Fatal("a branch not named worktree-<name> was deleted")
	}
	if top := Toplevel(ctx, repo.Dir("feat-used")); top != repo.Dir("feat-used") {
		t.Fatalf("Toplevel = %q", top)
	}
	if Toplevel(ctx, t.TempDir()) != "" {
		t.Fatal("Toplevel outside a repository")
	}
	if d, err := repo.CommonDir(ctx); err != nil || filepath.Base(d) != ".git" {
		t.Fatalf("CommonDir = %q, %v", d, err)
	}
	_ = inuse.Release(repo.Dir("feat-used"))
	_ = inuse.Release(root)
}

func TestRemoveForceFlagsAreSeparate(t *testing.T) {
	root := newRepoWithOrigin(t)
	repo := Repo{Root: root, Subdir: ".claude/worktrees"}
	run(t, root, "worktree", "add", "-q", "-b", "worktree-feat-a", ".claude/worktrees/feat-a")
	run(t, repo.Dir("feat-a"), "commit", "-q", "--allow-empty", "-m", "work")
	if err := os.WriteFile(filepath.Join(repo.Dir("feat-a"), "x"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := removeNamed(t, repo, "feat-a", true, false); !errors.Is(err, ErrUnmerged) {
		t.Fatalf("--force without --force-branch: %v", err)
	}
	if err := exec.Command("git", "-C", root, "rev-parse", "--verify", "-q", "refs/heads/worktree-feat-a").Run(); err != nil {
		t.Fatal("unmerged branch deleted without --force-branch")
	}
}
