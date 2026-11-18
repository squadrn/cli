// Package cli wires sprawl commands to the domain packages. It owns argument parsing and output;
// the domain packages return values and typed errors and never print.
package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/charmbracelet/x/term"
	"github.com/spf13/cobra"

	"sprawling-agents/internal/account"
	"sprawling-agents/internal/config"
	"sprawling-agents/internal/fsx"
	"sprawling-agents/internal/githook"
	"sprawling-agents/internal/provider"
	"sprawling-agents/internal/provider/claude"
	"sprawling-agents/internal/release"
	"sprawling-agents/internal/ui"
)

// buildSigners returns the release signing keys built into this binary ("" when none). Only the
// test binary replaces it, so script tests can sign their own fake releases.
var buildSigners = func() string {
	if release.HasSigners() {
		return release.Signers()
	}
	return ""
}

// Version is set at build time with -ldflags "-X sprawling-agents/internal/cli.Version=…".
var Version = "dev"

const (
	authTimeout = 5 * time.Second
	authTTL     = time.Minute
	authErrTTL  = 15 * time.Second
)

type app struct {
	root *cobra.Command
	// cmd is the name sprawl is installed under (config `command`), used in every message.
	cmd string
	// cfg is the loaded config; cfgErr is reported by commands that depend on it.
	cfg    config.Config
	cfgErr error
	// account is the account named before the subcommand (sprawl personal grid …); "" means main.
	account     string
	store       account.Store
	layout      release.Layout
	stdin       io.Reader
	stdout      io.Writer
	stderr      io.Writer
	interactive bool
	// colorOut and colorErr: the stream is a terminal and colors are not disabled.
	colorOut, colorErr bool
	cache              provider.AuthCache
	now                func() time.Time
	// agents overrides how provider CLIs are found (tests); nil uses the real ones.
	agents func(provider.Spec) (provider.Agent, error)
	// signers are the release signing keys updates must be signed with; "" when the build has none.
	signers string
	// backgroundCheck overrides how the update check is started (tests).
	backgroundCheck func()
	// gitChecked: the Git version was checked in this run (openRepo).
	gitChecked bool
}

// Run executes sprawl with args (program name excluded) and returns the process exit code.
func Run(args []string) int {
	// Hooks run on every tool call and git hook: dispatch them before anything that can fail.
	if len(args) > 0 && args[0] == "hook" {
		return runHook(args[1:], hookEnv{stdin: os.Stdin, stdout: os.Stdout, stderr: os.Stderr, now: time.Now})
	}
	cfg, cfgErr := config.Load()
	name := cfg.CommandName()
	err := run(args, cfg, cfgErr)
	var nf account.NotFoundError
	switch {
	case err == nil, silent(err):
	case errors.As(err, &nf):
		_, _ = fmt.Fprintf(os.Stderr, "%s: %s (run: %s account add %s)\n", name, err, name, nf.Ref)
	default:
		_, _ = fmt.Fprintf(os.Stderr, "%s: %s\n", name, err)
	}
	return exitCode(err)
}

func isTerminal(f *os.File) bool { return term.IsTerminal(f.Fd()) }

func run(args []string, cfg config.Config, cfgErr error) error {
	store, err := newStore(cfg, cfgErr)
	if err != nil {
		return err
	}
	noColor := os.Getenv("NO_COLOR") != "" || !enableVT()
	a := &app{
		cmd:         cfg.CommandName(),
		cfg:         cfg,
		cfgErr:      cfgErr,
		store:       store,
		layout:      release.DefaultLayout(store.Home),
		stdin:       os.Stdin,
		stdout:      os.Stdout,
		stderr:      os.Stderr,
		interactive: isTerminal(os.Stdin) && isTerminal(os.Stdout),
		colorOut:    !noColor && isTerminal(os.Stdout),
		colorErr:    !noColor && isTerminal(os.Stderr),
		cache: provider.AuthCache{
			Path:   filepath.Join(config.DirsFor(store.Home, config.Slug).Cache, "auth.json"),
			TTL:    authTTL,
			ErrTTL: authErrTTL,
		},
		now: time.Now,
	}
	a.signers = buildSigners()
	root := a.rootCmd()
	r, err := parseRoute(args, a.routeTable())
	if err != nil {
		return err
	}
	if r.dir != "" {
		if err := os.Chdir(r.dir); err != nil {
			return usagef("-C %s: %v", r.dir, err)
		}
	}
	if r.noColor {
		a.colorOut, a.colorErr = false, false
	}
	a.account = r.account
	a.updateNotice(r)
	switch {
	case r.pick:
		return a.pick()
	case r.sub == "":
		return a.runAgent(r.account, r.args)
	default:
		root.SetArgs(append([]string{r.sub}, r.args...))
		return root.Execute()
	}
}

