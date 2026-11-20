package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/squadrn/squadrn/internal/config"
	"github.com/squadrn/squadrn/internal/doctor"
	"github.com/squadrn/squadrn/internal/fsx"
	"github.com/squadrn/squadrn/internal/gitver"
	"github.com/squadrn/squadrn/internal/install"
	"github.com/squadrn/squadrn/internal/release"
)

// nameChoice is what the user asked for at install; empty fields keep the configured names.
type nameChoice struct {
	command  string
	aliases  []string
	aliasSet bool
	force    bool
	// selfInstall: the installers run this release (not self-update or rollback).
	selfInstall bool
	// interactive: ask for the names on the terminal (installers without name flags).
	interactive bool
}

// openTTY opens a terminal for prompts when stdin or stdout are redirected; tests replace it.
var openTTY = openTerminal

// terminalIO is where install prompts go: stdin/stdout when both are a terminal, else the
// controlling terminal. ok is false when there is none (CI, ssh -T): nothing is asked then.
func (a *app) terminalIO() (io.Reader, io.Writer, func(), bool) {
	if a.interactive {
		return a.stdin, a.stdout, func() {}, true
	}
	return openTTY()
}

func (a *app) selfInstallCmd() *cobra.Command {
	var nc nameChoice
	cmd := &cobra.Command{
		Use:    "self-install <unpacked-release-dir>",
		Short:  "Install an unpacked release (used by install.sh)",
		Hidden: true,
		Args:   oneArg,
		RunE: func(cmd *cobra.Command, args []string) error {
			nc.aliasSet = cmd.Flags().Changed("alias")
			nc.selfInstall = true
			return a.installRelease(args[0], Version, nc)
		},
	}
	cmd.Flags().StringVar(&nc.command, "command", "", "command name to install under (default: configured, else srn)")
	cmd.Flags().StringSliceVar(&nc.aliases, "alias", nil, "extra command names, comma separated")
	cmd.Flags().BoolVarP(&nc.force, "force", "f", false, "install even if another program on PATH has one of the names")
	cmd.Flags().BoolVar(&nc.interactive, "interactive", false, "ask for the command name and aliases on the terminal")
	return cmd
}

// installRelease places the release, links the command names and installs or refreshes the
// plugin in every account.
func (a *app) installRelease(src, version string, nc nameChoice) error {
	cfgPath, err := config.Path()
	if err != nil {
		return err
	}
	cfg, fresh, err := config.ReadOrEmpty(cfgPath)
	if err != nil {
		return err
	}
	if err := release.CheckInstallDir(a.layout.DataDir); err != nil {
		return err
	}
	accepted := install.Names(cfg)
	oldCmd, oldAliases := cfg.Command, cfg.Aliases
	if cfg.Command == "" {
		cfg.Command = config.DefaultCommand
	}
	if fresh {
		cfg.Aliases = config.DefaultAliases()
	}
	// A chosen command that is a default alias swaps with the default command (--command squadrn
	// on a fresh machine installs squadrn with the alias srn).
	if nc.command != "" {
		cfg.SetCommand(nc.command)
	}
	if nc.aliasSet {
		cfg.Aliases = nc.aliases
	}
	if nc.interactive && nc.command == "" && !nc.aliasSet {
		if in, out, done, ok := a.terminalIO(); ok {
			p := namePrompt{in: bufio.NewReader(in), out: out, check: a.nameCheck(accepted, nc.force)}
			var forced map[string]bool
			cfg.Command, cfg.Aliases, forced = p.run(cfg.Command, cfg.Aliases)
			done()
			for n := range forced {
				accepted[n] = true
			}
		}
	}
	if err := a.nameCheck(accepted, nc.force).Check(cfg); err != nil {
		return err
	}
	unlock, err := fsx.Lock(a.installLockPath(), 0)
	var held fsx.LockedError
	if errors.As(err, &held) {
		return fmt.Errorf("%w; try again when it has finished", held)
	} else if err != nil {
		return err
	}
	defer unlock()
	// Keep the installed release for --rollback. The snapshot is staged first because src may be
	// the previous snapshot itself (a rollback).
	staged := a.layout.Previous() + ".new"
	if err := a.layout.Snapshot(staged); err != nil {
		return fmt.Errorf("keep the installed release for rollback: %w", err)
	}
	if err := a.layout.Install(src, version); err != nil {
		_ = os.RemoveAll(staged)
		return err
	}
	if fsx.IsDir(staged) {
		if err := fsx.MoveDir(staged, a.layout.Previous()); err != nil {
			a.warnf("cannot keep the previous release for --rollback (%v); a rollback may reinstall an older one", err)
			_ = os.RemoveAll(staged)
		}
	}
	beta := nc.selfInstall && cfg.UpdateChannel == "" && release.Prerelease(version)
	if beta {
		cfg.UpdateChannel = config.Beta
	}
	if err := config.Save(cfgPath, cfg); err != nil {
		return err
	}
	a.cmd, a.cfg, a.cfgErr = cfg.Command, cfg, nil
	if err := registerToastApp(); err != nil {
		a.warnf("cannot register squadrn for desktop notifications: %v", err)
	}
	a.out("installed squadrn %s → %s\n", version, a.tilde(a.layout.Binary()))
	if beta {
		a.out("  pre-release: following the beta channel (back to stable: %s config set update_channel stable)\n", cfg.Command)
	}
	if err := a.linkNames(cfg); err != nil {
		return err
	}
	if !fresh && oldCmd != "" {
		if msg := renameSummary(oldCmd, oldAliases, cfg.Command, cfg.Aliases); msg != "" {
			a.out("%s\n", msg)
		}
	}
	a.installPlugins()
	// An update must never be blocked by Git (that would strand users on old releases); say it.
	if err := gitver.Require(context.Background()); err != nil {
		a.warnf("%v", err)
	}
	if !onPATH(a.layout.BinDir) {
		a.out("%s: %s\n", pathWhere, doctor.PathFix(a.layout.BinDir))
	}
	if fresh && nc.selfInstall {
		return a.firstRun(nc.interactive)
	}
	a.out("next: %s doctor\n", cfg.Command)
	return nil
}

