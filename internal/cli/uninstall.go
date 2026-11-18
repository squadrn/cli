package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"sprawling-agents/internal/config"
	"sprawling-agents/internal/fsx"
	"sprawling-agents/internal/install"
)

func (a *app) selfUninstallCmd() *cobra.Command {
	var yes, purge bool
	cmd := &cobra.Command{
		Use:   "self-uninstall",
		Short: "Remove sprawl: plugin from every account, command links, completions, installed files",
		Long: `Remove sprawl from this machine. Accounts, their logins and conversations are kept.
--purge also removes the config, the guard log and caches. Running agent sessions keep going;
restart them afterwards.`,
		Args: noArgs,
		RunE: func(*cobra.Command, []string) error {
			what := "Remove sprawl (plugin in every account, " + a.tilde(a.layout.DataDir) + ")"
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
	seen := map[string]bool{}
	for _, acct := range accts {
		if seen[acct.Provider] {
			continue
		}
		seen[acct.Provider] = true
		ag, err := a.agent(acct.Spec)
		if err != nil {
			a.warnf("%v; remove the plugin yourself: %s plugin uninstall sprawl@sprawl", err, acct.Spec.Binary)
			continue
		}
		same := accts[:0:0]
		for _, x := range accts {
			if x.Provider == acct.Provider {
				same = append(same, x)
			}
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
		names = append(names, strings.TrimSuffix(filepath.Base(l), ".exe"))
		a.removeCompletions(strings.TrimSuffix(filepath.Base(l), ".exe"))
	}
	a.removeProfileLines()
	// Executables still running (this one, a session, a hook) cannot be deleted on Windows; they
	// are removed once sprawl has exited.
	var later []string
	if err := os.RemoveAll(a.layout.DataDir); err != nil {
		later = append(later, a.layout.DataDir)
	}
	later = append(later, a.layout.Leftovers(names)...)
	if len(later) > 0 {
		if err := removeAfterExit(later); err != nil {
			a.warnf("could not remove %s: %v", strings.Join(later, ", "), err)
		}
	}
	a.out("  removed %s\n", a.tilde(a.layout.DataDir))
	if purge {
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
	a.out("sprawl is uninstalled; restart running agent sessions\n")
	return nil
}

// removeCompletions deletes the completion scripts sprawl generated for name.
func (a *app) removeCompletions(name string) {
	for shell, dir := range a.completionDirs() {
		file := completionFile(shell, dir, name)
		if b, err := os.ReadFile(file); err == nil && generatedCompletion(b, name) {
			_ = os.Remove(file)
		}
	}
}
