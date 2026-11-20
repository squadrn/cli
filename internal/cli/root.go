// Package cli wires squadrn commands to the domain packages. It owns argument parsing and output;
// the domain packages return values and typed errors and never print.
package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/charmbracelet/x/term"
	"github.com/spf13/cobra"

	"github.com/squadrn/squadrn/internal/account"
	"github.com/squadrn/squadrn/internal/config"
	"github.com/squadrn/squadrn/internal/fsx"
	"github.com/squadrn/squadrn/internal/githook"
	"github.com/squadrn/squadrn/internal/provider"
	_ "github.com/squadrn/squadrn/internal/provider/all"
	"github.com/squadrn/squadrn/internal/release"
	"github.com/squadrn/squadrn/internal/ui"
)

// buildSigners returns the release signing keys built into this binary ("" when none). Only the
// test binary replaces it, so script tests can sign their own fake releases.
var buildSigners = release.Signers

// Version is set at build time with -ldflags "-X github.com/squadrn/squadrn/internal/cli.Version=…".
var Version = "dev"

const (
	authTimeout = 5 * time.Second
	authTTL     = time.Minute
	authErrTTL  = 15 * time.Second
)

type app struct {
	root *cobra.Command
	// cmd is the name squadrn is installed under (config `command`), used in every message.
	cmd string
	// cfg is the loaded config; cfgErr is reported by commands that depend on it.
	cfg    config.Config
	cfgErr error
	// account is the account named before the subcommand (squadrn personal grid …); "" means main.
	account     string
	store       account.Store
	layout      release.Layout
	stdin       io.Reader
	stdout      io.Writer
	stderr      io.Writer
	interactive bool
	// ttyErr: stderr is a terminal that understands ANSI cursor sequences, so banners, notices,
	// spinners and the picker can be shown. NO_COLOR and --no-color do not change it.
	ttyErr bool
	// colorOut and colorErr: the stream is a terminal and colors are not disabled.
	colorOut, colorErr bool
	// cfgWarned: the invalid-config warning was shown in this run.
	cfgWarned bool
	cache     provider.AuthCache
	now       func() time.Time
	// agents overrides how provider CLIs are found (tests); nil uses the real ones.
	agents func(provider.Spec) (provider.Agent, error)
	// signers are the release signing keys updates must be signed with; "" when the build has none.
	signers string
	// backgroundCheck overrides how the update check is started (tests).
	backgroundCheck func()
	// gitChecked: the Git version was checked in this run (openRepo).
	gitChecked bool
}

// Run executes squadrn with args (program name excluded) and returns the process exit code.
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
	case errors.As(err, &nf) && unmanaged(nf.Ref.String()):
		_, _ = fmt.Fprintf(os.Stderr, "%s: %s; its dir exists but is not a %s account (run: %s account adopt %s)\n", name, err, config.Slug, name, nf.Ref)
	case errors.As(err, &nf):
		_, _ = fmt.Fprintf(os.Stderr, "%s: %s (run: %s account add %s)\n", name, err, name, nf.Ref)
	default:
		_, _ = fmt.Fprintf(os.Stderr, "%s: %s\n", name, err)
	}
	return exitCode(err)
}

func unmanaged(ref string) bool {
	s, err := account.NewStore()
	return err == nil && s.IsUnmanaged(ref)
}

func isTerminal(f *os.File) bool { return term.IsTerminal(f.Fd()) }

func run(args []string, cfg config.Config, cfgErr error) error {
	store, err := newStore(cfg, cfgErr)
	if err != nil {
		return err
	}
	vt := enableVT()
	noColor := os.Getenv("NO_COLOR") != "" || !vt
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
		ttyErr:      vt && isTerminal(os.Stderr),
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
	return store, nil
}