// installLockPath keeps two installs, updates or rollbacks from running at once.
func (a *app) installLockPath() string { return filepath.Join(a.layout.DataDir, "install.lock") }

func (a *app) nameCheck(accepted map[string]bool, force bool) install.NameCheck {
	self, _ := os.Executable()
	return install.NameCheck{Layout: a.layout, Accepted: accepted, Force: force, Self: self}
}

func (a *app) linkNames(cfg config.Config) error {
	names := append([]string{cfg.Command}, cfg.Aliases...)
	res, err := a.layout.LinkCommands(names, a.now())
	if err != nil {
		return err
	}
	for _, l := range res.Created {
		a.out("  linked %s\n", a.tilde(l))
	}
	for _, l := range res.Removed {
		a.out("  removed %s\n", a.tilde(l))
	}
	for _, l := range res.BackedUp {
		a.out("  moved an unrelated file aside: %s\n", a.tilde(l))
	}
	a.writeCompletions(names, res.Removed)
	return nil
}

func (a *app) installPlugins() {
	accts, err := a.store.List()
	if err != nil {
		a.warnf("%v", err)
		return
	}
	for _, idx := range byProvider(accts) {
		same := pick(accts, idx)
		ag, err := a.agent(same[0].Spec)
		if err != nil {
			a.warnf("%v; install the plugin later by re-running install.sh", err)
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		for _, r := range install.Plugins(ctx, same, ag, a.layout.DataDir) {
			if r.Action == install.PluginFailed {
				a.errf("  %s: plugin update failed: %v\n", r.Account.Label(), r.Err)
				continue
			}
			a.out("  %s: plugin %s\n", r.Account.Label(), r.Action)
		}
		cancel()
	}
}

// firstRun follows the first install: srn setup at a terminal (the installers pass --interactive
// then, and pipe stdin, so the controlling terminal is used), else a summary of the agent CLIs,
// the accounts and next steps. Never after an update.
func (a *app) firstRun(interactive bool) error {
	in, out, done, ok := a.terminalIO()
	if !ok || !interactive {
		if ok {
			done()
		}
		a.summary()
		return nil
	}
	defer done()
	stdin, stdout, stderr, interactive := a.stdin, a.stdout, a.stderr, a.interactive
	a.stdin, a.stdout, a.stderr, a.interactive = in, out, out, true
	defer func() { a.stdin, a.stdout, a.stderr, a.interactive = stdin, stdout, stderr, interactive }()
	a.out("\nset up %s (every step can be skipped; run it again any time: %s setup)\n", config.Slug, a.cmd)
	return a.setup(false)
}
