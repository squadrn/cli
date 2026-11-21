package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/squadrn/squadrn-cli/internal/account"
	"github.com/squadrn/squadrn-cli/internal/config"
	"github.com/squadrn/squadrn-cli/internal/fsx"
	"github.com/squadrn/squadrn-cli/internal/provider"
)

// addOptions are account add's flags; nil pointers mean "ask at a terminal, else no".
type addOptions struct {
	profile string
	with    []string
	usage   *bool
	login   *bool
	relink  bool
	yes     bool
	dryRun  bool
	json    bool
	// provider is -p; adopt takes over an unmanaged dir; makeDefault sets default_account.
	provider    string
	adopt       bool
	makeDefault bool
	// repair: account repair, which brings an existing account up to date; add refuses one.
	repair bool
}

func (a *app) accountAddCmd() *cobra.Command {
	var o addOptions
	cmd := &cobra.Command{
		Use:   "add [<provider>/]<name>",
		Short: "Create an account (settings, shared files, push guard, usage, login)",
		Example: fmt.Sprintf(`  %[1]s account add work                     asks for the profile, usage tracking and login
  %[1]s account add work -p codex --profile strict --usage --login
  %[1]s account add work --with privacy,no-attribution
  %[1]s account add work --default           a bare %[1]s runs it
  %[1]s account add tool --adopt             take over an existing ~/.claude-tool
  %[1]s account add work --dry-run           show what would be done, change nothing`, a.cmd),
		Args:              oneArg,
		ValidArgsFunction: cobra.NoFileCompletions,
	}
	finish := addFlags(cmd, &o, false)
	f := cmd.Flags()
	f.StringVarP(&o.provider, "provider", "p", "", "agent CLI: "+strings.Join(providerIDs(), ", ")+" (or provider/ in the name)")
	f.BoolVar(&o.makeDefault, "default", false, "make it the default account (a bare "+a.cmd+" runs it)")
	f.BoolVar(&o.adopt, "adopt", false, "take over an existing dir named like the account that "+config.Slug+" did not make")
	_ = cmd.RegisterFlagCompletionFunc("provider", cobra.FixedCompletions(providerIDs(), cobra.ShellCompDirectiveNoFileComp))
	cmd.RunE = func(_ *cobra.Command, args []string) error {
		if err := finish(); err != nil {
			return err
		}
		ref, err := withProvider(args[0], o.provider)
		if err != nil {
			return err
		}
		return a.jsonErrors(o.json, a.accountAdd(ref, o))
	}
	return cmd
}

func (a *app) accountRepairCmd() *cobra.Command {
	var o addOptions
	var all bool
	cmd := &cobra.Command{
		Use:   "repair [<account> | --all]",
		Short: "Bring existing accounts up to date (shared files, settings, push guard)",
		Example: examples(a.cmd,
			"account repair work", "asks about usage tracking and the login when they are off",
			"account repair --all", "every account, without asking",
			"account repair work --profile strict", "switch the settings profile",
			"account repair work --relink", "link files the account has its own copy of (keeps a backup)"),
		Args:              optionalArg,
		ValidArgsFunction: a.completeAccounts,
	}
	finish := addFlags(cmd, &o, true)
	cmd.Flags().BoolVar(&all, "all", false, "repair every account, without asking")
	cmd.RunE = func(_ *cobra.Command, args []string) error {
		if err := finish(); err != nil {
			return err
		}
		o.repair = true
		switch {
		case all && len(args) > 0:
			return usagef("name an account or --all, not both")
		case all:
			return a.jsonErrors(o.json, a.repairAll(o))
		case len(args) == 0:
			return usagef("name an account to repair, or --all: %s account repair <account>", a.cmd)
		}
		return a.jsonErrors(o.json, a.accountRepair(args[0], o))
	}
	return cmd
}

