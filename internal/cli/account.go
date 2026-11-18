package cli

import (
	"context"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"sprawling-agents/internal/account"
)

func (a *app) accountCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "account",
		Short: "Manage agent accounts (default: ls)",
		Args:  subcommandArgs,
		RunE:  func(*cobra.Command, []string) error { return a.accountList(asJSON) },
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print JSON")
	ls := &cobra.Command{
		Use:   "ls",
		Short: "List accounts with login status",
		Args:  noArgs,
		RunE:  func(*cobra.Command, []string) error { return a.accountList(asJSON) },
	}
	ls.Flags().BoolVar(&asJSON, "json", false, "print JSON")
	cmd.AddCommand(
		ls,
		&cobra.Command{
			Use:               "add [<provider>/]<name>",
			Short:             "Create or repair an account (shared links, allowlisted MCP, plugin)",
			Example:           fmt.Sprintf("  %[1]s account add work\n  %[1]s account add claude/work", a.cmd),
			Args:              oneArg,
			RunE:              func(_ *cobra.Command, args []string) error { return a.accountAdd(args[0]) },
			ValidArgsFunction: cobra.NoFileCompletions,
		},
		&cobra.Command{
			Use:               "login <account>",
			Short:             "Sign an account in",
			Args:              oneArg,
			RunE:              func(_ *cobra.Command, args []string) error { return a.accountLogin(args[0]) },
			ValidArgsFunction: a.completeAccounts,
		},
		&cobra.Command{
			Use:               "logout <account>",
			Short:             "Sign an account out (keeps its dir)",
			Args:              oneArg,
			RunE:              func(_ *cobra.Command, args []string) error { return a.accountLogout(args[0]) },
			ValidArgsFunction: a.completeAccounts,
		},
		a.accountRenameCmd(),
		a.accountRmCmd(),
		a.accountAdoptCmd(),
	)
	return cmd
}

func (a *app) accountRenameCmd() *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:               "rename <account> <new-name>",
		Short:             "Rename an account (logs it out first; log in again afterwards)",
		Args:              exactArgs(2),
		RunE:              func(_ *cobra.Command, args []string) error { return a.accountRename(args[0], args[1], force) },
		ValidArgsFunction: a.completeAccounts,
	}
	cmd.Flags().BoolVarP(&force, "force", "f", false, "rename even if a session seems to use the account")
	return cmd
}

func (a *app) accountAdoptCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:               "adopt [<provider>/]<name>",
		Short:             "Make an existing config dir (e.g. ~/.claude-<name>) an account",
		Args:              oneArg,
		ValidArgsFunction: a.completeUnmanaged,
		RunE: func(_ *cobra.Command, args []string) error {
			acct, err := a.store.Define(args[0])
			if err != nil {
				return err
			}
			if a.store.Managed(acct) {
				a.out("%s is already an account\n", acct.Label())
				return nil
			}
			if err := a.confirm(yes, "Manage %s as account %s (another tool may own it)", a.tilde(acct.Dir), acct.Label()); err != nil {
				return err
			}
			if acct, err = a.store.Adopt(args[0]); err != nil {
				return err
			}
			a.out("adopted %s (%s); link main's settings with: %s account add %s\n", acct.Label(), a.tilde(acct.Dir), a.cmd, acct.Label())
			return nil
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "do not ask for confirmation")
	return cmd
}

func (a *app) completeUnmanaged(_ *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	un, _ := a.store.Unmanaged()
	names := make([]string, len(un))
	for i, acct := range un {
		names[i] = acct.Label()
	}
	return names, cobra.ShellCompDirectiveNoFileComp
}

type accountRow struct {
	Account  string `json:"account"`
	LoggedIn *bool  `json:"logged_in"` // null when the status is unavailable
	Email    string `json:"email,omitempty"`
	Org      string `json:"org,omitempty"`
	Dir      string `json:"dir"`
	Error    string `json:"error,omitempty"`
}

func (a *app) accountList(asJSON bool) error {
	accts, err := a.store.List()
	if err != nil {
		return err
	}
	stop := a.spin(fmt.Sprintf("checking %d account(s)…", len(accts)))
	statuses := a.statuses(accts, true)
	stop()
	rows := make([]accountRow, len(accts))
	for i, acct := range accts {
		r := accountRow{Account: acct.Label(), Dir: acct.Dir, Email: statuses[i].Status.Email, Org: statuses[i].Status.OrgName}
		if statuses[i].Err != nil {
			r.Error = statuses[i].Err.Error()
		} else {
			r.LoggedIn = &statuses[i].Status.LoggedIn
		}
		rows[i] = r
	}
	if asJSON {
		return a.printJSON(rows)
	}
	w := tabwriter.NewWriter(a.stdout, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "ACCOUNT\tLOGIN\tEMAIL\tDIR")
	for _, r := range rows {
		login, email := "?", r.Email
		if r.LoggedIn != nil {
			login = map[bool]string{true: "yes", false: "no"}[*r.LoggedIn]
		}
		if email == "" {
			email = "-"
		}
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", r.Account, login, email, a.tilde(r.Dir))
	}
	if err := w.Flush(); err != nil {
		return err
	}
	un, _ := a.store.Unmanaged()
	for _, acct := range un {
		a.out("unmanaged: %s (%s account adopt %s)\n", a.tilde(acct.Dir), a.cmd, acct.Label())
	}
	return nil
}