// newStore is the account store every entry point (commands and hooks) uses.
func newStore(cfg config.Config, cfgErr error) (account.Store, error) {
	store, err := account.NewStore()
	if err != nil {
		return store, err
	}
	if cfgErr == nil {
		store.DefaultProvider = cfg.DefaultProvider
	}
	// Accounts provisioned before account markers carry sprawl's plugin (or a previous name's).
	store.HasPlugin = func(spec provider.Spec, dir string) bool {
		if spec.ID != provider.Claude.ID {
			return false
		}
		for _, id := range append([]string{account.PluginID}, account.LegacyPluginIDs()...) {
			if claude.HasPlugin(dir, id) {
				return true
			}
		}
		return false
	}
	return store, nil
}

func (a *app) routeTable() routeTable {
	subs := map[string]bool{"help": true, cobra.ShellCompRequestCmd: true, cobra.ShellCompNoDescRequestCmd: true}
	for _, c := range a.root.Commands() {
		subs[c.Name()] = true
	}
	return routeTable{
		isAccount:   a.store.Exists,
		subcommands: subs,
		passthrough: func(w string) bool {
			for _, spec := range provider.All() {
				if slices.Contains(spec.Subcommands, w) {
					return true
				}
			}
			return false
		},
		suggestions: func() []string {
			var out []string
			for _, c := range a.root.Commands() {
				if !c.Hidden {
					out = append(out, c.Name())
				}
			}
			accts, _ := a.store.List()
			for _, acct := range accts {
				out = append(out, acct.Name)
			}
			return out
		},
		interactive: a.interactive,
		cmd:         a.cmd,
	}
}

func (a *app) rootCmd() *cobra.Command {
	c := a.cmd
	root := &cobra.Command{
		Use:   c + " [<account>] [<command> | agent args... | -- prompt]",
		Short: "Run coding agents across multiple accounts and parallel worktrees",
		Long: fmt.Sprintf(`%[1]s runs coding agents (today: Claude Code) as one of several accounts on this machine.

Accounts are provider/name: claude/main is ~/.claude, claude/personal is ~/.claude-personal.`, c),
		Example: fmt.Sprintf(`  %[1]s                          pick an account interactively
  %[1]s personal                 run as claude/personal (also: -a personal, claude/personal)
  %[1]s personal -p "hi"         agent flags go to the agent CLI
  %[1]s personal -- "fix the bug"  a prompt goes after --
  %[1]s account add work         create claude/work, then: %[1]s account login work
  %[1]s grid ~/repo feat-a personal:fix-b`, c),
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.ArbitraryArgs,
		RunE:          func(_ *cobra.Command, args []string) error { return a.runAgent(a.account, args) },
		ValidArgsFunction: func(_ *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
			if len(args) > 0 {
				return nil, cobra.ShellCompDirectiveNoFileComp
			}
			return a.accountNames(), cobra.ShellCompDirectiveNoFileComp
		},
	}
	root.SetIn(a.stdin)
	root.SetOut(a.stdout)
	root.SetErr(a.stderr)
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error { return usagef("%v", err) })
	var noColor bool
	var acct, dir string
	root.PersistentFlags().BoolVar(&noColor, "no-color", false, "disable colors (also: NO_COLOR=1)")
	root.PersistentFlags().StringVarP(&acct, "account", "a", "", "account to run as (provider/name or name)")
	root.PersistentFlags().StringVarP(&dir, "dir", "C", "", "run as if started in this directory")
	root.PersistentPreRunE = func(*cobra.Command, []string) error {
		if noColor {
			a.colorOut, a.colorErr = false, false
		}
		if acct != "" {
			a.account = acct
		}
		if dir != "" {
			if err := os.Chdir(dir); err != nil {
				return usagef("-C %s: %v", dir, err)
			}
		}
		return nil
	}
	root.AddGroup(
		&cobra.Group{ID: "agents", Title: "Agents:"},
		&cobra.Group{ID: "accounts", Title: "Accounts:"},
		&cobra.Group{ID: "setup", Title: "Setup:"},
	)
	add := func(group string, cmds ...*cobra.Command) {
		for _, cmd := range cmds {
			cmd.GroupID = group
			root.AddCommand(cmd)
		}
	}
	add("agents", a.wtCmd(), a.gridCmd(), a.psCmd(), a.swapCmd(), a.gcCmd(), a.depsCmd())
	add("accounts", a.accountCmd(), a.usageCmd())
	add("setup", a.doctorCmd(), a.configCmd(), a.selfUpdateCmd(), a.selfUninstallCmd(), a.versionCmd())
	root.AddCommand(a.selfInstallCmd())
	root.SetHelpCommandGroupID("setup")
	root.SetCompletionCommandGroupID("setup")
	root.InitDefaultHelpCmd()
	root.InitDefaultCompletionCmd()
	a.root = root
	return root
}