// addFlags declares the flags add and repair share; the returned func checks them once parsed.
func addFlags(cmd *cobra.Command, o *addOptions, repair bool) func() error {
	var track, noTrack, login, noLogin bool
	profiles := make([]string, len(provider.ProfileNames))
	for i, p := range provider.ProfileNames {
		profiles[i] = p.Name
	}
	options := make([]string, len(provider.Options))
	for i, opt := range provider.Options {
		options[i] = opt.Name
	}
	f := cmd.Flags()
	profileHelp := "settings profile: " + strings.Join(profiles, ", ") + " (default: default_profile, else " + provider.DefaultProfile + ")"
	if repair {
		profileHelp = "switch the settings profile: " + strings.Join(profiles, ", ") + " (default: keep the account's)"
	}
	f.StringVar(&o.profile, "profile", "", profileHelp)
	f.StringSliceVar(&o.with, "with", nil, "opt-in settings, repeatable or comma separated: "+strings.Join(options, ", "))
	f.BoolVar(&track, "usage", false, "track usage limits in the status line without asking")
	f.BoolVar(&noTrack, "no-usage", false, "do not track usage limits and do not ask")
	f.BoolVar(&login, "login", false, "log in right away without asking")
	f.BoolVar(&noLogin, "no-login", false, "do not log in and do not ask")
	if repair {
		f.BoolVar(&o.relink, "relink", false, "move the account's own copies of shared files aside (.bak-<time>) and link them to main (asks)")
	}
	f.BoolVar(&o.dryRun, "dry-run", false, "print the plan and change nothing")
	f.BoolVar(&o.json, "json", false, "print the result as JSON (implies --yes)")
	f.BoolVarP(&o.yes, "yes", "y", false, "do not ask; use the defaults and the flags given")
	_ = cmd.RegisterFlagCompletionFunc("profile", cobra.FixedCompletions(profiles, cobra.ShellCompDirectiveNoFileComp))
	_ = cmd.RegisterFlagCompletionFunc("with", cobra.FixedCompletions(options, cobra.ShellCompDirectiveNoFileComp))
	return func() error {
		if track && noTrack {
			return usagef("--usage and --no-usage cannot be combined")
		}
		if login && noLogin {
			return usagef("--login and --no-login cannot be combined")
		}
		if track || noTrack {
			o.usage = &track
		}
		if login || noLogin {
			o.login = &login
		}
		return nil
	}
}

func providerIDs() []string {
	var ids []string
	for _, s := range provider.All() {
		ids = append(ids, s.ID)
	}
	return ids
}

// withProvider qualifies ref with -p: they must agree when ref names a provider too.
func withProvider(ref, id string) (string, error) {
	if id == "" {
		return ref, nil
	}
	if _, ok := provider.Get(id); !ok {
		return "", usagef("unknown provider %q (use %s)", id, strings.Join(providerIDs(), ", "))
	}
	if pid, _, ok := strings.Cut(ref, "/"); ok {
		if pid != id {
			return "", usagef("%s names provider %s, -p says %s", ref, pid, id)
		}
		return ref, nil
	}
	return id + "/" + ref, nil
}

// addStep is one applied step, for the failure report.
type addStep struct {
	name    string
	created bool // the account dir exists in place
}

// addPlan is everything account add will do, read before anything is written (planAdd). The
// questions of the flag path and the wizard fill in what is left to decide.
type addPlan struct {
	acct account.Account
	ag   provider.Agent
	// main: the provider's default account, which the CLI creates on its own; only the push
	// guard, usage tracking and the login apply.
	main  bool
	isNew bool
	// adopt takes over a dir another tool made (add --adopt).
	adopt bool
	// makeDefault sets default_account to the account (add --default).
	makeDefault bool
	version     string
	rec         account.Managed
	profile     string
	options     []string
	// shared names what main shares with a new account.
	shared []string
	relink bool
	// trackUsage and noUsage turn usage tracking on or off; neither leaves it as it is.
	trackUsage, noUsage bool
	login               bool
	// status is the login before the add, then after the login step.
	status provider.AuthStatus
	start  time.Time
	// waited is the time spent on the user (questions, the login), left out of "done in".
	waited time.Duration
}

// addResult is what account add did; --json prints it.
type addResult struct {
	Account  string   `json:"account"`
	Provider string   `json:"provider"`
	Dir      string   `json:"dir"`
	Created  bool     `json:"created"`
	Adopted  bool     `json:"adopted"`
	Profile  string   `json:"profile"`
	Options  []string `json:"options"`
	Guard    string   `json:"guard"`
	Usage    bool     `json:"usage"`
	LoggedIn bool     `json:"loggedIn"`
	Default  bool     `json:"default"`
	Start    string   `json:"start"`
	DryRun   bool     `json:"dryRun,omitempty"`
	// changes counts the steps that changed something; guard is the guard line once known.
	changes int
	guard   string
}

// accountAdd creates (or, for account repair, repairs) an account: plan (read only), ask what is left (at a terminal),
// apply (a new account is built in a temp dir and renamed into place), then log in and summarise
// (ADR 0011, ADR 0014).
func (a *app) accountAdd(input string, o addOptions) error {
	start := time.Now()
	if a.cfgErr != nil {
		return a.cfgErr
	}
	if o.json {
		o.yes = true
	}
	p, err := a.planAdd(input, o)
	if err != nil {
		return err
	}
	p.start = start
	r := a.addReporter(o.json)
	if o.dryRun {
		return a.reportPlan(p, o.json)
	}
	r.header(p)
	if err := a.askAdd(p, o, r); err != nil {
		return err
	}
	res, err := a.applyAdd(p, r)
	if err != nil {
		return err
	}
	if p.login {
		a.addLogin(p, r, &res)
	}
	return r.done(p, res)
}

