package worktree

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func run(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// newRepoWithOrigin returns a clone of a bare origin whose default branch is main.
func newRepoWithOrigin(t *testing.T) string {
	t.Helper()
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	for _, k := range []string{"GIT_AUTHOR_NAME", "GIT_COMMITTER_NAME"} {
		t.Setenv(k, "t")
	}
	for _, k := range []string{"GIT_AUTHOR_EMAIL", "GIT_COMMITTER_EMAIL"} {
		t.Setenv(k, "t@t")
	}
	root := t.TempDir()
	origin := filepath.Join(root, "origin.git")
	seed := filepath.Join(root, "seed")
	run(t, root, "init", "-q", "--bare", "-b", "main", origin)
	run(t, root, "init", "-q", "-b", "main", seed)
	run(t, seed, "commit", "-q", "--allow-empty", "-m", "init")
	run(t, seed, "push", "-q", origin, "main")
	repo := filepath.Join(root, "repo")
	run(t, root, "clone", "-q", origin, repo)
	resolved, err := filepath.EvalSymlinks(repo)
	if err != nil {
		t.Fatal(err)
	}
	return resolved
}

func TestGCWithOriginAndSquashMergedPR(t *testing.T) {
	ctx := context.Background()
	root := newRepoWithOrigin(t)
	repo := Repo{Root: root, Subdir: ".claude/worktrees"}
	run(t, root, "worktree", "add", "-q", "-b", "worktree-feat-squashed", ".claude/worktrees/feat-squashed")
	run(t, repo.Dir("feat-squashed"), "commit", "-q", "--allow-empty", "-m", "work")
	run(t, root, "worktree", "add", "-q", "-b", "worktree-feat-open", ".claude/worktrees/feat-open")
	run(t, repo.Dir("feat-open"), "commit", "-q", "--allow-empty", "-m", "work")

	if err := repo.Fetch(ctx); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	base, err := repo.DefaultBase(ctx)
	if err != nil || base != "origin/main" {
		t.Fatalf("DefaultBase = %q, %v", base, err)
	}
	if got, err := Open(ctx, repo.Dir("feat-open"), repo.Subdir); err != nil || got != repo {
		t.Fatalf("Open from a worktree = %+v, %v", got, err)
	}

	merged := func(_ context.Context, branch string) bool { return branch == "worktree-feat-squashed" }
	cands, err := repo.Classify(ctx, base, merged)
	if err != nil {
		t.Fatal(err)
	}
	actions := map[string]Action{}
	for _, c := range cands {
		actions[filepath.Base(c.Path)] = c.Action
	}
	if actions["feat-squashed"] != RemoveSquashed || actions["feat-open"] != Keep {
		t.Fatalf("actions = %v", actions)
	}

	removed, errs := repo.RemoveAll(ctx, cands, false)
	if len(removed) != 1 || removed[0] != "feat-squashed" || len(errs) != 0 {
		t.Fatalf("Apply = %v, %v", removed, errs)
	}
	if _, err := os.Stat(repo.Dir("feat-squashed")); !os.IsNotExist(err) {
		t.Error("squash-merged worktree still exists")
	}
	if err := exec.Command("git", "-C", root, "rev-parse", "--verify", "-q", "worktree-feat-squashed").Run(); err == nil {
		t.Error("squash-merged branch must be force-deleted (-D)")
	}
}

func TestGitHubSquashMergedOnlyForGitHub(t *testing.T) {
	repo := Repo{Root: newRepoWithOrigin(t)}
	if repo.GitHubSquashMerged(context.Background()) != nil {
		t.Fatal("a non-GitHub origin must not query gh")
	}
}

func TestDefaultBaseWithoutAnyBranch(t *testing.T) {
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	dir := t.TempDir()
	run(t, dir, "init", "-q", "-b", "trunk", dir)
	if _, err := (Repo{Root: dir}).DefaultBase(context.Background()); err == nil {
		t.Fatal("expected an error for a repository without main/master")
	}
}

func TestRemoveAndAgents(t *testing.T) {
	ctx := context.Background()
	root := newRepoWithOrigin(t)
	repo := Repo{Root: root, Subdir: ".claude/worktrees"}
	run(t, root, "worktree", "add", "-q", "-b", "worktree-feat-a", ".claude/worktrees/feat-a")
	run(t, root, "worktree", "add", "-q", "-b", "worktree-feat-b", ".claude/worktrees/feat-b")
	run(t, repo.Dir("feat-b"), "commit", "-q", "--allow-empty", "-m", "work")
	run(t, root, "worktree", "add", "-q", "-b", "elsewhere", "../elsewhere")
	agents, err := repo.Agents(ctx)
	if err != nil || len(agents) != 2 {
		t.Fatalf("Agents = %+v, %v", agents, err)
	}
	if err := os.WriteFile(filepath.Join(repo.Dir("feat-a"), "x"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if dirty, _ := Dirty(ctx, repo.Dir("feat-a")); !dirty {
		t.Fatal("Dirty = false")
	}
	if err := removeNamed(t, repo, "feat-a", false, false); err == nil {
		t.Fatal("removed a dirty worktree without force")
	}
	if err := removeNamed(t, repo, "feat-a", true, true); err != nil {
		t.Fatal(err)
	}
	if err := removeNamed(t, repo, "feat-b", false, false); !errors.Is(err, ErrUnmerged) {
		t.Fatalf("unmerged branch: %v", err)
	}
	if err := removeNamed(t, repo, "nope", false, false); err == nil {
		t.Fatal("missing worktree: no error")
	}
	if _, err := Open(ctx, t.TempDir(), ""); err == nil {
		t.Fatal("Open outside a repository")
	}
}

// removeNamed removes the agent worktree name the way `wt rm` does: find it, then remove it.
func removeNamed(t *testing.T, repo Repo, name string, forceTree, forceBranch bool) error {
	t.Helper()
	w, err := repo.Find(context.Background(), name)
	if err != nil {
		return err
	}
	return repo.Remove(context.Background(), w, forceTree, forceBranch)
}