func (a *app) accountAdd(input string) error {
	acct, err := a.store.Define(input)
	if err != nil {
		return err
	}
	if fi, err := os.Stat(acct.Dir); err == nil && fi.IsDir() && !a.store.Managed(acct) {
		return fmt.Errorf("%s exists but is not a %s account (another tool may use it); to manage it, run: %s account adopt %s", a.tilde(acct.Dir), a.cmd, a.cmd, acct.Label())
	}
	ag, err := a.agent(acct.Spec)
	if err != nil {
		return err
	}
	if err := a.provision(acct, ag); err != nil {
		return err
	}
	a.cache.Forget(acct.Label())
	if s, err := ag.Auth(context.Background(), acct.ConfigDir()); err != nil || !s.LoggedIn {
		a.out("next: %s account login %s\n", a.cmd, acct.Label())
	}
	return nil
}

func (a *app) accountLogin(input string) error {
	acct, err := a.store.Get(input)
	if err != nil {
		return err
	}
	ag, err := a.agent(acct.Spec)
	if err != nil {
		return err
	}
	a.cache.Forget(acct.Label())
	a.banner(acct)
	return a.session(ag, acct, ag.LoginArgs())
}

func (a *app) accountLogout(input string) error {
	acct, err := a.store.Get(input)
	if err != nil {
		return err
	}
	ag, err := a.agent(acct.Spec)
	if err != nil {
		return err
	}
	a.cache.Forget(acct.Label())
	if _, err := ag.Output(context.Background(), acct.ConfigDir(), ag.LogoutArgs()...); err != nil {
		return err
	}
	a.out("logged out %s\n", acct.Label())
	return nil
}

// relocator is implemented by agents that record absolute config-dir paths.
type relocator interface {
	Relocate(oldDir, newDir string) error
}

func (a *app) accountRename(input, newName string, force bool) error {
	acct, err := a.store.Get(input)
	if err != nil {
		return err
	}
	if err := a.checkAccountFree(acct, force); err != nil {
		return err
	}
	if _, err := a.store.RenameTarget(acct, newName); err != nil {
		return err
	}
	ag, err := a.agent(acct.Spec)
	if err != nil {
		return err
	}
	// The login is stored under a key derived from the config dir (the macOS keychain item is
	// named after it): sign out first so the old item does not stay behind.
	a.cache.Forget(acct.Label())
	if _, err := ag.Output(context.Background(), acct.ConfigDir(), ag.LogoutArgs()...); err != nil {
		a.warnf("could not log %s out before renaming: %v", acct.Label(), err)
	}
	to, err := a.store.Rename(acct, newName)
	if err != nil {
		return err
	}
	if r, ok := ag.(relocator); ok {
		if err := r.Relocate(acct.Dir, to.Dir); err != nil {
			a.warnf("could not update plugin paths: %v; run: %s account add %s", err, a.cmd, to.Label())
		}
	}
	a.out("renamed %s → %s (%s), logged out\n", acct.Label(), to.Label(), a.tilde(to.Dir))
	a.out("next: %s account login %s\n", a.cmd, to.Label())
	return nil
}

func (a *app) accountRmCmd() *cobra.Command {
	var yes, force bool
	cmd := &cobra.Command{
		Use:               "rm <account>",
		Short:             "Log an account out and move its dir to the trash",
		Args:              oneArg,
		ValidArgsFunction: a.completeAccounts,
		RunE: func(_ *cobra.Command, args []string) error {
			acct, err := a.store.Get(args[0])
			if err != nil {
				return err
			}
			if acct.IsMain() {
				return fmt.Errorf("%w: refusing to remove %s", account.ErrReserved, acct.Label())
			}
			if err := a.checkAccountFree(acct, force); err != nil {
				return err
			}
			if err := a.confirm(yes, "Log out %s and move %s to the trash", acct.Label(), a.tilde(acct.Dir)); err != nil {
				return err
			}
			// Log out while the dir is still where its login is keyed to, then move it.
			loggedOut := false
			if ag, err := a.agent(acct.Spec); err == nil {
				_, err := ag.Output(context.Background(), acct.ConfigDir(), ag.LogoutArgs()...)
				loggedOut = err == nil
			}
			a.cache.Forget(acct.Label())
			dst, err := a.store.MoveToTrash(acct, a.now())
			if err != nil {
				if loggedOut {
					return fmt.Errorf("%s was logged out, but its dir stays in place: %w", acct.Label(), err)
				}
				return err
			}
			a.out("removed account %s (moved to %s)\n", acct.Label(), a.tilde(dst))
			return nil
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "do not ask for confirmation")
	cmd.Flags().BoolVarP(&force, "force", "f", false, "remove even if a session seems to use the account")
	return cmd
}

func (a *app) completeAccounts(_ *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	return a.accountNames(), cobra.ShellCompDirectiveNoFileComp
}

func exactArgs(n int) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if len(args) != n {
			return usagef("usage: %s", cmd.UseLine())
		}
		return nil
	}
}