// planAdd reads what account add would do to input with o; it writes nothing. The CLI's version
// and the login state are read in parallel with the account.
func (a *app) planAdd(input string, o addOptions) (*addPlan, error) {
	if main, ok := a.store.MainOf(input); ok {
		return a.planAddMain(main, o)
	}
	acct, err := a.store.Define(input)
	if err != nil {
		return nil, err
	}
	if o.profile != "" && provider.ProfileSummary(o.profile) == "" {
		return nil, usagef("unknown profile %q (use %s)", o.profile, profileList())
	}
	for _, w := range o.with {
		if !provider.ValidOption(w) {
			return nil, usagef("unknown --with %q (use %s)", w, optionList())
		}
	}
	managed := a.store.Managed(acct)
	unmanaged := !managed && fsx.IsDir(acct.Dir)
	switch {
	case unmanaged && !o.adopt:
		return nil, fixError{
			msg: fmt.Sprintf("%s exists but is not a %s account (another tool may use it)", a.tilde(acct.Dir), config.Slug),
			how: "to manage it, run", fix: a.cmd + " account add " + input + " --adopt",
		}
	case o.repair && !managed:
		return nil, account.NotFoundError{Ref: acct.Ref}
	case !o.repair && managed:
		return nil, fixError{msg: acct.Label() + " already exists", how: "repair", fix: a.cmd + " account repair " + acct.Label(), paren: true}
	case o.adopt && !unmanaged:
		return nil, usagef("nothing to adopt: %s does not exist (to create the account, leave out --adopt)", a.tilde(acct.Dir))
	}
	ag, err := a.agent(acct.Spec)
	if err != nil {
		return nil, err
	}
	p := &addPlan{acct: acct, ag: ag, isNew: !managed && !unmanaged, adopt: unmanaged, makeDefault: o.makeDefault}
	version := make(chan string, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), authTimeout)
		defer cancel()
		out, _ := ag.Output(ctx, "", "--version")
		version <- provider.ParseVersion(out)
	}()
	login := make(chan provider.AuthStatus, 1)
	if p.isNew {
		login <- provider.AuthStatus{}
	} else {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), authTimeout)
			defer cancel()
			s, _ := ag.Auth(ctx, acct.ConfigDir())
			login <- s
		}()
	}
	if p.rec, _, err = account.ReadManaged(acct.Dir); err != nil {
		return nil, err
	}
	p.profile, p.options = p.rec.Profile, p.rec.Options
	if p.isNew && p.profile == "" {
		p.profile = a.cfg.ProfileName()
	}
	if o.profile != "" {
		p.profile = o.profile
	}
	if o.with != nil {
		p.options = o.with
	}
	if p.isNew {
		p.shared = a.sharedNames(acct.Spec)
	}
	p.relink = o.relink && !p.isNew
	p.usageFlags(o)
	p.version = <-version
	p.status = <-login
	return p, nil
}

// planAddMain plans the setup of a provider's default account (~/.claude, ~/.codex), which the
// CLI creates on its own: squadrn's push guard, usage tracking and the login. Its settings stay
// the user's own, so profiles and shared links do not apply.
func (a *app) planAddMain(acct account.Account, o addOptions) (*addPlan, error) {
	if o.profile != "" || o.with != nil || o.relink {
		return nil, usagef("%s is the default account (%s): --profile, --with and --relink apply to other accounts", acct.Label(), a.tilde(acct.Dir))
	}
	ag, err := a.agent(acct.Spec)
	if err != nil {
		return nil, err
	}
	p := &addPlan{acct: acct, ag: ag, main: true, makeDefault: o.makeDefault}
	ctx, cancel := context.WithTimeout(context.Background(), authTimeout)
	out, _ := ag.Output(ctx, "", "--version")
	p.version = provider.ParseVersion(out)
	p.status, _ = ag.Auth(ctx, acct.ConfigDir())
	cancel()
	p.usageFlags(o)
	return p, nil
}

func (p *addPlan) usageFlags(o addOptions) {
	p.trackUsage = o.usage != nil && *o.usage
	p.noUsage = o.usage != nil && !*o.usage
	p.login = o.login != nil && *o.login
}

// askAdd asks the flag path's questions at a terminal, as plan lines: what the flags leave open.
func (a *app) askAdd(p *addPlan, o addOptions, r addReporter) error {
	ask := a.interactive && !o.yes
	in := bufio.NewReader(a.stdin)
	asked := time.Now()
	acct := p.acct
	var err error
	if !p.main {
		if p.isNew && o.profile == "" && ask && settingsFormat(acct.Spec) != nil {
			if p.profile, err = a.askProfile(in, acct.Spec, p.profile); err != nil {
				return err
			}
		} else if p.profile != "" {
			r.planLine("profile", profileDesc(acct.Spec, p.profile, p.options))
		}
		if len(p.shared) > 0 {
			r.planLine("shared", fmt.Sprintf("%s from %s/%s", strings.Join(p.shared, ", "), acct.Provider, account.Main))
		}
		if p.relink {
			if p.relink, err = a.confirmRelink(in, acct, p.ag, ask, o.yes); err != nil {
				return err
			}
		}
	}
	if statusLineOf(acct.Spec) != nil && o.usage == nil && ask && (p.isNew || !a.usageTracking(acct)) {
		if p.trackUsage, err = a.askYes(in, "usage limits", "track for "+acct.Label()+"?"); err != nil {
			return err
		}
		p.noUsage = !p.trackUsage
	}
	if !p.status.LoggedIn && o.login == nil && ask {
		if p.login, err = a.askYes(in, "log in now?", ""); err != nil {
			return err
		}
	}
	if ask {
		p.waited = time.Since(asked)
	}
	return nil
}

