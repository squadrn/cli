package cli

import (
	"context"
	"os"
	"os/exec"
	"time"

	"github.com/spf13/cobra"

	"github.com/squadrn/squadrn/internal/doctor"
	"github.com/squadrn/squadrn/internal/gitver"
	"github.com/squadrn/squadrn/internal/proc"
	"github.com/squadrn/squadrn/internal/update"
	"github.com/squadrn/squadrn/internal/worktree"
)

func (a *app) doctorCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:               "doctor [<account>]",
		Short:             "Check the install, the push guard, hooks and every account (or one)",
		Args:              optionalArg,
		ValidArgsFunction: a.completeAccounts,
		RunE: func(_ *cobra.Command, args []string) error {
			only := ""
			if len(args) == 1 {
				acct, err := a.store.Get(args[0])
				if err != nil {
					return err
				}
				only = acct.Label()
			}
			if asJSON {
				return a.doctorJSON(only)
			}
			return a.runDoctor(only)
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print JSON")
	return cmd
}

func (a *app) printChecks(checks []doctor.Check) error {
	marks := map[doctor.Status]string{
		doctor.OK:   a.paint(a.colorOut, "32", "✓"),
		doctor.Warn: a.paint(a.colorOut, "33", "!"),
		doctor.Fail: a.paint(a.colorOut, "31", "✗"),
	}
	group := ""
	counts := map[doctor.Status]int{}
	for _, c := range checks {
		if c.Group != group {
			group = c.Group
			a.out("%s\n", group)
		}
		counts[c.Status]++
		line := "  " + marks[c.Status] + " " + c.Name
		if c.Fix != "" {
			line += "  → " + c.Fix
		}
		a.out("%s\n", line)
	}
	switch {
	case counts[doctor.Fail] > 0:
		a.out("%d problem(s), %d warning(s)\n", counts[doctor.Fail], counts[doctor.Warn])
		return errQuiet
	case counts[doctor.Warn] > 0:
		a.out("ok with %d warning(s)\n", counts[doctor.Warn])
	default:
		a.out("all checks passed\n")
	}
	return nil
}

// doctorEnv is what doctor checks with; only limits the account checks to one account.
func (a *app) doctorEnv(only string) doctor.Env {
	ctx := context.Background()
	env := doctor.Env{
		Store:     a.store,
		Agent:     a.agent,
		Layout:    a.layout,
		Config:    a.cfg,
		LookPath:  exec.LookPath,
		Self:      selfPath(),
		Names:     append([]string{a.cmd}, a.cfg.Aliases...),
		Command:   a.cmd,
		Timeout:   10 * time.Second,
		Version:   Version,
		Signed:    a.signers != "",
		Git:       gitver.Detect,
		GitHint:   gitver.Hint(),
		ConfigErr: a.cfgErr,
		Usage:     a.usageTracking,
		Only:      only,
		Notifier: func() (string, string, bool) {
			n, ok := findNotifier()
			return n.Tool, n.Note, ok
		},
	}
	if latest, ok := update.Available(Version, update.Load(a.updateStatePath()), a.cfg.Channel()); ok {
		env.Latest = latest
	}
	if cwd, err := os.Getwd(); err == nil {
		if repo, err := worktree.Open(ctx, cwd, ""); err == nil {
			env.Repo = repo.Root
		}
	}
	if bin, err := exec.LookPath("gh"); err == nil {
		env.GH = func(ctx context.Context, args ...string) ([]byte, error) {
			return proc.Output(ctx, proc.Options{Dir: env.Repo}, bin, args...)
		}
	}
	return env
}

func (a *app) checks(only string) []doctor.Check {
	stop := a.spin("checking…")
	defer stop()
	return doctor.Run(context.Background(), a.doctorEnv(only))
}

// runDoctor prints the checks; errQuiet when one fails.
func (a *app) runDoctor(only string) error { return a.printChecks(a.checks(only)) }

func (a *app) doctorJSON(only string) error {
	checks := a.checks(only)
	if err := a.printJSON(checks); err != nil {
		return err
	}
	for _, c := range checks {
		if c.Status == doctor.Fail {
			return errQuiet
		}
	}
	return nil
}
