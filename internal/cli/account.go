package cli

import (
	"bufio"
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/squadrn/squadrn-cli/internal/account"
	"github.com/squadrn/squadrn-cli/internal/provider"
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
		a.accountAddCmd(),
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
	var force, yes bool
	cmd := &cobra.Command{
		Use:               "rename <account> <new-name>",
		Short:             "Rename an account (logs it out first; log in again afterwards)",
		Args:              exactArgs(2),
		RunE:              func(_ *cobra.Command, args []string) error { return a.accountRename(args[0], args[1], force, yes) },
		ValidArgsFunction: a.completeAccounts,
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "do not ask for confirmation")
	cmd.Flags().BoolVarP(&force, "force", "f", false, "rename even if a session seems to use the account")
	return cmd
}

func (a *app) accountAdoptCmd() *cobra.Command {
	var yes bool
	var profile string
	cmd := &cobra.Command{
		Use:               "adopt [<provider>/]<name>",
		Short:             "Make an existing config dir (e.g. ~/.claude-<name>) an account and set it up",
		Long:              "Make an existing config dir an account, then set it up like account add. Its own settings are\nkept: they become the account's copy, and keys that differ from main's stay as they are.",
		Args:              oneArg,
		ValidArgsFunction: a.completeUnmanaged,
		RunE: func(_ *cobra.Command, args []string) error {
			acct, err := a.store.Define(args[0])
			if err != nil {
				return err
			}
			if a.store.Managed(acct) {
				a.out("%s is already an account; to repair it: %s account add %s\n", acct.Label(), a.cmd, acct.Label())
				return nil
			}
			if err := a.confirm(yes, "Manage %s as account %s (another tool may own it)", a.tilde(acct.Dir), acct.Label()); err != nil {
				return err
			}
			if acct, err = a.store.Adopt(args[0]); err != nil {
				return err
			}
			a.out("adopted %s (%s)\n", acct.Label(), a.tilde(acct.Dir))
			return a.accountAdd(acct.Label(), addOptions{profile: profile, yes: yes})
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "do not ask; use the defaults")
	cmd.Flags().StringVar(&profile, "profile", "", "settings profile to apply (default: none, keep its settings)")
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

// confirmRelink asks before moving the account's own copies of shared files aside: at a
// terminal as a plan question (no keeps them), else it needs yes. false means there is nothing
// to move or the answer was no.
func (a *app) confirmRelink(in *bufio.Reader, acct account.Account, ag provider.Agent, ask, yes bool) (bool, error) {
	conflicts := a.provisioner(ag).Conflicts(acct)
	if len(conflicts) == 0 {
		return false, nil
	}
	names := make([]string, len(conflicts))
	for i, c := range conflicts {
		names[i] = a.tilde(c)
	}
	if ask {
		return a.askNo(in, "relink", "move "+strings.Join(names, ", ")+" aside (.bak-<time>) and link main's?")
	}
	return true, a.confirm(yes, "Move %s aside (.bak-<time>) and link to the main account's", strings.Join(names, ", "))
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
	a.banner(acct)
	err = a.runLogin(ag, acct)
	a.cache.Forget(acct.Label())
	if err != nil {
		return err
	}
	s, err := ag.Auth(context.Background(), acct.ConfigDir())
	if err != nil || !s.LoggedIn {
		return fmt.Errorf("%s is still not logged in; try again: %s account login %s", acct.Label(), a.cmd, acct.Label())
	}
	if s.Email != "" {
		a.out("logged in to %s as %s\n", acct.Label(), s.Email)
	} else {
		a.out("logged in to %s\n", acct.Label())
	}
	return nil
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
	if fl, ok := ag.(provider.FileLogout); ok {
		moved, err := a.store.TrashFiles(acct, fl.LogoutFiles(acct.ConfigDir()), a.now())
		if err != nil {
			return err
		}
		if len(moved) == 0 {
			a.out("%s holds no login\n", acct.Label())
			return nil
		}
		a.out("moved the login files of %s to the trash (%s)\n", acct.Label(), a.tilde(filepath.Dir(moved[0])))
	} else if _, err := ag.Output(context.Background(), acct.ConfigDir(), ag.LogoutArgs()...); err != nil {
		return err
	}
	a.out("logged out %s\n", acct.Label())
	return nil
}

// runLogin runs the CLI's login for acct, after the hint of a CLI whose login is a session that
// does not end by itself.
func (a *app) runLogin(ag provider.Agent, acct account.Account) error {
	if h, ok := ag.(provider.LoginHinter); ok {
		a.out("%s\n", h.LoginHint())
	}
	return ag.Run(acct.ConfigDir(), ag.LoginArgs())
}

func (a *app) accountRename(input, newName string, force, yes bool) error {
	acct, err := a.store.Get(input)
	if err != nil {
		return err
	}
	if err := a.checkAccountFree(acct, force); err != nil {
		return err
	}
	to, err := a.store.RenameTarget(acct, newName)
	if err != nil {
		return err
	}
	ag, err := a.agent(acct.Spec)
	if err != nil {
		return err
	}
	// A login kept in files of the account (FileLogout) moves with its dir.
	_, keepsLogin := ag.(provider.FileLogout)
	if keepsLogin {
		err = a.confirm(yes, "Rename %s to %s", acct.Label(), to.Label())
	} else {
		err = a.confirm(yes, "Rename %s to %s; it will be logged out and has to log in again", acct.Label(), to.Label())
	}
	if err != nil {
		return err
	}
	// The login is stored under a key derived from the config dir (the macOS keychain item is
	// named after it): sign out first so the old item does not stay behind.
	a.cache.Forget(acct.Label())
	if !keepsLogin {
		if _, err := ag.Output(context.Background(), acct.ConfigDir(), ag.LogoutArgs()...); err != nil {
			a.warnf("cannot log %s out before renaming: %v", acct.Label(), err)
		}
	}
	to, err = a.store.Rename(acct, newName)
	if err != nil {
		return err
	}
	if err := usageStore(a.store.Home).Rename(acct.Provider, acct.Name, to.Name); err != nil {
		a.warnf("cannot move the usage record of %s: %v", acct.Label(), err)
	}
	if r, ok := ag.(provider.Relocator); ok {
		if err := r.Relocate(acct.Dir, to.Dir); err != nil {
			a.warnf("cannot update plugin paths: %v; run: %s account add %s", err, a.cmd, to.Label())
		}
	}
	if keepsLogin {
		a.out("renamed %s → %s (%s)\n", acct.Label(), to.Label(), a.tilde(to.Dir))
		return nil
	}
	a.out("renamed %s → %s (%s), logged out\n", acct.Label(), to.Label(), a.tilde(to.Dir))
	a.out("next: %s account login %s\n", a.cmd, to.Label())
	return nil
}

func (a *app) accountRmCmd() *cobra.Command {
	var yes, force, purge bool
	cmd := &cobra.Command{
		Use:   "rm <account>",
		Short: "Log an account out and move its dir to the trash (--purge: delete it for good)",
		Long: "Log an account out and move its dir to the trash.\n\n" +
			"--purge deletes the dir permanently instead and removes every record squadrn keeps of the\n" +
			"account: its usage record, its handoffs, its in-use records and its cached login status.\n" +
			"At a terminal it asks you to type the account (e.g. claude/personal); --yes skips that,\n" +
			"and without a terminal it is required.",
		Args:              oneArg,
		ValidArgsFunction: a.completeAccounts,
		RunE: func(_ *cobra.Command, args []string) error {
			acct, err := a.store.Get(args[0])
			if err != nil {
				return err
			}
			if acct.IsMain() {
				return account.MainError{Account: acct}
			}
			if err := a.checkAccountFree(acct, force); err != nil {
				return err
			}
			if purge {
				return a.accountPurge(acct, yes)
			}
			if err := a.confirm(yes, "Log out %s and move %s to the trash", acct.Label(), a.tilde(acct.Dir)); err != nil {
				return err
			}
			// Log out while the dir is still where its login is keyed to, then move it.
			loggedOut := a.logoutQuiet(acct) == nil
			a.cache.Forget(acct.Label())
			dst, err := a.store.MoveToTrash(acct, a.now())
			if err != nil {
				if loggedOut {
					return fmt.Errorf("%s was logged out, but its dir stays in place: %w", acct.Label(), err)
				}
				return err
			}
			if _, err := usageStore(a.store.Home).Remove(acct.Provider, acct.Name); err != nil {
				a.warnf("cannot remove the usage record of %s: %v", acct.Label(), err)
			}
			a.out("removed account %s (moved to %s)\n", acct.Label(), a.tilde(dst))
			return nil
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "do not ask for confirmation")
	cmd.Flags().BoolVarP(&force, "force", "f", false, "remove even if a session seems to use the account")
	cmd.Flags().BoolVar(&purge, "purge", false, "delete the dir permanently and every record squadrn keeps of the account")
	return cmd
}

// logoutQuiet signs acct out without printing, before its dir is trashed or deleted. A login
// kept in files of the account (FileLogout) goes with the dir.
func (a *app) logoutQuiet(acct account.Account) error {
	ag, err := a.agent(acct.Spec)
	if err != nil {
		return err
	}
	if _, ok := ag.(provider.FileLogout); ok {
		return nil
	}
	_, err = ag.Output(context.Background(), acct.ConfigDir(), ag.LogoutArgs()...)
	return err
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