// applyAdd carries out p: the only writer of account add. A new account is built in a temp dir
// and renamed into place, so a failure leaves nothing half made.
func (a *app) applyAdd(p *addPlan, r addReporter) (addResult, error) {
	res := addResult{Account: p.acct.Label(), Provider: p.acct.Provider, Dir: p.acct.Dir, Created: p.isNew, Adopted: p.adopt, Profile: p.profile, Options: p.options}
	note := func(format string, args ...any) {
		res.changes++
		r.note(fmt.Sprintf(format, args...))
	}
	warn := func(format string, args ...any) { r.warn(fmt.Sprintf(format, args...)) }
	if p.main {
		if err := a.applyAddMain(p, &res, note, warn); err != nil {
			return res, err
		}
		a.applyDefault(p, note, warn)
		return res, nil
	}
	acct, ag := p.acct, p.ag
	step := addStep{created: !p.isNew}
	fail := func(err error) (addResult, error) { return res, r.failed(acct, step, err) }
	work := acct
	if p.isNew {
		step.name = "create " + a.tilde(acct.Dir)
		tmp, err := os.MkdirTemp(filepath.Dir(acct.Dir), filepath.Base(acct.Dir)+".tmp-")
		if err != nil {
			return fail(err)
		}
		defer func() {
			if fsx.IsDir(tmp) && strings.HasPrefix(filepath.Base(tmp), filepath.Base(acct.Dir)+".tmp-") {
				_ = os.RemoveAll(tmp)
			}
		}()
		work.Dir = tmp
	}
	if p.adopt {
		step.name = "adopt " + a.tilde(acct.Dir)
		if _, err := a.store.Adopt(acct.Label()); err != nil {
			return fail(err)
		}
		note("adopted %s as %s", a.tilde(acct.Dir), acct.Label())
	}
	step.name = "set up shared files and the push guard"
	prov := a.provisioner(ag)
	prov.Relink = p.relink
	rep, err := prov.Provision(context.Background(), work)
	if err != nil {
		return fail(err)
	}
	step.name = "build settings"
	if p.profile != "" && (p.profile != p.rec.Profile || !slices.Equal(p.options, p.rec.Options)) {
		if err := account.SetManagedProfile(work, p.profile, p.options); err != nil {
			return fail(err)
		}
	}
	if d, _ := provider.DriverOf(acct.Spec.ID); p.isNew && d.Seed != nil {
		if err := d.Seed.Seed(a.store.Home, work.StateDir()); err != nil {
			return fail(err)
		}
	}
	srep, err := a.syncSettingsAs(work, acct)
	if err != nil {
		return fail(err)
	}
	if p.isNew {
		step.name = "move the new account into place"
		if err := os.Rename(work.Dir, acct.Dir); err != nil {
			return fail(err)
		}
		step.created = true
		if rl, ok := ag.(provider.Relocator); ok {
			if err := rl.Relocate(work.Dir, acct.Dir); err != nil {
				return fail(err)
			}
		}
		note("created %s (private)", a.tilde(acct.Dir))
	}
	a.cache.Forget(acct.Label())
	switch {
	case p.isNew || srep.Created || srep.Backup != "":
		what := "settings from " + acct.Provider + "/" + account.Main
		if mainAcct, _ := a.store.MainOf(acct.Provider + "/" + account.Main); !fsx.IsFile(account.SettingsPath(mainAcct)) {
			// No main account (or no settings in it) to start from.
			what = "settings created"
			if p.profile != "" {
				what = "settings: " + profileName(p.profile, p.options)
			}
		} else if p.profile != "" {
			what += " + " + profileName(p.profile, p.options)
		}
		if srep.Backup != "" {
			what += " (the link to main's is kept as " + a.tilde(srep.Backup) + ")"
		}
		note("%s", what)
	case len(srep.Written)+len(srep.Removed) > 0:
		note("settings updated: %s", strings.Join(append(srep.Written, srep.Removed...), ", "))
	}
	if len(srep.Kept) > 0 {
		warn("settings: kept your change to %s (it no longer follows %s/%s)", strings.Join(srep.Kept, ", "), acct.Provider, account.Main)
	}
	a.reportProvision(acct, rep, p.isNew, note, warn)
	if p.isNew || rep.PluginInstalled {
		if res.guard = a.guardLine(acct, ag, rep.PluginInstalled); res.guard != "" {
			note("push guard: %s", res.guard)
		} else if acct.Spec.Caps.Plugins {
			warn("push guard: the %s plugin is not available here; re-run install.sh, then: %s account repair %s", config.Slug, a.cmd, acct.Label())
		}
	}
	// A new account copies main's status line, so it may already run the recorder.
	switch tracking := a.usageTracking(acct); {
	case p.trackUsage && !tracking:
		a.turnUsageOn(acct, note, warn)
	case p.noUsage && tracking:
		a.turnUsageOff(acct, note, warn)
	case p.isNew && tracking:
		note("usage tracking on")
	}
	a.applyDefault(p, note, warn)
	return res, nil
}