func (a *app) routeTable() routeTable {
	subs := map[string]bool{"help": true, cobra.ShellCompRequestCmd: true, cobra.ShellCompNoDescRequestCmd: true}
	for _, c := range a.root.Commands() {
		subs[c.Name()] = true
	}
	return routeTable{
		isAccount:   a.store.Exists,
		isUnmanaged: a.store.IsUnmanaged,
		subcommands: subs,
		passthrough: func(acct, w string) bool {
			specs := provider.All()
			if acct == "" {
				specs = []provider.Spec{a.defaultSpec()}
			} else if x, err := a.store.Get(acct); err == nil {
				specs = []provider.Spec{x.Spec}
			}
			for _, spec := range specs {
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
		Long: `Squadrn runs coding agents (today: Claude Code) as one of several accounts on this machine.

Accounts are provider/name: claude/main is ~/.claude, claude/personal is ~/.claude-personal.`,
		Example: fmt.Sprintf(`  %[1]s                                  pick an account interactively
  %[1]s personal                         run as claude/personal (also: -a personal, claude/personal)
  %[1]s personal -p "hi"                 agent flags go to the agent CLI
  %[1]s personal -- "fix the bug"        a prompt goes after --
  %[1]s account add work                 create claude/work, then: %[1]s account login work
  %[1]s grid ~/repo feat-a personal:fix-b  two agents side by side`, c),
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
	root.PersistentPreRunE = func(cmd *cobra.Command, _ []string) error {
		if noColor {
			a.colorOut, a.colorErr = false, false
		}
		if acct != "" {
			if a.account != "" && a.account != acct {
				return usagef("the account is given twice (%s and -a %s)", a.account, acct)
			}
			a.account = acct
		}
		if a.account != "" && !takesAccount(cmd) {
			return usagef("%s does not take an account", commandName(cmd))
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
	a.completionForEveryName()
	return root
}

// takesAccount reports whether cmd runs as an account: starting an agent (the root command),
// wt <name> and grid. Help and completion ignore it.
func takesAccount(cmd *cobra.Command) bool {
	path := strings.Fields(cmd.CommandPath())[1:]
	if len(path) == 0 {
		return true
	}
	switch path[0] {
	case "help", cobra.ShellCompRequestCmd, cobra.ShellCompNoDescRequestCmd:
		return true
	case "wt", "grid":
		return len(path) == 1
	}
	return false
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
// lowerFirst turns a question ("Remove x") into the start of an error message ("remove x").
func lowerFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToLower(s[:1]) + s[1:]
}

func (a *app) confirm(yes bool, format string, args ...any) error {
	if yes {
		return nil
	}
	if !a.interactive {
		return usagef("%s; re-run with --yes to confirm", lowerFirst(fmt.Sprintf(format, args...)))
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
	if !a.ttyErr {
		return func() {}
	}
	return ui.Spin(a.stderr, msg)
}

func (a *app) versionCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "version",
		Short: "Print the squadrn version",
		Args:  noArgs,
		RunE: func(*cobra.Command, []string) error {
			if asJSON {
				return a.printJSON(map[string]string{"version": Version})
			}
			a.out("squadrn %s\n", Version)
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
	d, ok := provider.DriverOf(spec.ID)
	if !ok || d.New == nil {
		return nil, fmt.Errorf("provider %q is not supported yet", spec.ID)
	}
	return d.New(a.sessionEnv())
}

// sessionEnv points core.hooksPath at the git-level guard for sessions squadrn starts. An invalid
// config keeps it on: the guard fails closed (as the hooks do).
func (a *app) sessionEnv() []string {
	if a.cfgErr == nil && !a.cfg.GitHooksEnabled() {
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
	return a.session(ag, acct, args, "")
}

func (a *app) resolve(input string) (account.Account, error) {
	if input == "" {
		input = account.Main
	}
	return a.store.Get(input)
}

// banner names the account a session runs as and warns when it is known to be logged out.
func (a *app) banner(acct account.Account) {
	if !a.ttyErr {
		return
	}
	a.errf("%s: account %s (%s)\n", a.cmd, a.paint(a.colorErr, "1", acct.Label()), a.tilde(acct.Dir))
	if s, ok := a.cache.Cached(acct.Label()); ok && !s.LoggedIn {
		a.warnf("%s is not logged in; run: %s account login %s", acct.Label(), a.cmd, acct.Label())
	}
}

// session starts an interactive agent session as acct after naming it: shared-file copies are
// refreshed first (Windows without links) and executables left over from earlier updates are
// cleaned up. wt names the worktree of `wt <name>`, recorded as in use although the agent CLI may
// only create it once it runs.
func (a *app) session(ag provider.Agent, acct account.Account, args []string, wt string) error {
	a.banner(acct)
	r := a.store.RefreshShared(acct)
	for _, c := range r.Conflicts {
		a.warnf("%s was edited in %s, so it no longer follows the main account; to link it again (keeps a backup): %s", a.tilde(c), acct.Label(), a.relinkHint(acct))
	}
	for _, c := range r.Replaced {
		a.warnf("%s was replaced in %s, so it no longer follows the main account; to link it again (keeps a backup): %s", a.tilde(c), acct.Label(), a.relinkHint(acct))
	}
	if a.cfgErr != nil && !a.cfgWarned {
		a.cfgWarned = true
		a.warnf("%v; the git push guard stays on, but fix the config (%s doctor)", a.cfgErr, a.cmd)
	}
	a.layout.CleanOld(append([]string{a.cmd}, a.cfg.Aliases...))
	defer a.markInUse(acct, wt)()
	return ag.Exec(acct.ConfigDir(), args)
}

// relinkHint is the command that links an account's own copies of shared files to main again.
func (a *app) relinkHint(acct account.Account) string {
	return a.cmd + " account add " + acct.Label() + " --relink"
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

// byProvider groups the indexes of accts by provider, in order of first appearance.
func byProvider(accts []account.Account) [][]int {
	var groups [][]int
	pos := map[string]int{}
	for i, acct := range accts {
		g, ok := pos[acct.Provider]
		if !ok {
			g = len(groups)
			pos[acct.Provider] = g
			groups = append(groups, nil)
		}
		groups[g] = append(groups[g], i)
	}
	return groups
}

// pick returns the accounts at idx.
func pick(accts []account.Account, idx []int) []account.Account {
	out := make([]account.Account, len(idx))
	for j, i := range idx {
		out[j] = accts[i]
	}
	return out
}

// statuses queries login status for accts, grouped per provider. refresh bypasses the cache.
func (a *app) statuses(accts []account.Account, refresh bool) []provider.AuthResult {
	results := make([]provider.AuthResult, len(accts))
	for _, idx := range byProvider(accts) {
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
	if !a.ttyErr {
		// The picker draws on stderr with cursor sequences; without them, list and explain.
		return usagef("the account picker needs a terminal; name an account: %s <account>\n  accounts: %s", a.cmd, strings.Join(a.accountNames(), ", "))
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
			return a.session(ag, acct, ag.LoginArgs(), "")
		} else if !errors.Is(err, errAborted) {
			return err
		}
	}
	return a.session(ag, acct, nil, "")
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
	return a.session(ag, acct, ag.LoginArgs(), "")
}

// marketplaceDir locates the local squadrn plugin marketplace: $SQUADRN_MARKETPLACE, else the
// installed release dir. "" means no plugin is available to install.
func (a *app) marketplaceDir() string {
	if dir := os.Getenv("SQUADRN_MARKETPLACE"); dir != "" {
		if _, err := os.Stat(filepath.Join(dir, ".claude-plugin", "marketplace.json")); err == nil {
			return dir
		}
	}
	return a.layout.Marketplace()
}

func (a *app) provisioner(ag provider.Agent) account.Provisioner {
	return account.Provisioner{Store: a.store, Agent: ag, SharedMCP: a.cfg.SharedMCP, Marketplace: a.marketplaceDir()}
}

func (a *app) provision(acct account.Account, ag provider.Agent) error {
	return a.provisionWith(acct, ag, false)
}

// provisionWith creates or repairs acct; relink moves its own copies of shared files aside.
func (a *app) provisionWith(acct account.Account, ag provider.Agent, relink bool) error {
	if a.cfgErr != nil {
		return a.cfgErr
	}
	p := a.provisioner(ag)
	p.Relink = relink
	rep, err := p.Provision(context.Background(), acct)
	a.out("account %s → %s\n", acct.Label(), a.tilde(acct.Dir))
	for _, dst := range slices.Sorted(maps.Keys(rep.Backups)) {
		a.out("  moved %s aside to %s\n", a.tilde(dst), a.tilde(rep.Backups[dst]))
	}
	for _, l := range rep.Linked {
		a.out("  linked %s\n", a.tilde(l))
	}
	for _, l := range rep.Unlinked {
		a.out("  removed stale link %s\n", a.tilde(l))
	}
	for _, c := range rep.Conflicts {
		a.out("  kept %s (exists and is not a link to main; to link it: %s)\n", a.tilde(c), a.relinkHint(acct))
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
	switch {
	case rep.PluginInstalled && acct.Spec.Plugin == "":
		a.out("  push guard hook installed and trusted (%s)\n", acct.Spec.Binary)
	case rep.PluginInstalled:
		a.out("  plugin installed %s\n", acct.Spec.Plugin)
	}
	return err
}

// commandName is the command path without the program name, which error messages already carry.
func commandName(cmd *cobra.Command) string {
	return strings.TrimPrefix(cmd.CommandPath(), cmd.Root().Name()+" ")
}

func noArgs(cmd *cobra.Command, args []string) error {
	if len(args) > 0 {
		return usagef("%s takes no arguments", commandName(cmd))
	}
	return nil
}

func optionalArg(cmd *cobra.Command, args []string) error {
	if len(args) > 1 {
		return usagef("usage: %s", cmd.UseLine())
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
