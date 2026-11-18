// Package worktree manages the git worktrees an agent CLI creates under <repo>/<subdir>
// (Claude Code: .claude/worktrees/<name> on branch worktree-<name>).
package worktree

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"sprawling-agents/internal/proc"
)

var ErrInvalidName = errors.New("invalid worktree name")

// NamePattern is <type>-<slug>; Claude Code turns <name> into branch worktree-<name>.
var NamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,47}$`)

// reservedNames are `wt` subcommands.
var reservedNames = []string{"ls", "rm"}

func ValidateName(name string) error {
	if !NamePattern.MatchString(name) {
		return fmt.Errorf("%w %q (expected <type>-<slug>, e.g. feat-login)", ErrInvalidName, name)
	}
	if slices.Contains(reservedNames, name) {
		return fmt.Errorf("%w %q: it is a wt subcommand", ErrInvalidName, name)
	}
	return nil
}

func git(ctx context.Context, dir string, args ...string) (string, error) {
	out, err := proc.Output(ctx, proc.Options{}, "git", append([]string{"-C", dir}, args...)...)
	if err != nil {
		return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return strings.TrimSpace(string(out)), nil
}

// Repo is a repository's main checkout and where its agent worktrees live.
type Repo struct {
	Root   string
	Subdir string // relative to Root, e.g. .claude/worktrees
}

// Open finds the main checkout of the repository containing dir, even from inside a linked
// worktree. Symlinks are resolved so paths compare reliably.
func Open(ctx context.Context, dir, subdir string) (Repo, error) {
	common, err := git(ctx, dir, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		if ctx.Err() != nil {
			return Repo{}, err
		}
		return Repo{}, fmt.Errorf("not inside a git repository: %s", dir)
	}
	root, err := filepath.EvalSymlinks(filepath.Dir(common))
	if err != nil {
		return Repo{}, err
	}
	return Repo{Root: root, Subdir: subdir}, nil
}

// Base is the directory holding the agent worktrees.
func (r Repo) Base() string { return filepath.Join(r.Root, r.Subdir) }

func (r Repo) Dir(name string) string { return filepath.Join(r.Base(), name) }

type Worktree struct {
	Path   string
	Branch string // short name; "" when detached
}

// Name is the worktree's name when it lives under the agent dir.
func (r Repo) Name(w Worktree) (string, bool) {
	rel, err := filepath.Rel(r.Base(), w.Path)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") || strings.ContainsRune(rel, filepath.Separator) {
		return "", false
	}
	return rel, true
}

// List returns the repository's worktrees from `git worktree list --porcelain`.
func (r Repo) List(ctx context.Context) ([]Worktree, error) {
	out, err := git(ctx, r.Root, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}
	return parsePorcelain(out), nil
}

// Agents returns the worktrees under the agent dir.
func (r Repo) Agents(ctx context.Context) ([]Worktree, error) {
	all, err := r.List(ctx)
	if err != nil {
		return nil, err
	}
	var out []Worktree
	for _, w := range all {
		if _, ok := r.Name(w); ok {
			out = append(out, w)
		}
	}
	return out, nil
}

func parsePorcelain(out string) []Worktree {
	var list []Worktree
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "worktree "):
			p := strings.TrimPrefix(line, "worktree ")
			if resolved, err := filepath.EvalSymlinks(p); err == nil {
				p = resolved
			}
			list = append(list, Worktree{Path: p})
		case strings.HasPrefix(line, "branch ") && len(list) > 0:
			list[len(list)-1].Branch = strings.TrimPrefix(line, "branch refs/heads/")
		}
	}
	return list
}

// Dirty reports whether a worktree has uncommitted changes.
func Dirty(ctx context.Context, path string) (bool, error) {
	status, err := git(ctx, path, "status", "--porcelain")
	return status != "", err
}

// DefaultBase is the branch merged work lands on: origin/HEAD, else the first that exists of
// origin/main, origin/master, main, master.
func (r Repo) DefaultBase(ctx context.Context) (string, error) {
	if ref, err := git(ctx, r.Root, "symbolic-ref", "-q", "--short", "refs/remotes/origin/HEAD"); err == nil && ref != "" {
		return ref, nil
	}
	for _, ref := range []string{"origin/main", "origin/master", "main", "master"} {
		if _, err := git(ctx, r.Root, "rev-parse", "-q", "--verify", ref+"^{commit}"); err == nil {
			return ref, nil
		}
	}
	return "", errors.New("cannot determine the default branch (set it with: git remote set-head origin -a)")
}