// applyDefault makes p's account the default account (add --default).
func (a *app) applyDefault(p *addPlan, note, warn func(string, ...any)) {
	if !p.makeDefault || (a.cfgErr == nil && a.cfg.DefaultAccount == p.acct.Label()) {
		return
	}
	if err := a.saveDefaultAccount(p.acct.Label()); err != nil {
		warn("default account: %v; run: %s account default %s", err, a.cmd, p.acct.Label())
		return
	}
	note("default account: a bare %s runs %s", a.cmd, p.acct.Label())
}

// saveDefaultAccount writes default_account ("" clears it) to the config file.
func (a *app) saveDefaultAccount(label string) error {
	if a.cfgErr != nil {
		return a.cfgErr
	}
	path, err := config.Path()
	if err != nil {
		return err
	}
	cfg := a.cfg
	cfg.DefaultAccount = label
	if err := config.Save(path, cfg); err != nil {
		return err
	}
	a.cfg = cfg
	return nil
}

// accountRepair brings an existing account up to date: account add's plan and apply for an
// account that exists.
func (a *app) accountRepair(input string, o addOptions) error {
	if a.cfgErr != nil {
		return a.cfgErr
	}
	acct, err := a.store.Get(input)
	if err != nil {
		return err
	}
	return a.accountAdd(acct.Label(), o)
}

// repairAll repairs every account without asking; one that fails does not stop the others.
func (a *app) repairAll(o addOptions) error {
	if a.cfgErr != nil {
		return a.cfgErr
	}
	accts, err := a.store.List()
	if err != nil {
		return err
	}
	o.yes = true
	failed := 0
	for _, acct := range accts {
		err := a.accountAdd(acct.Label(), o)
		if err == nil {
			continue
		}
		failed++
		if err = a.jsonErrors(o.json, err); !silent(err) {
			a.errf("%s: %s: %v\n", a.cmd, acct.Label(), err)
		}
	}
	if failed > 0 {
		return errQuiet
	}
	return nil
}

// applyAddMain sets up a provider's default account: the push guard and usage tracking.
func (a *app) applyAddMain(p *addPlan, res *addResult, note, warn func(string, ...any)) error {
	acct := p.acct
	if err := os.MkdirAll(acct.Dir, 0o700); err != nil {
		return err
	}
	installed := false
	if gi, ok := p.ag.(provider.GuardInstaller); ok && acct.Spec.Caps.Plugins {
		if mp := a.marketplaceDir(); mp == "" {
			warn("push guard: the %s plugin is not available here; re-run install.sh, then: %s account repair %s", config.Slug, a.cmd, acct.Label())
		} else {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			var err error
			installed, err = gi.InstallPlugin(ctx, acct.ConfigDir(), mp)
			cancel()
			if err != nil {
				return fmt.Errorf("push guard for %s: %w", acct.Label(), err)
			}
		}
	}
	if installed {
		res.guard = a.guardLine(acct, p.ag, true)
		note("push guard: %s", res.guard)
	}
	switch tracking := a.usageTracking(acct); {
	case p.trackUsage && !tracking:
		a.turnUsageOn(acct, note, warn)
	case p.noUsage && tracking:
		a.turnUsageOff(acct, note, warn)
	}
	return nil
}

func (a *app) turnUsageOn(acct account.Account, note, warn func(string, ...any)) {
	if err := a.enableUsage(acct); err != nil {
		warn("usage tracking: %v; run: %s usage setup %s", err, a.cmd, acct.Label())
	} else {
		note("usage tracking on")
	}
}

func (a *app) turnUsageOff(acct account.Account, note, warn func(string, ...any)) {
	if err := a.disableUsage(acct); err != nil {
		warn("usage tracking: %v; run: %s usage setup --undo %s", err, a.cmd, acct.Label())
	} else {
		note("usage tracking off")
	}
}

// addLogin runs the CLI's login for p's account and reads the login state again. A login that
// does not finish leaves a valid account and says how to log in later.
func (a *app) addLogin(p *addPlan, r addReporter, res *addResult) {
	acct := p.acct
	r.loginStart()
	if h, ok := p.ag.(provider.LoginHinter); ok {
		r.info(h.LoginHint())
	}
	loginAt := time.Now()
	err := p.ag.Run(acct.ConfigDir(), p.ag.LoginArgs())
	p.waited += time.Since(loginAt)
	a.cache.Forget(acct.Label())
	if err != nil {
		r.warn(fmt.Sprintf("login did not finish (%v); run: %s account login %s", err, a.cmd, acct.Label()))
	}
	ctx, cancel := context.WithTimeout(context.Background(), authTimeout)
	p.status, _ = p.ag.Auth(ctx, acct.ConfigDir())
	cancel()
	if p.status.LoggedIn {
		res.changes++
		r.note("logged in as " + loginName(p.status))
	}
}

