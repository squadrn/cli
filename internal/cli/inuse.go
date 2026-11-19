package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/squadrn/cli/internal/account"
	"github.com/squadrn/cli/internal/fsx"
	"github.com/squadrn/cli/internal/inuse"
	"github.com/squadrn/cli/internal/worktree"
)

// markInUse records the session about to start in the account's config dir and in the worktree
// it runs in, so gc, wt rm and account rm/rename refuse to delete them meanwhile. On Unix the
// process becomes the agent CLI and the records outlive it as stale entries; the returned
// release runs only when the session returns (Windows) or fails to start.
func (a *app) markInUse(acct account.Account, wt string) (release func()) {
	var dirs []string
	if inuse.Acquire(acct.Dir, inuse.Entry{Account: acct.Label()}) == nil {
		dirs = append(dirs, acct.Dir)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cwd, err := os.Getwd()
	top := ""
	if err == nil {
		top = worktree.Toplevel(ctx, cwd)
	}
	if top != "" && worktree.Exclude(ctx, top, inuse.Dir+"/") == nil {
		dir, e := top, inuse.Entry{Account: acct.Label()}
		if wt != "" {
			if p := filepath.Join(top, acct.Spec.WorktreeDir, wt); fsx.IsDir(p) {
				dir = p
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
