package guard

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strings"
)

// RefUpdate is one line git passes to a pre-push hook on stdin.
type RefUpdate struct {
	LocalRef, LocalSHA, RemoteRef, RemoteSHA string
}

// ParsePrePush reads pre-push hook input: "<local ref> <local sha> <remote ref> <remote sha>".
func ParsePrePush(r io.Reader) ([]RefUpdate, error) {
	var out []RefUpdate
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) == 0 {
			continue
		}
		if len(f) != 4 {
			return nil, fmt.Errorf("unexpected pre-push input %q", sc.Text())
		}
		out = append(out, RefUpdate{f[0], f[1], f[2], f[3]})
	}
	return out, sc.Err()
}

func isZeroSHA(s string) bool { return strings.Trim(s, "0") == "" }

// DecidePrePush judges the exact ref updates of a push (layer 2). It never asks: git hooks have
// no way to prompt, so anything that cannot be shown safe is denied.
func (g Guard) DecidePrePush(ctx context.Context, dir string, updates []RefUpdate) Decision {
	facts := newFactsCache(g.Git)
	f, err := facts.get(ctx, dir)
	if err != nil {
		return deny("could not read the repository to check this push: " + err.Error())
	}
	protected := protectedSet(g.Protected, f)
	for _, u := range updates {
		branch, isBranch := branchName(u.RemoteRef)
		if !isBranch {
			continue
		}
		if protected.match(branch) {
			if isZeroSHA(u.LocalSHA) {
				return deny(fmt.Sprintf("deleting protected branch %q is not allowed", branch))
			}
			return deny(fmt.Sprintf("direct push to protected branch %q is not allowed; open a PR", branch))
		}
		if isZeroSHA(u.LocalSHA) || isZeroSHA(u.RemoteSHA) {
			continue // deleting or creating a non-protected branch
		}
		// A remote commit missing locally cannot be an ancestor: the push needs force anyway.
		ff, err := g.Git.IsAncestor(ctx, dir, u.RemoteSHA, u.LocalSHA)
		if err != nil {
			return deny(fmt.Sprintf("cannot verify that %s is a fast-forward (fetch first): %v", branch, err))
		}
		if !ff {
			return deny(fmt.Sprintf("force push to %q is not allowed (not a fast-forward)", branch))
		}
	}
	return allow()
}