// result completes res for p once everything ran: what is true of the account now.
func (a *app) result(p *addPlan, res addResult) addResult {
	if res.Options == nil {
		res.Options = []string{}
	}
	guard := res.guard
	if guard == "" {
		guard = a.guardLine(p.acct, p.ag, false)
	}
	res.Guard = "missing"
	if guard != "" {
		res.Guard = "installed"
	}
	res.Usage = a.usageTracking(p.acct)
	res.LoggedIn = p.status.LoggedIn
	res.Start = a.startCommand(p.acct)
	res.Default = a.cfgErr == nil && a.cfg.DefaultAccount == p.acct.Label()
	return res
}

// planLines describe p for --dry-run and the wizard's review: label and text.
func (a *app) planLines(p *addPlan) [][2]string {
	acct := p.acct
	var lines [][2]string
	if !p.main {
		profile := "none (keeps its own settings)"
		if p.profile != "" {
			profile = p.profile
			if len(p.options) > 0 {
				profile += " + " + strings.Join(p.options, ", ")
			}
			profile += "   " + acct.Spec.ProfileSummary(p.profile)
		}
		lines = append(lines, [2]string{"profile", profile})
		if len(p.shared) > 0 {
			lines = append(lines, [2]string{"shared", strings.Join(p.shared, ", ") + " ← " + acct.Provider + "/" + account.Main})
		}
		main, _ := a.store.MainOf(acct.Provider + "/" + account.Main)
		settings := "kept, follows " + acct.Provider + "/" + account.Main
		switch {
		case p.isNew && fsx.IsFile(account.SettingsPath(main)) && p.profile != "":
			settings = "copy of " + acct.Provider + "/" + account.Main + " + " + p.profile + " (follows main)"
		case p.isNew && fsx.IsFile(account.SettingsPath(main)):
			settings = "copy of " + acct.Provider + "/" + account.Main + " (follows main)"
		case p.isNew:
			settings = "created"
			if p.profile != "" {
				settings += " from the " + p.profile + " profile"
			}
		}
		lines = append(lines, [2]string{"settings", settings})
	}
	lines = append(lines, [2]string{"guard", a.plannedGuard(acct)})
	if statusLineOf(acct.Spec) != nil {
		usage := "unchanged"
		switch {
		case p.trackUsage:
			usage = "on"
		case p.noUsage:
			usage = "off"
		}
		lines = append(lines, [2]string{"usage", usage})
	}
	login := "no (--login to log in)"
	switch {
	case p.status.LoggedIn:
		login = "already logged in"
	case p.login:
		login = "yes"
	}
	lines = append(lines, [2]string{"login", login})
	if p.makeDefault {
		lines = append(lines, [2]string{"default", "yes: a bare " + a.cmd + " runs it"})
	}
	return lines
}

// plannedGuard names the push-guard layers an account gets, without asking its CLI.
func (a *app) plannedGuard(acct account.Account) string {
	var layers []string
	if acct.Spec.Caps.Plugins {
		if a.marketplaceDir() == "" {
			layers = append(layers, "no pre-tool hook (the plugin is not available here)")
		} else {
			layers = append(layers, "pre-tool hook")
		}
	}
	if (a.cfgErr != nil || a.cfg.GitHooksEnabled()) && !acct.Spec.NoGitHooks {
		layers = append(layers, "git pre-push")
	}
	if len(layers) == 0 {
		return "none"
	}
	return strings.Join(layers, " + ")
}

// reportPlan prints p for --dry-run: the plan lines, or with --json the result it would have.
func (a *app) reportPlan(p *addPlan, asJSON bool) error {
	if asJSON {
		res := addResult{
			Account: p.acct.Label(), Provider: p.acct.Provider, Dir: p.acct.Dir, Created: p.isNew,
			Profile: p.profile, Options: p.options, Usage: p.trackUsage || (!p.noUsage && a.usageTracking(p.acct)),
			LoggedIn: p.status.LoggedIn, Start: a.startCommand(p.acct), DryRun: true, Guard: "installed",
			Adopted: p.adopt, Default: p.makeDefault || (a.cfgErr == nil && a.cfg.DefaultAccount == p.acct.Label()),
		}
		if res.Options == nil {
			res.Options = []string{}
		}
		if strings.HasPrefix(a.plannedGuard(p.acct), "no ") || a.plannedGuard(p.acct) == "none" {
			res.Guard = "missing"
		}
		return a.printJSONLine(res)
	}
	state := "exists"
	switch {
	case p.adopt:
		state = "exists, adopt it"
	case p.main:
		state = "the default account"
	case p.isNew:
		state = "new"
	}
	a.out("plan: %s → %s (%s)\n", p.acct.Label(), a.tilde(p.acct.Dir), state)
	for _, l := range a.planLines(p) {
		a.out("  %-9s %s\n", l[0], l[1])
	}
	a.out("nothing was changed\n")
	return nil
}

