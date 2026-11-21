package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

// completionLine is a shell completion request (`__complete <words…> <toComplete>`) split the
// way parseRoute splits a command line, so `squadrn <account> …` completes like `squadrn …`.
type completionLine struct {
	account string
	dir     string
	// rest are the complete words after the globals and the account.
	rest       []string
	toComplete string
	// flagValue: toComplete is the value of a global flag (-a, -C); cobra completes it.
	flagValue bool
	// invalid: the words before toComplete do not parse (an account given twice, …).
	invalid bool
}

func splitCompletion(words []string, t routeTable) completionLine {
	var l completionLine
	if len(words) == 0 {
		return l
	}
	done := words[:len(words)-1]
	l.toComplete = words[len(words)-1]
	if n := len(done); n > 0 {
		switch done[n-1] {
		case "-a", "--account", "-C", "--dir":
			l.flagValue = true
			return l
		}
	}
	var r route
	rest, err := r.globals(done, false)
	if err == nil && r.account == "" && len(rest) > 0 && !t.subcommands[rest[0]] && t.isAccount(rest[0]) {
		r.account = rest[0]
		rest, err = r.globals(rest[1:], true)
	}
	l.account, l.dir, l.rest, l.invalid = r.account, r.dir, rest, err != nil
	return l
}

// complete answers a shell completion request. Squadrn's router reads the account before the
// subcommand, which cobra does not know about: the account is taken out of the words here, and
// the first word after it (account, subcommand or agent subcommand) is completed here too.
// Nothing here runs an agent CLI.
func (a *app) complete(req string, words []string) error {
	a.completionFlags()
	t := a.routeTable()
	l := splitCompletion(words, t)
	noDesc := req == cobra.ShellCompNoDescRequestCmd
	switch {
	case len(words) == 0, l.flagValue:
		return a.cobraComplete(req, words)
	case l.invalid:
		return a.printCompletions(noDesc, nil, cobra.ShellCompDirectiveNoFileComp)
	}
	if l.dir != "" {
		_ = os.Chdir(l.dir) // worktree names are those of the repository -C names
	}
	a.account = l.account
	switch {
	case len(l.rest) == 0 && strings.HasPrefix(l.toComplete, "-"):
		return a.cobraComplete(req, []string{l.toComplete})
	case len(l.rest) == 0:
		return a.printCompletions(noDesc, a.firstWords(l.account, l.toComplete), cobra.ShellCompDirectiveNoFileComp|cobra.ShellCompDirectiveKeepOrder)
	case l.rest[0] == "--":
		return a.printCompletions(noDesc, nil, cobra.ShellCompDirectiveNoFileComp)
	case t.subcommands[l.rest[0]]:
		return a.cobraComplete(req, append(l.rest, l.toComplete))
	case strings.HasPrefix(l.rest[0], "-"), t.passthrough(l.account, l.rest[0]):
		// The agent's own flags and subcommands: squadrn does not know their arguments.
		return a.printCompletions(noDesc, nil, cobra.ShellCompDirectiveDefault)
	}
	return a.printCompletions(noDesc, nil, cobra.ShellCompDirectiveNoFileComp)
}

func (a *app) cobraComplete(req string, words []string) error {
	a.root.SetArgs(append([]string{req}, words...))
	return a.root.Execute()
}

// completionFlags completes the values of the global flags.
func (a *app) completionFlags() {
	_ = a.root.RegisterFlagCompletionFunc("account", func(_ *cobra.Command, _ []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
		return a.accountCompletions(toComplete), cobra.ShellCompDirectiveNoFileComp | cobra.ShellCompDirectiveKeepOrder
	})
	_ = a.root.RegisterFlagCompletionFunc("dir", func(*cobra.Command, []string, string) ([]cobra.Completion, cobra.ShellCompDirective) {
		return nil, cobra.ShellCompDirectiveFilterDirs
	})
}

// firstWords completes the first word after the globals: accounts, then subcommands. After an
// account: the subcommands that run as an account, then the agent CLI's own subcommands.
func (a *app) firstWords(account, toComplete string) []cobra.Completion {
	var out []cobra.Completion
	if account == "" {
		out = a.accountCompletions(toComplete)
	}
	for _, c := range a.root.Commands() {
		if !c.IsAvailableCommand() && c.Name() != "help" {
			continue
		}
		if account != "" && (c.Name() == "help" || !takesAccount(c)) {
			continue
		}
		if strings.HasPrefix(c.Name(), toComplete) {
			out = append(out, cobra.CompletionWithDesc(c.Name(), c.Short))
		}
	}
	if account != "" {
		if acct, err := a.store.Get(account); err == nil {
			for _, w := range acct.Spec.Subcommands {
				if strings.HasPrefix(w, toComplete) {
					out = append(out, cobra.CompletionWithDesc(w, acct.Spec.Name+" subcommand"))
				}
			}
		}
	}
	return out
}

// accountCompletions offers each account by the shortest name that runs it: its bare name when
// that names only this account (personal, main), else provider/name. A prefix its bare name does
// not match (cl, claude/) is matched against its provider/name.
func (a *app) accountCompletions(toComplete string) []cobra.Completion {
	accts, _ := a.store.List()
	var out []cobra.Completion
	for _, acct := range accts {
		label := acct.Label()
		word, desc := label, acct.Spec.Name
		if x, err := a.store.Get(acct.Name); err == nil && x.Ref == acct.Ref {
			word, desc = acct.Name, label
		}
		switch {
		case strings.HasPrefix(word, toComplete):
			out = append(out, cobra.CompletionWithDesc(word, desc))
		case strings.HasPrefix(label, toComplete):
			out = append(out, cobra.CompletionWithDesc(label, acct.Spec.Name))
		}
	}
	return out
}

// printCompletions answers in cobra's __complete format: one candidate per line (TAB, then its
// description), then :directive.
func (a *app) printCompletions(noDesc bool, comps []cobra.Completion, d cobra.ShellCompDirective) error {
	var b strings.Builder
	for _, c := range comps {
		if noDesc {
			c, _, _ = strings.Cut(c, "\t")
		}
		b.WriteString(strings.TrimSpace(c) + "\n")
	}
	fmt.Fprintf(&b, ":%d\n", d)
	_, err := fmt.Fprint(a.stdout, b.String())
	return err
}
