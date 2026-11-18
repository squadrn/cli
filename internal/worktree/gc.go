package worktree

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"sprawling-agents/internal/proc"
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
		out = append(out, c)
	}
	return out, nil
}

// RemoveAll removes the classified worktrees and their branches, then prunes stale metadata.
func (r Repo) RemoveAll(ctx context.Context, cands []Candidate) (removed int, errs []error) {
	for _, c := range cands {
		if c.Action == Keep {
			continue
		}
		if err := r.remove(ctx, c.Worktree, c.Action == RemoveSquashed, false); err != nil {
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

// ErrUnmerged means a branch was kept because it has commits outside its upstream/HEAD.
var ErrUnmerged = errors.New("branch not fully merged")

// RemoveOne removes the agent worktree name. Without force, git refuses a dirty worktree and the
// branch is only deleted when merged (it is kept otherwise, reported as ErrUnmerged).
func (r Repo) RemoveOne(ctx context.Context, name string, force bool) error {
	agents, err := r.Agents(ctx)
	if err != nil {
		return err
	}
	for _, w := range agents {
		if n, _ := r.Name(w); n == name {
			return r.remove(ctx, w, force, force)
		}
	}
	return fmt.Errorf("worktree not found: %s", r.Dir(name))
}

func (r Repo) remove(ctx context.Context, w Worktree, forceBranch, forceTree bool) error {
	args := []string{"worktree", "remove", w.Path}
	if forceTree {
		args = []string{"worktree", "remove", "--force", w.Path}
	}
	if _, err := git(ctx, r.Root, args...); err != nil {
		return err
	}
	if w.Branch == "" {
		return nil
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
	return func(ctx context.Context, branch string) bool {
		out, err := proc.Output(ctx, proc.Options{Dir: r.Root}, gh, "pr", "list", "--head", branch, "--state", "merged", "--json", "number", "--jq", "length")
		n := strings.TrimSpace(string(out))
		return err == nil && n != "" && n != "0"
	}
}
