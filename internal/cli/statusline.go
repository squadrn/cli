package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/squadrn/squadrn/internal/account"
	"github.com/squadrn/squadrn/internal/fsx"
	"github.com/squadrn/squadrn/internal/provider/claude"
)

// settingsPath is the account's settings file, which holds its status line.
func settingsPath(acct account.Account) string {
	return filepath.Join(acct.Dir, acct.Spec.Settings)
}

// usageTracking: the account's status line already runs squadrn's recorder.
func (a *app) usageTracking(acct account.Account) bool {
	if !acct.Spec.Caps.UsageReport {
		return false
	}
	cmd, ok, err := claude.StatusLineCommand(settingsPath(acct))
	if err != nil || !ok {
		return false
	}
	_, ours := unwrapStatusLine(cmd)
	return ours
}

// statusLineBinary is what the status line runs: the installed binary (a fixed path that
// survives command renames and updates), else this one.
func (a *app) statusLineBinary() string {
	if fsx.IsFile(a.layout.Binary()) {
		return a.layout.Binary()
	}
	if exe := selfPath(); fsx.IsFile(exe) {
		return exe
	}
	exe, _ := os.Executable()
	return exe
}

// statusLineHook is what the status line runs squadrn with.
const statusLineHook = "hook statusline"

// wrapStatusLine is the status line command that records usage and then runs old ("" for none).
func (a *app) wrapStatusLine(old string) (string, error) {
	return claude.WrapStatusLine(a.statusLineBinary(), statusLineHook, old, a.store.Home)
}

func unwrapStatusLine(cmd string) (old string, ours bool) {
	return claude.UnwrapStatusLine(cmd, statusLineHook)
}

// settingsGroup is one settings file and the accounts that use it (through links to main's).
type settingsGroup struct {
	path     string
	accounts []string
}

func (a *app) settingsGroups(targets []account.Account) ([]settingsGroup, error) {
	all, err := a.store.List()
	if err != nil {
		return nil, err
	}
	var groups []settingsGroup
	seen := map[string]bool{}
	for _, t := range targets {
		if !t.Spec.Caps.UsageReport {
			continue
		}
		key := fsx.Real(settingsPath(t))
		if seen[key] {
			continue
		}
		seen[key] = true
		g := settingsGroup{path: settingsPath(t)}
		for _, x := range all {
			if x.Spec.Caps.UsageReport && (fsx.Real(settingsPath(x)) == key || fsx.SameFile(settingsPath(x), key)) {
				g.accounts = append(g.accounts, x.Label())
			}
		}
		groups = append(groups, g)
	}
	return groups, nil
}

func (a *app) setupGroup(g settingsGroup, undo, yes bool) error {
	where := fmt.Sprintf("%s (%s)", a.tilde(fsx.Real(g.path)), strings.Join(g.accounts, ", "))
	cmd, has, err := claude.StatusLineCommand(g.path)
	if err != nil {
		return err
	}
	old, ours := unwrapStatusLine(cmd)
	if undo {
		if !has || !ours {
			a.out("usage tracking is not set up in %s\n", where)
			return nil
		}
		if err := a.confirm(yes, "Restore the previous status line in %s", where); err != nil {
			return err
		}
		if err := claude.SetStatusLineCommand(g.path, old); err != nil {
			return err
		}
		a.out("usage tracking off: %s\n", where)
		return nil
	}
	if has && ours {
		a.out("usage tracking already on: %s\n", where)
		return nil
	}
	wrapped, err := a.wrapStatusLine(cmd)
	if err != nil {
		return err
	}
	what := "Record usage limits through the status line of " + where
	if has {
		what += "; it keeps running " + cmd
	}
	if err := a.confirm(yes, "%s", what); err != nil {
		return err
	}
	if err := claude.SetStatusLineCommand(g.path, wrapped); err != nil {
		return err
	}
	a.out("usage tracking on: %s\n", where)
	return nil
}

// offerUsageTracking asks once, at account add, whether to record the account's usage. choice
// is the --usage/--no-usage flag; nil asks at a terminal and does nothing otherwise.
func (a *app) offerUsageTracking(acct account.Account, choice *bool) error {
	if !acct.Spec.Caps.UsageReport || a.usageTracking(acct) {
		return nil
	}
	if choice == nil {
		if !a.interactive {
			return nil
		}
		a.errf("Track usage limits for this account in its status line? [Y/n] ")
		line, err := bufio.NewReader(a.stdin).ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return err
		}
		yes := !strings.HasPrefix(strings.ToLower(strings.TrimSpace(line)), "n")
		choice = &yes
	}
	if !*choice {
		return nil
	}
	groups, err := a.settingsGroups([]account.Account{acct})
	if err != nil {
		return err
	}
	for _, g := range groups {
		if err := a.setupGroup(g, false, true); err != nil {
			return err
		}
	}
	return nil
}

// restoreStatusLines undoes usage tracking in every account (self-uninstall).
func (a *app) restoreStatusLines() {
	accts, err := a.store.List()
	if err != nil {
		return
	}
	groups, err := a.settingsGroups(accts)
	if err != nil {
		return
	}
	for _, g := range groups {
		cmd, has, err := claude.StatusLineCommand(g.path)
		old, ours := unwrapStatusLine(cmd)
		if err != nil || !has || !ours {
			continue
		}
		if err := claude.SetStatusLineCommand(g.path, old); err != nil {
			a.warnf("cannot restore the status line in %s: %v", a.tilde(g.path), err)
			continue
		}
		a.out("  restored the status line in %s\n", a.tilde(fsx.Real(g.path)))
	}
}
