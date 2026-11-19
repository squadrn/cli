package worktree

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"github.com/squadrn/squadrn/internal/inuse"
	"github.com/squadrn/squadrn/internal/proc"
)

type Action string

const (
	Keep           Action = "keep"
	Remove         Action = "remove"
	RemoveSquashed Action = "remove-squashed"
)

type Candidate struct {
	Worktree
	Action Action
	Reason string
	// InUse lists the sessions running in the worktree; such a worktree is kept.
	InUse []inuse.Entry
	// Ignored counts ignored paths (build output, node_modules) that removal deletes.
	Ignored int
}

// branchPrefix starts the branch of every agent worktree (Claude Code: worktree-<name>). squadrn
// only ever deletes branches named branchPrefix+<worktree name>.
const branchPrefix = "worktree-"

// InUse returns the sessions running in an agent worktree: those recorded in the worktree and
// those recorded in the main checkout for it before it existed (wt <name>).
func (r Repo) InUse(w Worktree) []inuse.Entry {
	out := inuse.Active(w.Path)
	name, ok := r.Name(w)
	if !ok {
		return out
	}
	for _, e := range inuse.Active(r.Root) {
		if e.Worktree == name {
			out = append(out, e)
		}
	}
	return out
}

// SquashMerged reports whether branch was merged through a PR (squash or rebase merges leave
// no ancestry for git to see). nil disables the check.
type SquashMerged func(ctx context.Context, branch string) bool

// Classify decides for every agent worktree whether gc may remove it. Only clean worktrees
// whose branch has nothing outside base (or whose PR was merged) are marked for removal.
func (r Repo) Classify(ctx context.Context, base string, squashed SquashMerged) ([]Candidate, error) {
	agents, err := r.Agents(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Candidate, 0, len(agents))
	for _, wt := range agents {
		c := Candidate{Worktree: wt, Action: Keep}
		if c.InUse = r.InUse(wt); len(c.InUse) > 0 {
			c.Reason = "in use"
			out = append(out, c)
			continue
		}
		switch dirty, err := Dirty(ctx, wt.Path); {
		case err != nil:
			c.Reason = "status unavailable"
		case dirty:
			c.Reason = "uncommitted changes"
		case wt.Branch == "":
			c.Reason = "detached HEAD"
		default:
			ahead, err := git(ctx, r.Root, "rev-list", "-n1", base+".."+wt.Branch)
			switch {
			case err != nil:
				c.Reason = "cannot compare with " + base
			case ahead == "":
				c.Action, c.Reason = Remove, "merged or no commits"
			case squashed != nil && squashed(ctx, wt.Branch):
				c.Action, c.Reason = RemoveSquashed, "PR merged (squash/rebase)"
			default:
				c.Reason = "unmerged commits"
			}
		}
		if c.Action != Keep {
			c.Ignored, _ = Ignored(ctx, wt.Path)
		}
		out = append(out, c)
	}
	return out, nil
}

// RemoveAll removes the classified worktrees and their branches, then prunes stale metadata.
// force lets git remove worktrees it otherwise refuses (those containing submodules); gc only
// marks clean worktrees, so nothing uncommitted is lost.
func (r Repo) RemoveAll(ctx context.Context, cands []Candidate, force bool) (removed int, errs []error) {
	for _, c := range cands {
		if c.Action == Keep {
			continue
		}
		if used := r.InUse(c.Worktree); len(used) > 0 {
			errs = append(errs, fmt.Errorf("%s: %w by pid %d", c.Path, ErrInUse, used[0].PID))
			continue
		}
		if err := r.Remove(ctx, c.Worktree, force, c.Action == RemoveSquashed); err != nil {
			errs = append(errs, err)
			continue
		}
		removed++
	}
	if _, err := git(ctx, r.Root, "worktree", "prune"); err != nil {
		errs = append(errs, err)
	}
	return removed, errs
}

