package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/squadrn/squadrn/internal/account"
	"github.com/squadrn/squadrn/internal/fsx"
	"github.com/squadrn/squadrn/internal/provider"
)

// settingsPath is the account's settings file, which holds its status line.
func settingsPath(acct account.Account) string {
	return account.SettingsPath(acct)
}

// statusLineOf is the status line of spec's CLI; nil when it reports no usage that way.
func statusLineOf(spec provider.Spec) provider.StatusLine {
	if !spec.Caps.UsageReport {
		return nil
	}
	d, _ := provider.DriverOf(spec.ID)
	return d.StatusLine
}

// usageTracking: the account's status line already runs squadrn's recorder.
func (a *app) usageTracking(acct account.Account) bool {
	sl := statusLineOf(acct.Spec)
	if sl == nil {
		return false
	}
	cmd, ok, err := sl.Command(settingsPath(acct))
	if err != nil || !ok {
		return false
	}
	_, ours := sl.Unwrap(cmd, statusLineHook)
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
func (a *app) wrapStatusLine(sl provider.StatusLine, old string) (string, error) {
	return sl.Wrap(a.statusLineBinary(), statusLineHook, old, a.store.Home)
}

// settingsGroup is one settings file, the status line format it holds, and the accounts that
// use it: one account, unless its settings could not be made its own copy (still a link).
type settingsGroup struct {
	path  string
	sl    provider.StatusLine
	accts []account.Account
}

// claim records the status line as squadrn's key in the group's own copies (on), or hands it
// back to main's value (off), so the settings sync neither reverts nor reports it.
func (g settingsGroup) claim(on bool) error {
	for _, acct := range g.accts {
		if fsx.Real(settingsPath(acct)) != fsx.Real(g.path) {
			continue
		}
		f := settingsFormat(acct.Spec)
		if on {
			if err := account.ClaimSetting(acct, f, g.sl.Key()); err != nil {
				return err
			}
		} else if err := account.ReleaseSetting(acct, f, g.sl.Key()); err != nil {
			return err
		}
	}
	return nil
}

// tracked reads g's status line: whether it has one and whether it runs squadrn's recorder.
func (g settingsGroup) tracked() (cmd, old string, has, ours bool, err error) {
	cmd, has, err = g.sl.Command(g.path)
	if err != nil {
		return "", "", false, false, err
	}
	old, ours = g.sl.Unwrap(cmd, statusLineHook)
	return cmd, old, has, ours, nil
}

func (a *app) settingsGroups(targets []account.Account) ([]settingsGroup, error) {
	all, err := a.store.List()
	if err != nil {
		return nil, err
	}
	var groups []settingsGroup
	seen := map[string]bool{}
	for _, t := range targets {
		sl := statusLineOf(t.Spec)
		if sl == nil {
			continue
		}
		// An account still linked to main's settings gets its own copy first.
		if _, err := a.syncSettings(t); err != nil {
			return nil, err
		}
		key := fsx.Real(settingsPath(t))
		if seen[key] {
			continue
		}
		seen[key] = true
		g := settingsGroup{path: settingsPath(t), sl: sl}
		for _, x := range all {
			if x.Spec.Caps.UsageReport && (fsx.Real(settingsPath(x)) == key || fsx.SameFile(settingsPath(x), key)) {
				g.accts = append(g.accts, x)
			}
		}
		groups = append(groups, g)
	}
	return groups, nil
}

// groupWhere names a settings file and the accounts that use it.
func (a *app) groupWhere(g settingsGroup) string {
	labels := make([]string, len(g.accts))
	for i, acct := range g.accts {
		labels[i] = acct.Label()
	}
	return fmt.Sprintf("%s (%s)", a.tilde(fsx.Real(g.path)), strings.Join(labels, ", "))
}

// setupGroup turns usage tracking on (or off, undo) in g; quiet skips the result line.
func (a *app) setupGroup(g settingsGroup, undo, yes, quiet bool) error {
	where := a.groupWhere(g)
	say := a.out
	if quiet {
		say = func(string, ...any) {}
	}
	cmd, old, has, ours, err := g.tracked()
	if err != nil {
		return err
	}
	if undo {
		if !has || !ours {
			say("usage tracking is not set up in %s\n", where)
			return nil
		}
		if err := a.confirm(yes, "Restore the previous status line in %s", where); err != nil {
			return err
		}
		if err := g.sl.SetCommand(g.path, old); err != nil {
			return err
		}
		if err := g.claim(false); err != nil {
			return err
		}
		say("usage tracking off: %s\n", where)
		return nil
	}
	if has && ours {
		say("usage tracking already on: %s\n", where)
		return nil
	}
	wrapped, err := a.wrapStatusLine(g.sl, cmd)
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
	if err := g.sl.SetCommand(g.path, wrapped); err != nil {
		return err
	}
	if err := g.claim(true); err != nil {
		return err
	}
	say("usage tracking on: %s\n", where)
	return nil
}

// enableUsage turns usage tracking on for acct without asking (account add).
func (a *app) enableUsage(acct account.Account) error {
	if statusLineOf(acct.Spec) == nil || a.usageTracking(acct) {
		return nil
	}
	groups, err := a.settingsGroups([]account.Account{acct})
	if err != nil {
		return err
	}
	for _, g := range groups {
		if err := a.setupGroup(g, false, true, true); err != nil {
			return err
		}
	}
	return nil
}

// disableUsage turns usage tracking off for acct without asking (account add --no-usage). An
// account other than main keeps the status line it copied from main without the recorder, as a
// key of its own, so the settings sync does not bring main's recorder back.
func (a *app) disableUsage(acct account.Account) error {
	if !a.usageTracking(acct) {
		return nil
	}
	groups, err := a.settingsGroups([]account.Account{acct})
	if err != nil {
		return err
	}
	for _, g := range groups {
		if acct.IsMain() {
			if err := a.setupGroup(g, true, true, true); err != nil {
				return err
			}
			continue
		}
		_, old, has, ours, err := g.tracked()
		if err != nil || !has || !ours {
			return err
		}
		if err := g.sl.SetCommand(g.path, old); err != nil {
			return err
		}
		if err := g.claim(true); err != nil {
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
		_, old, has, ours, err := g.tracked()
		if err != nil || !has || !ours {
			continue
		}
		if err := g.sl.SetCommand(g.path, old); err != nil {
			a.warnf("cannot restore the status line in %s: %v", a.tilde(g.path), err)
			continue
		}
		a.out("  restored the status line in %s\n", a.tilde(fsx.Real(g.path)))
	}
}
