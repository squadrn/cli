// Package worktree manages the git worktrees an agent CLI creates under <repo>/<subdir>
// (Claude Code: .claude/worktrees/<name> on branch worktree-<name>).
package worktree

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/squadrn/cli/internal/fsx"
	"github.com/squadrn/cli/internal/inuse"
	"github.com/squadrn/cli/internal/proc"
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
// worktree, a submodule or a repository whose git dir lives elsewhere. Symlinks are resolved so
// paths compare reliably.
func Open(ctx context.Context, dir, subdir string) (Repo, error) {
	common, err := git(ctx, dir, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		if ctx.Err() != nil {
			return Repo{}, err
		}
		return Repo{}, fmt.Errorf("not inside a git repository: %s", dir)
	}
	root, err := mainWorktree(ctx, dir, common)
	if err != nil {
		return Repo{}, err
	}
	if root, err = filepath.EvalSymlinks(root); err != nil {
		return Repo{}, err
	}
	return Repo{Root: root, Subdir: subdir}, nil
}

// mainWorktree is the first entry of `git worktree list`, which is the main checkout except
// when git can only name the git dir: a submodule (its work tree is core.worktree), a
// --separate-git-dir repository (known only from inside its main checkout) and a bare
// repository (agent worktrees then live in the current linked worktree).
func mainWorktree(ctx context.Context, dir, common string) (string, error) {
	out, err := git(ctx, dir, "worktree", "list", "--porcelain")
	if err != nil {
		return "", err
	}
	first, _, _ := strings.Cut(out, "\n\n")
	main := parsePorcelain(first)
	bare := slices.Contains(strings.Split(first, "\n"), "bare")
	commonReal, _ := filepath.EvalSymlinks(common)
	switch {
	case bare:
		top, err := git(ctx, dir, "rev-parse", "--show-toplevel")
		if err != nil || top == "" {
			return "", fmt.Errorf("%s is a bare repository; run this from one of its worktrees", common)
		}
		return top, nil
	case len(main) == 1 && main[0].Path != commonReal && main[0].Path != common:
		return main[0].Path, nil
	}
	if wt, err := git(ctx, dir, "--git-dir="+common, "config", "--get", "core.worktree"); err == nil && wt != "" {
		if !filepath.IsAbs(wt) {
			wt = filepath.Join(common, wt)
		}
		return filepath.Clean(wt), nil
	}
	gitDir, err := git(ctx, dir, "rev-parse", "--path-format=absolute", "--git-dir")
	if err == nil && gitDir == common {
		if top, err := git(ctx, dir, "rev-parse", "--show-toplevel"); err == nil && top != "" {
			return top, nil
		}
	}
	return "", fmt.Errorf("cannot find the main checkout of the repository at %s (its git dir is separate); run this from the main checkout", common)
}

// CommonDir is the repository's shared git dir (.git of the main checkout).
func (r Repo) CommonDir(ctx context.Context) (string, error) {
	return git(ctx, r.Root, "rev-parse", "--path-format=absolute", "--git-common-dir")
}

// Toplevel is the root of the worktree containing dir ("" outside a repository).
func Toplevel(ctx context.Context, dir string) string {
	top, err := git(ctx, dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return ""
	}
	if real, err := filepath.EvalSymlinks(top); err == nil {
		return real
	}
	return top
}

// Exclude adds pattern to the repository's info/exclude (shared by all its worktrees) unless
// it is already there, so git never reports the files squadrn keeps in a worktree.
func Exclude(ctx context.Context, dir, pattern string) error {
	path, err := git(ctx, dir, "rev-parse", "--path-format=absolute", "--git-path", "info/exclude")
	if err != nil {
		return err
	}
	b, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if slices.Contains(strings.Split(strings.ReplaceAll(string(b), "\r", ""), "\n"), pattern) {
		return nil
	}
	if len(b) > 0 && !bytes.HasSuffix(b, []byte("\n")) {
		b = append(b, '\n')
	}
	return fsx.WriteBytes(path, append(b, pattern+"\n"...), 0o644)
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

// Dirty reports whether a worktree has uncommitted changes, untracked files included whatever
// status.showUntrackedFiles says, and changes inside submodules.
func Dirty(ctx context.Context, path string) (bool, error) {
	status, err := git(ctx, path, "status", "--porcelain", "--untracked-files=all", "--ignore-submodules=none")
	return status != "", err
}

// Ignored counts the ignored paths in a worktree (an ignored directory counts once). Removing
// the worktree deletes them; squadrn's own registry is not counted.
func Ignored(ctx context.Context, path string) (int, error) {
	status, err := git(ctx, path, "status", "--porcelain", "--untracked-files=normal", "--ignored")
	if err != nil {
		return 0, err
	}
	n := 0
	for _, line := range strings.Split(status, "\n") {
		p, ok := strings.CutPrefix(line, "!! ")
		if d := strings.TrimSuffix(p, "/"); ok && d != inuse.Dir && !slices.Contains(inuse.LegacyDirs(), d) {
			n++
		}
	}
	return n, nil
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
