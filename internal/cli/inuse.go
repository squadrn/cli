package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/squadrn/squadrn/internal/account"
	"github.com/squadrn/squadrn/internal/fsx"
	"github.com/squadrn/squadrn/internal/inuse"
	"github.com/squadrn/squadrn/internal/usage"
	"github.com/squadrn/squadrn/internal/worktree"
)

// markInUse records the session about to start in the account's config dir and in the worktree
// it runs in, so gc, wt rm and account rm/rename refuse to delete them meanwhile. On Unix the
// process becomes the agent CLI and the records outlive it as stale entries; the returned
// release runs only when the session returns (Windows) or fails to start.
func (a *app) markInUse(acct account.Account, wt string) (release func()) {
	var dirs []string
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cwd, err := os.Getwd()
	top := ""
	if err == nil {
		top = worktree.Toplevel(ctx, cwd)
	}
	session := cwd
	if top != "" && wt != "" {
		session = filepath.Join(top, acct.Spec.WorktreeDir, wt)
	}
	if inuse.Acquire(acct.Dir, inuse.Entry{Account: acct.Label(), Cwd: session}) == nil {
		dirs = append(dirs, acct.Dir)
	}
	if top != "" && worktree.Exclude(ctx, top, inuse.Dir+"/") == nil {
		dir, e := top, inuse.Entry{Account: acct.Label()}
		if wt != "" {
			if fsx.IsDir(session) {
				dir = session
			} else {
				e.Worktree = wt // created by the agent CLI once it runs
			}
		}
		if inuse.Acquire(dir, e) == nil {
			dirs = append(dirs, dir)
		}
	}
	return func() {
		for _, d := range dirs {
			_ = inuse.Release(d)
		}
	}
}

// sessionDesc names a running session for messages: "pid 123 (claude/work)".
func (a *app) sessionDesc(e inuse.Entry) string {
	if e.Account == "" {
		return fmt.Sprintf("pid %d", e.PID)
	}
	return fmt.Sprintf("pid %d (%s)", e.PID, e.Account)
}

// checkAccountFree refuses to move an account dir while a grid pane runs as the account or,
// unless force, another session squadrn started still uses it.
func (a *app) checkAccountFree(acct account.Account, force bool) error {
	if p := a.anyPane(acct.Label()); p != "" {
		return fmt.Errorf("%s is running in tmux pane %s; stop it first", acct.Label(), p)
	}
	if used := inuse.Active(acct.Dir); len(used) > 0 && !force {
		return fmt.Errorf("%s is in use by %s; stop it first (or use --force)", acct.Label(), a.sessionDesc(used[0]))
	}
	return nil
}

// recordedDirs are the directories squadrn knows sessions of accts ran in without searching the
// disk: the working directories in their usage records and in the handoffs match accepts, and
// their parents up to (not including) the home dir.
func (a *app) recordedDirs(accts []account.Account, match func(usage.Handoff) bool) []string {
	var recorded []string
	for _, acct := range accts {
		if snap, ok, _ := a.usageStore().Load(acct.Provider, acct.Name); ok {
			recorded = append(recorded, snap.Cwd, snap.LimitedCwd)
		}
	}
	_, _, _ = a.ledger().Latest(func(h usage.Handoff) bool {
		if match(h) {
			recorded = append(recorded, h.Cwd)
		}
		return false
	})
	seen := map[string]bool{}
	var dirs []string
	for _, d := range recorded {
		for d != "" && filepath.IsAbs(d) && d != a.store.Home && d != filepath.Dir(d) {
			if !seen[d] {
				seen[d] = true
				dirs = append(dirs, d)
			}
			d = filepath.Dir(d)
		}
	}
	return dirs
}
