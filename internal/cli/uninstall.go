package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/squadrn/squadrn/internal/account"
	"github.com/squadrn/squadrn/internal/config"
	"github.com/squadrn/squadrn/internal/fsx"
	"github.com/squadrn/squadrn/internal/install"
	"github.com/squadrn/squadrn/internal/inuse"
	"github.com/squadrn/squadrn/internal/release"
	"github.com/squadrn/squadrn/internal/usage"
)

func (a *app) selfUninstallCmd() *cobra.Command {
	var yes, purge bool
	cmd := &cobra.Command{
		Use:   "self-uninstall",
		Short: "Remove squadrn: plugin from every account, command links, completions, installed files",
		Long: `Remove squadrn from this machine. Accounts, their logins and conversations are kept.
--purge also removes the config, the guard log and caches, and the session registries
(.squadrn-inuse) in account dirs and in the directories squadrn recorded. Running agent sessions
keep going; restart them afterwards.`,
		Args: noArgs,
		RunE: func(*cobra.Command, []string) error {
			what := "Remove squadrn (plugin in every account, " + a.tilde(a.layout.DataDir) + ")"
			if purge {
				what += " and its config, logs and caches"
			}
			if err := a.confirm(yes, "%s", what); err != nil {
				return err
			}
			return a.uninstall(purge)
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "do not ask for confirmation")
	cmd.Flags().BoolVar(&purge, "purge", false, "also remove the config, guard log and caches")
	return cmd
}

func (a *app) uninstall(purge bool) error {
	accts, err := a.store.List()
	if err != nil {
		return err
	}
	for _, idx := range byProvider(accts) {
		same := pick(accts, idx)
		ag, err := a.agent(same[0].Spec)
		if err != nil {
			a.warnf("%v; remove the plugin yourself: %s plugin uninstall squadrn@squadrn", err, same[0].Spec.Binary)
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		for label, err := range install.RemovePlugins(ctx, same, ag) {
			if err != nil {
				a.warnf("%s: plugin uninstall: %v", label, err)
				continue
			}
			a.out("  %s: plugin removed\n", label)
		}
		cancel()
	}
	names := append([]string{a.cmd}, a.cfg.Aliases...)
	a.restoreStatusLines()
	links, err := a.layout.Unlink()
	if err != nil {
		return err
	}
	for _, l := range links {
		a.out("  removed %s\n", a.tilde(l))
		name := release.CommandName(filepath.Base(l))
		names = append(names, name)
		a.removeCompletions(name)
	}
	a.removeProfileLines()
	// Executables still running (this one, a session, a hook) cannot be deleted on Windows; they
	// are removed once squadrn has exited.
	var later []string
	if err := os.RemoveAll(a.layout.DataDir); err != nil {
		later = append(later, a.layout.DataDir)
	}
	later = append(later, a.layout.Leftovers(names)...)
	if len(later) > 0 {
		switch deferred, err := removeAfterExit(later); {
		case err != nil:
			a.warnf("cannot remove %s: %v", strings.Join(later, ", "), err)
		case deferred:
			for _, p := range later {
				a.out("  %s will be removed when squadrn exits\n", a.tilde(p))
			}
		}
	}
	if !fsx.IsDir(a.layout.DataDir) {
		a.out("  removed %s\n", a.tilde(a.layout.DataDir))
	}
	if purge {
		a.purgeInUse(accts)
		dirs := config.DirsFor(a.store.Home, config.Slug)
		for _, d := range []string{dirs.Config, dirs.State, dirs.Cache} {
			if fsx.IsDir(d) {
				if err := os.RemoveAll(d); err != nil {
					return err
				}
				a.out("  removed %s\n", a.tilde(d))
			}
		}
	}
	a.out("squadrn is uninstalled; restart running agent sessions\n")
	return nil
}

// removeCompletions deletes the completion scripts squadrn generated for name.
func (a *app) removeCompletions(name string) {
	for shell, dir := range a.completionDirs() {
		file := completionFile(shell, dir, name)
		if b, err := os.ReadFile(file); err == nil && generatedCompletion(b, name) {
			_ = os.Remove(file)
		}
	}
}

// purgeInUse removes the session registries (.squadrn-inuse) squadrn can find without searching
// the disk: in every account dir, and in the directories its usage records and handoffs name and
// their parents. A registry with a live session is left alone.
func (a *app) purgeInUse(accts []account.Account) {
	seen := map[string]bool{}
	var dirs []string
	add := func(d string) {
		if d != "" && !seen[d] {
			seen[d] = true
			dirs = append(dirs, d)
		}
	}
	for _, acct := range accts {
		add(acct.Dir)
	}
	var recorded []string
	for _, acct := range accts {
		if snap, ok, _ := a.usageStore().Load(acct.Provider, acct.Name); ok {
			recorded = append(recorded, snap.Cwd, snap.LimitedCwd)
		}
	}
	_, _, _ = a.ledger().Latest(func(h usage.Handoff) bool {
		recorded = append(recorded, h.Cwd)
		return false
	})
	for _, d := range recorded {
		for d != "" && filepath.IsAbs(d) && d != a.store.Home && d != filepath.Dir(d) {
			add(d)
			d = filepath.Dir(d)
		}
	}
	for _, d := range dirs {
		reg := filepath.Join(d, inuse.Dir)
		if !fsx.IsDir(reg) {
			continue
		}
		if live := inuse.Active(d); len(live) > 0 {
			a.warnf("kept %s: %s is still running", a.tilde(reg), a.sessionDesc(live[0]))
			continue
		}
		if err := os.RemoveAll(reg); err != nil {
			a.warnf("cannot remove %s: %v", a.tilde(reg), err)
			continue
		}
		a.out("  removed %s\n", a.tilde(reg))
	}
}