var (
	// ErrUnmerged means a branch was kept because it has commits outside its upstream/HEAD.
	ErrUnmerged = errors.New("branch not fully merged")
	// ErrInUse means a session squadrn started still runs in the worktree.
	ErrInUse = errors.New("worktree in use")
)

// Find returns the agent worktree name.
func (r Repo) Find(ctx context.Context, name string) (Worktree, error) {
	agents, err := r.Agents(ctx)
	if err != nil {
		return Worktree{}, err
	}
	for _, w := range agents {
		if n, _ := r.Name(w); n == name {
			return w, nil
		}
	}
	return Worktree{}, fmt.Errorf("worktree not found: %s", r.Dir(name))
}

// Remove removes agent worktree w. Without forceTree a dirty worktree is refused; without
// forceBranch the branch is only deleted when merged (it is kept otherwise, reported as
// ErrUnmerged).
func (r Repo) Remove(ctx context.Context, w Worktree, forceTree, forceBranch bool) error {
	args := []string{"worktree", "remove", w.Path}
	if forceTree {
		args = []string{"worktree", "remove", "--force", w.Path}
	} else if dirty, err := Dirty(ctx, w.Path); err != nil || dirty {
		// git's own check honours status.showUntrackedFiles=no and would delete untracked files.
		return fmt.Errorf("%s has uncommitted changes or untracked files (or its status is unavailable)", w.Path)
	}
	if _, err := git(ctx, r.Root, args...); err != nil {
		return err
	}
	name, _ := r.Name(w)
	if w.Branch == "" || w.Branch != branchPrefix+name {
		return nil // not a branch the agent CLI made for this worktree: never delete it
	}
	flag := "-d"
	if forceBranch {
		flag = "-D" // not an ancestor of base, but its PR is merged (or the user forced it)
	}
	if _, err := git(ctx, r.Root, "branch", flag, w.Branch); err != nil {
		return fmt.Errorf("%w: kept branch %s (%w)", ErrUnmerged, w.Branch, err)
	}
	return nil
}

// Fetch refreshes origin so merge checks see the latest default branch. No origin is fine.
func (r Repo) Fetch(ctx context.Context) error {
	if r.originURL(ctx) == "" {
		return nil
	}
	_, err := git(ctx, r.Root, "fetch", "--prune", "--quiet", "origin")
	return err
}

// originURL is "" when the repository has no origin remote.
func (r Repo) originURL(ctx context.Context) string {
	url, err := git(ctx, r.Root, "remote", "get-url", "origin")
	if err != nil {
		return ""
	}
	return url
}

// GitHubSquashMerged asks gh about merged PRs, only for repositories hosted on GitHub.
func (r Repo) GitHubSquashMerged(ctx context.Context) SquashMerged {
	if !strings.Contains(r.originURL(ctx), "github.com") {
		return nil
	}
	gh, err := exec.LookPath("gh")
	if err != nil {
		return nil
	}
	return r.mergedPR(gh)
}

// mergedPR reports a branch as merged only when a merged PR's head was the branch's current
// tip or contains it: a branch name reused after its PR merged carries new commits the PR
// never had, and deleting it would lose them.
func (r Repo) mergedPR(gh string) SquashMerged {
	return func(ctx context.Context, branch string) bool {
		tip, err := git(ctx, r.Root, "rev-parse", "-q", "--verify", "refs/heads/"+branch+"^{commit}")
		if err != nil || tip == "" {
			return false
		}
		out, err := proc.Output(ctx, proc.Options{Dir: r.Root}, gh, "pr", "list", "--head", branch, "--state", "merged", "--json", "number,headRefOid", "--jq", ".[].headRefOid")
		if err != nil {
			return false
		}
		for _, oid := range strings.Fields(string(out)) {
			if oid == tip {
				return true
			}
			if _, err := git(ctx, r.Root, "cat-file", "-e", oid+"^{commit}"); err != nil {
				continue // the PR head is not here, so containment cannot be proven
			}
			if _, err := git(ctx, r.Root, "merge-base", "--is-ancestor", tip, oid); err == nil {
				return true
			}
		}
		return false
	}
}