// out and errf print user-facing messages; a failed write to the terminal is not actionable.
func (a *app) out(format string, args ...any)  { _, _ = fmt.Fprintf(a.stdout, format, args...) }
func (a *app) errf(format string, args ...any) { _, _ = fmt.Fprintf(a.stderr, format, args...) }

func (a *app) warnf(format string, args ...any) {
	a.errf("%s: %s\n", a.cmd, a.paint(a.colorErr, "33", "warning: ")+fmt.Sprintf(format, args...))
}

// paint wraps s in an ANSI color when enabled.
func (a *app) paint(enabled bool, code, s string) string {
	if !enabled {
		return s
	}
	return "\033[" + code + "m" + s + "\033[0m"
}

func (a *app) printJSON(v any) error {
	enc := json.NewEncoder(a.stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// confirm asks a yes/no question on the terminal. yes skips it; without a terminal the command
// must be re-run with --yes. "no" (or anything but y/yes) aborts with exit 1.
func (a *app) confirm(yes bool, format string, args ...any) error {
	if yes {
		return nil
	}
	if !a.interactive {
		return usagef("%s; re-run with --yes to confirm", fmt.Sprintf(format, args...))
	}
	a.errf("%s? [y/N] ", fmt.Sprintf(format, args...))
	line, err := bufio.NewReader(a.stdin).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	if s := strings.ToLower(strings.TrimSpace(line)); s != "y" && s != "yes" {
		return errAborted
	}
	return nil
}

// spin shows a spinner on stderr while work runs, when stderr is a terminal.
func (a *app) spin(msg string) func() {
	if !a.colorErr {
		return func() {}
	}
	return ui.Spin(a.stderr, msg)
}

func (a *app) versionCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "version",
		Short: "Print the sprawl version",
		Args:  noArgs,
		RunE: func(*cobra.Command, []string) error {
			if asJSON {
				return a.printJSON(map[string]string{"version": Version})
			}
			a.out("sprawl %s\n", Version)
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print JSON")
	return cmd
}

// agent returns the driver for a provider, with the git-level guard in its environment.
func (a *app) agent(spec provider.Spec) (provider.Agent, error) {
	if a.agents != nil {
		return a.agents(spec)
	}
	if spec.ID != provider.Claude.ID {
		return nil, fmt.Errorf("provider %q is not supported yet", spec.ID)
	}
	cl, err := claude.Find()
	if err != nil {
		return nil, err
	}
	cl.ExtraEnv = a.sessionEnv()
	return cl, nil
}

// sessionEnv points core.hooksPath at the git-level guard for sessions sprawl starts.
func (a *app) sessionEnv() []string {
	if a.cfgErr != nil || !a.cfg.GitHooksEnabled() {
		return nil
	}
	dir := a.layout.GitHooks()
	cfg, err := config.Path()
	if err != nil || !fsx.IsFile(a.layout.Binary()) {
		return nil
	}
	// Shims from an older release, or for another config path, are rewritten before use.
	if !githook.Installed(dir, a.layout.Binary(), cfg) && githook.Install(dir, a.layout.Binary(), cfg) != nil {
		return nil
	}
	return githook.Entries(os.Environ(), dir)
}

func (a *app) runAgent(accountInput string, args []string) error {
	acct, err := a.resolve(accountInput)
	if err != nil {
		return err
	}
	ag, err := a.agent(acct.Spec)
	if err != nil {
		return err
	}
	a.banner(acct)
	return a.session(ag, acct, args)
}

func (a *app) resolve(input string) (account.Account, error) {
	if input == "" {
		input = account.Main
	}
	return a.store.Get(input)
}

// banner names the account a session runs as and warns when it is known to be logged out.
func (a *app) banner(acct account.Account) {
	if !a.colorErr {
		return
	}
	a.errf("%s: account \033[1m%s\033[0m (%s)\n", a.cmd, acct.Label(), a.tilde(acct.Dir))
	if s, ok := a.cache.Cached(acct.Label()); ok && !s.LoggedIn {
		a.warnf("%s is not logged in; run: %s account login %s", acct.Label(), a.cmd, acct.Label())
	}
}

// session starts an interactive agent session as acct: shared-file copies are refreshed first
// (Windows without links) and executables left over from earlier updates are cleaned up.
func (a *app) session(ag provider.Agent, acct account.Account, args []string) error {
	return a.sessionIn(ag, acct, args, "")
}

// sessionIn is session for `wt <name>`: the session is recorded as using that worktree, which
// the agent CLI may only create once it runs.
func (a *app) sessionIn(ag provider.Agent, acct account.Account, args []string, wt string) error {
	r := a.store.RefreshShared(acct)
	for _, c := range r.Conflicts {
		a.warnf("%s was edited in %s, so it no longer follows the main account; remove it and run: %s account add %s", a.tilde(c), acct.Label(), a.cmd, acct.Label())
	}
	for _, c := range r.Replaced {
		a.warnf("%s was replaced in %s, so it no longer follows the main account; remove it and run: %s account add %s", a.tilde(c), acct.Label(), a.cmd, acct.Label())
	}
	fsx.CleanOld(filepath.Dir(a.layout.Binary()))
	fsx.CleanOld(a.layout.BinDir)
	defer a.markInUse(acct, wt)()
	return ag.Exec(acct.ConfigDir(), args)
}

func (a *app) tilde(path string) string {
	if rel, err := filepath.Rel(a.store.Home, path); err == nil && !strings.HasPrefix(rel, "..") {
		return filepath.Join("~", rel)
	}
	return path
}

func (a *app) accountNames() []string {
	accts, _ := a.store.List()
	names := make([]string, 0, len(accts))
	for _, acct := range accts {
		names = append(names, acct.Label())
	}
	return names
}

// statuses queries login status for accts, grouped per provider. refresh bypasses the cache.
func (a *app) statuses(accts []account.Account, refresh bool) []provider.AuthResult {
	results := make([]provider.AuthResult, len(accts))
	bySpec := map[string][]int{}
	for i, acct := range accts {
		bySpec[acct.Provider] = append(bySpec[acct.Provider], i)
	}
	for _, idx := range bySpec {
		spec := accts[idx[0]].Spec
		ag, err := a.agent(spec)
		if err != nil {
			for _, i := range idx {
				results[i] = provider.AuthResult{Err: err}
			}
			continue
		}
		targets := make([]provider.Target, len(idx))
		for j, i := range idx {
			targets[j] = provider.Target{Key: accts[i].Label(), ConfigDir: accts[i].ConfigDir()}
		}
		var res []provider.AuthResult
		if refresh {
			res = a.cache.Refresh(context.Background(), ag, targets, authTimeout)
		} else {
			res = a.cache.Statuses(context.Background(), ag, targets, authTimeout)
		}
		for j, i := range idx {
			results[i] = res[j]
		}
	}
	return results
}

const addAccountItem = "+ add a new account"

func (a *app) pick() error {
	accts, err := a.store.List()
	if err != nil {
		return err
	}
	stop := a.spin("checking accounts…")
	statuses := a.statuses(accts, false)
	stop()
	items := make([]ui.Item, 0, len(accts)+1)
	for i, acct := range accts {
		item := ui.Item{Label: acct.Label(), Detail: statuses[i].Status.Email, Active: statuses[i].Status.LoggedIn}
		switch {
		case statuses[i].Err != nil:
			item.Status = "status unavailable"
		case !item.Active:
			item.Status = "not logged in"
		}
		items = append(items, item)
	}
	items = append(items, ui.Item{Label: addAccountItem})

	choice, err := ui.Pick(a.cmd+" ›", items, a.stdin, a.stderr, a.colorErr)
	if err != nil {
		return err
	}
	if choice == len(accts) {
		return a.addInteractively()
	}
	acct := accts[choice]
	ag, err := a.agent(acct.Spec)
	if err != nil {
		return err
	}
	if statuses[choice].Err == nil && !statuses[choice].Status.LoggedIn {
		if err := a.confirm(false, "%s is not logged in. Log in now", acct.Label()); err == nil {
			a.cache.Forget(acct.Label())
			return a.session(ag, acct, ag.LoginArgs())
		} else if !errors.Is(err, errAborted) {
			return err
		}
	}
	a.banner(acct)
	return a.session(ag, acct, nil)
}

func (a *app) addInteractively() error {
	a.errf("New account (name or provider/name, e.g. work): ")
	line, err := bufio.NewReader(a.stdin).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	acct, err := a.store.Define(strings.TrimSpace(line))
	if err != nil {
		return err
	}
	ag, err := a.agent(acct.Spec)
	if err != nil {
		return err
	}
	if err := a.provision(acct, ag); err != nil {
		return err
	}
	a.cache.Forget(acct.Label())
	return a.session(ag, acct, ag.LoginArgs())
}

// marketplaceDir locates the local sprawl plugin marketplace: $SPRAWL_MARKETPLACE, else the
// installed release dir. "" means no plugin is available to install.
func (a *app) marketplaceDir() string {
	if dir := os.Getenv("SPRAWL_MARKETPLACE"); dir != "" {
		if _, err := os.Stat(filepath.Join(dir, ".claude-plugin", "marketplace.json")); err == nil {
			return dir
		}
	}
	return a.layout.Marketplace()
}

func (a *app) provision(acct account.Account, r provider.Runner) error {
	if a.cfgErr != nil {
		return a.cfgErr
	}
	p := account.Provisioner{Store: a.store, Runner: r, SharedMCP: a.cfg.SharedMCP, Marketplace: a.marketplaceDir()}
	rep, err := p.Provision(context.Background(), acct)
	a.out("account %s → %s\n", acct.Label(), a.tilde(acct.Dir))
	for _, l := range rep.Linked {
		a.out("  linked %s\n", a.tilde(l))
	}
	for _, l := range rep.Unlinked {
		a.out("  removed stale link %s\n", a.tilde(l))
	}
	for _, c := range rep.Conflicts {
		a.out("  kept %s (exists and is not a link to main)\n", a.tilde(c))
	}
	for _, m := range rep.MCPAdded {
		a.out("  mcp added %s\n", m)
	}
	for _, m := range rep.MCPSkipped {
		a.out("  mcp skipped %s (has env/headers; add it manually: %s %s -- mcp add ...)\n", m, a.cmd, acct.Label())
	}
	for _, m := range rep.MCPMissing {
		a.out("  mcp missing %s (not configured in main)\n", m)
	}
	if rep.PluginInstalled {
		a.out("  plugin installed %s\n", account.PluginID)
	}
	return err
}

func noArgs(cmd *cobra.Command, args []string) error {
	if len(args) > 0 {
		return usagef("%s takes no arguments", cmd.CommandPath())
	}
	return nil
}

func oneArg(cmd *cobra.Command, args []string) error {
	if len(args) != 1 {
		return usagef("usage: %s", cmd.UseLine())
	}
	return nil
}

// subcommandArgs rejects unknown subcommands of a command group, suggesting close ones.
func subcommandArgs(cmd *cobra.Command, args []string) error {
	if len(args) == 0 {
		return nil
	}
	var names []string
	for _, c := range cmd.Commands() {
		names = append(names, c.Name())
	}
	msg := fmt.Sprintf("unknown %s command %q", cmd.Name(), args[0])
	if s := suggest(args[0], names); len(s) > 0 {
		msg += fmt.Sprintf(" (did you mean %s?)", strings.Join(s, " or "))
	}
	return usagef("%s; see: %s --help", msg, cmd.CommandPath())
}