// syncSettingsAs syncs work (a new account's temp dir) as the account it becomes.
func (a *app) syncSettingsAs(work, acct account.Account) (account.SettingsReport, error) {
	f := settingsFormat(work.Spec)
	layers, err := a.profileLayers(work, acct)
	if err != nil {
		return account.SettingsReport{}, err
	}
	rep, err := a.store.SyncSettings(work, f, layers, a.now())
	if err != nil {
		return rep, err
	}
	return rep, a.writeProfileFiles(work, acct)
}

// writeProfileFiles writes the files the profile recorded in work's dir keeps next to the
// settings (Gemini: its policy file) into work's state dir, when they changed.
func (a *app) writeProfileFiles(work, acct account.Account) error {
	d, _ := provider.DriverOf(acct.Spec.ID)
	pf, ok := d.Profiles.(provider.ProfileFiler)
	if !ok || work.IsMain() {
		return nil
	}
	m, _, err := account.ReadManaged(work.Dir)
	if err != nil || m.Profile == "" {
		return err
	}
	files, err := pf.ProfileFiles(m.Profile, m.Options, a.profileContext(acct))
	if err != nil {
		return err
	}
	for _, name := range slices.Sorted(maps.Keys(files)) {
		p := filepath.Join(work.StateDir(), name)
		if old, err := os.ReadFile(p); err == nil && string(old) == string(files[name]) {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			return err
		}
		if err := fsx.WriteBytes(p, files[name], 0o600); err != nil {
			return err
		}
	}
	return nil
}

// profileLayers is the settings layer of the profile recorded in work's dir, for account acct.
func (a *app) profileLayers(work, acct account.Account) ([]account.Layer, error) {
	d, _ := provider.DriverOf(acct.Spec.ID)
	m, _, err := account.ReadManaged(work.Dir)
	if err != nil || m.Profile == "" || d.Profiles == nil {
		return nil, err
	}
	doc, err := d.Profiles.Profile(m.Profile, m.Options, a.profileContext(acct))
	if err != nil {
		return nil, err
	}
	return []account.Layer{{Source: account.SourceProfile, Doc: doc}}, nil
}

// profileContext describes this machine for acct's profile: every other account's credential
// files and squadrn's own dirs, as ~/ paths with / separators.
func (a *app) profileContext(acct account.Account) provider.ProfileContext {
	ctx := provider.ProfileContext{Hook: a.statusLineBinary(), Home: a.store.Home, Notify: a.cfgErr == nil && a.cfg.Notify.On()}
	accts, _ := a.store.List()
	for _, x := range accts {
		if x.Label() == acct.Label() {
			continue
		}
		for _, p := range x.CredentialFiles() {
			ctx.OtherCredentials = append(ctx.OtherCredentials, a.rulePath(p))
		}
	}
	for _, dir := range []string{a.layout.DataDir, config.DirsFor(a.store.Home, config.Slug).Config} {
		ctx.OwnDirs = append(ctx.OwnDirs, a.rulePath(dir))
	}
	return ctx
}

// rulePath is p as permission rules write it: ~/ under home, / separators.
func (a *app) rulePath(p string) string {
	if rel, err := filepath.Rel(a.store.Home, p); err == nil && !strings.HasPrefix(rel, "..") {
		return "~/" + filepath.ToSlash(rel)
	}
	return filepath.ToSlash(p)
}

func (a *app) reportProvision(acct account.Account, rep account.Report, isNew bool, note, warn func(string, ...any)) {
	for _, dst := range slices.Sorted(maps.Keys(rep.Backups)) {
		note("moved %s aside to %s", a.tilde(dst), a.tilde(rep.Backups[dst]))
	}
	if !isNew {
		for _, l := range rep.Linked {
			note("linked %s", a.tilde(l))
		}
	}
	for _, l := range rep.Unlinked {
		note("removed stale link %s", a.tilde(l))
	}
	for _, c := range rep.Conflicts {
		warn("kept %s (exists and is not a link to main; to link it: %s)", a.tilde(c), a.relinkHint(acct))
	}
	if len(rep.MCPAdded) > 0 {
		note("mcp %s from %s/%s", strings.Join(rep.MCPAdded, ", "), acct.Provider, account.Main)
	}
	for _, m := range rep.MCPSkipped {
		warn("mcp %s not copied (has env/headers; add it yourself: %s %s -- mcp add ...)", m, a.cmd, acct.Label())
	}
	for _, m := range rep.MCPMissing {
		warn("mcp %s is not configured in %s/%s", m, acct.Provider, account.Main)
	}
}

// guardLine names the push-guard layers acct has: the CLI's pre-tool hook and git's pre-push.
// A plugin installed just now is installed and enabled; otherwise the CLI is asked.
func (a *app) guardLine(acct account.Account, ag provider.Agent, justInstalled bool) string {
	var layers []string
	if justInstalled {
		layers = append(layers, "pre-tool hook")
	} else if gi, ok := ag.(provider.GuardInstaller); ok && acct.Spec.Caps.Plugins {
		ctx, cancel := context.WithTimeout(context.Background(), authTimeout)
		installed, enabled, err := gi.PluginState(ctx, acct.ConfigDir())
		cancel()
		if err == nil && installed && enabled {
			layers = append(layers, "pre-tool hook")
		}
	}
	if len(layers) == 0 {
		return ""
	}
	if (a.cfgErr != nil || a.cfg.GitHooksEnabled()) && !acct.Spec.NoGitHooks {
		layers = append(layers, "git pre-push")
	}
	return strings.Join(layers, " + ")
}

// sharedNames lists what main shares with every account, by name.
func (a *app) sharedNames(spec provider.Spec) []string {
	main := a.store.MainDir(spec)
	var out []string
	for _, item := range slices.Sorted(maps.Keys(spec.SharedPerEntry)) {
		if fsx.IsDir(filepath.Join(main, item)) {
			out = append(out, item)
		}
	}
	for _, item := range spec.Shared {
		if _, err := os.Stat(filepath.Join(main, item)); err == nil {
			out = append(out, item)
		}
	}
	return out
}

// startCommand is the shortest command that starts acct: its bare name when that is unique.
func (a *app) startCommand(acct account.Account) string {
	if got, err := a.store.Get(acct.Name); err == nil && got.Label() == acct.Label() {
		return a.cmd + " " + acct.Name
	}
	return a.cmd + " " + acct.Label()
}

// askProfile lists spec's profiles with what each one does, then asks for one; Enter takes def.
func (a *app) askProfile(in *bufio.Reader, spec provider.Spec, def string) (string, error) {
	for i, p := range provider.ProfileNames {
		label := ""
		if i == 0 {
			label = "profiles"
		}
		a.errf("%s%-14s %-9s %s\n", a.padding(), label, p.Name, spec.ProfileSummary(p.Name))
	}
	for {
		a.errf("%s%-14s %s [%s]: ", a.padding(), "profile", profileList(), def)
		line, err := in.ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return "", err
		}
		answer := strings.ToLower(strings.TrimSpace(line))
		if answer == "" {
			return def, nil
		}
		for _, p := range provider.ProfileNames {
			if p.Name == answer || (len(answer) >= 2 && strings.HasPrefix(p.Name, answer)) {
				return p.Name, nil
			}
		}
		if errors.Is(err, io.EOF) {
			return "", usagef("unknown profile %q (use %s)", answer, profileList())
		}
		a.errf("  unknown profile %q\n", answer)
	}
}

// askYes asks a [Y/n] question on one plan line; Enter is yes.
func (a *app) askYes(in *bufio.Reader, label, question string) (bool, error) {
	if question != "" {
		question += " "
	}
	a.errf("%s%-14s %s[Y/n] ", a.padding(), label, question)
	line, err := in.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return false, err
	}
	return !strings.HasPrefix(strings.ToLower(strings.TrimSpace(line)), "n"), nil
}

// padding indents plan lines and questions: two spaces, or setup's step indent.
func (a *app) padding() string {
	if a.pad == "" {
		return "  "
	}
	return a.pad
}

// askNo is askYes for a question whose default is no.
func (a *app) askNo(in *bufio.Reader, label, question string) (bool, error) {
	a.errf("%s%-14s %s [y/N] ", a.padding(), label, question)
	line, err := in.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return false, err
	}
	s := strings.ToLower(strings.TrimSpace(line))
	return s == "y" || s == "yes", nil
}

func profileList() string {
	names := make([]string, len(provider.ProfileNames))
	for i, p := range provider.ProfileNames {
		names[i] = p.Name
	}
	return strings.Join(names, " / ")
}

func optionList() string {
	names := make([]string, len(provider.Options))
	for i, o := range provider.Options {
		names[i] = o.Name
	}
	return strings.Join(names, ", ")
}

// profileDesc is "standard (secrets denied, …)" plus the options.
func profileDesc(spec provider.Spec, profile string, options []string) string {
	s := profile + " (" + spec.ProfileSummary(profile) + ")"
	if len(options) > 0 {
		s += " + " + strings.Join(options, ", ")
	}
	return s
}

func profileName(profile string, options []string) string {
	s := profile + " profile"
	if len(options) > 0 {
		s += " + " + strings.Join(options, ", ")
	}
	return s
}

// loginName is "me@example.com (Max)": the email and the plan, when known.
func loginName(s provider.AuthStatus) string {
	who := s.Email
	if who == "" {
		who = "this account"
	}
	if s.SubscriptionType != "" {
		who += " (" + strings.ToUpper(s.SubscriptionType[:1]) + s.SubscriptionType[1:] + ")"
	}
	return who
}
