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
}

func (a *app) accountAddCmd() *cobra.Command {
	var o addOptions
	var track, noTrack, login, noLogin bool
	profiles := make([]string, len(provider.ProfileNames))
	for i, p := range provider.ProfileNames {
		profiles[i] = p.Name
	}
	options := make([]string, len(provider.Options))
	for i, opt := range provider.Options {
		options[i] = opt.Name
	}
	cmd := &cobra.Command{
		Use:   "add [<provider>/]<name>",
		Short: "Create or repair an account (settings, shared files, push guard, usage, login)",
		Example: fmt.Sprintf(`  %[1]s account add work                     asks for the profile, usage tracking and login
  %[1]s account add claude/work --profile strict --usage --login
  %[1]s account add work --with privacy      also turn telemetry off
  %[1]s account add work --relink            link files the account has its own copy of (keeps a backup)`, a.cmd),
		Args: oneArg,
		RunE: func(_ *cobra.Command, args []string) error {
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
			return a.accountAdd(args[0], o)
		},
		ValidArgsFunction: cobra.NoFileCompletions,
	}
	f := cmd.Flags()
	f.StringVar(&o.profile, "profile", "", "settings profile for a new account: "+strings.Join(profiles, ", ")+" (default "+provider.DefaultProfile+")")
	f.StringSliceVar(&o.with, "with", nil, "opt-in settings, repeatable: "+strings.Join(options, ", "))
	f.BoolVar(&track, "usage", false, "track usage limits in the status line without asking")
	f.BoolVar(&noTrack, "no-usage", false, "do not track usage limits and do not ask")
	f.BoolVar(&login, "login", false, "log in right away without asking")
	f.BoolVar(&noLogin, "no-login", false, "do not log in and do not ask")
	f.BoolVar(&o.relink, "relink", false, "move the account's own copies of shared files aside (.bak-<time>) and link them to main (asks)")
	f.BoolVarP(&o.yes, "yes", "y", false, "do not ask; use the defaults and the flags given")
	_ = cmd.RegisterFlagCompletionFunc("profile", cobra.FixedCompletions(profiles, cobra.ShellCompDirectiveNoFileComp))
	_ = cmd.RegisterFlagCompletionFunc("with", cobra.FixedCompletions(options, cobra.ShellCompDirectiveNoFileComp))
	return cmd
}

// addStep is one applied step, for the failure report.
type addStep struct {
	name    string
	created bool // the account dir exists in place
}

// accountAdd creates or repairs an account in four steps: plan, ask everything up front, apply
// (a new account is built in a temp dir and renamed into place), verify and summarise (ADR 0011).
func (a *app) accountAdd(input string, o addOptions) error {
	start := time.Now()
	if a.cfgErr != nil {
		return a.cfgErr
	}
	if main, ok := a.store.MainOf(input); ok {
		return a.accountAddMain(main, o, start)
	}
	acct, err := a.store.Define(input)
	if err != nil {
		return err
	}
	if o.profile != "" && provider.ProfileSummary(o.profile) == "" {
		return usagef("unknown profile %q (use %s)", o.profile, profileList())
	}
	for _, w := range o.with {
		if !provider.ValidOption(w) {
			return usagef("unknown --with %q (use %s)", w, optionList())
		}
	}
	if fi, err := os.Stat(acct.Dir); err == nil && fi.IsDir() && !a.store.Managed(acct) {
		return fmt.Errorf("%s exists but is not a %s account (another tool may use it); to manage it, run: %s account adopt %s", a.tilde(acct.Dir), config.Slug, a.cmd, input)
	}
	ag, err := a.agent(acct.Spec)
	if err != nil {
		return err
	}
	isNew := !a.store.Managed(acct)

	// Plan: the CLI version and the login run in parallel with reading the account.
	version := make(chan string, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), authTimeout)
		defer cancel()
		out, _ := ag.Output(ctx, "", "--version")
		version <- provider.ParseVersion(out)
	}()
	login := make(chan provider.AuthStatus, 1)
	if isNew {
		login <- provider.AuthStatus{}
	} else {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), authTimeout)
			defer cancel()
			s, _ := ag.Auth(ctx, acct.ConfigDir())
			login <- s
		}()
	}
	rec, _, err := account.ReadManaged(acct.Dir)
	if err != nil {
		return err
	}
	profile, options := rec.Profile, rec.Options
	if isNew && profile == "" {
		profile = a.cfg.ProfileName()
	}
	if o.profile != "" {
		profile = o.profile
	}
	if o.with != nil {
		options = o.with
	}
	head := acct.Spec.Name
	if v := <-version; v != "" {
		head += " " + v
	}
	if isNew {
		a.out("%s · new account %s → %s\n", head, acct.Label(), a.tilde(acct.Dir))
	} else {
		a.out("%s · %s → %s\n", head, acct.Label(), a.tilde(acct.Dir))
	}
	ask := a.interactive && !o.yes
	in := bufio.NewReader(a.stdin)
	// waited is the time spent on the user (questions, the login), left out of "done in".
	var waited time.Duration
	asked := time.Now()
	if isNew && o.profile == "" && ask && settingsFormat(acct.Spec) != nil {
		if profile, err = a.askProfile(in, acct.Spec, profile); err != nil {
			return err
		}
	} else if profile != "" {
		a.out("%s%-14s %s\n", a.padding(), "profile", profileDesc(acct.Spec, profile, options))
	}
	if shared := a.sharedNames(acct.Spec); isNew && len(shared) > 0 {
		a.out("%s%-14s %s from %s/%s\n", a.padding(), "shared", strings.Join(shared, ", "), acct.Provider, account.Main)
	}
	if o.relink && !isNew {
		if o.relink, err = a.confirmRelink(in, acct, ag, ask, o.yes); err != nil {
			return err
		}
	}
	trackUsage := o.usage != nil && *o.usage
	noUsage := o.usage != nil && !*o.usage
	if statusLineOf(acct.Spec) != nil && o.usage == nil && ask && (isNew || !a.usageTracking(acct)) {
		if trackUsage, err = a.askYes(in, "usage limits", "track for "+acct.Label()+"?"); err != nil {
			return err
		}
		noUsage = !trackUsage
	}
	status := <-login
	doLogin := o.login != nil && *o.login
	if !status.LoggedIn && o.login == nil && ask {
		if doLogin, err = a.askYes(in, "log in now?", ""); err != nil {
			return err
		}
	}
	if ask {
		waited = time.Since(asked)
	}

	// Apply.
	step := addStep{created: !isNew}
	fail := func(err error) error { return a.addFailed(acct, step, err) }
	changes := 0
	note := func(format string, args ...any) {
		changes++
		a.out("%s %s\n", a.paint(a.colorOut, "32", "✓"), fmt.Sprintf(format, args...))
	}
	warn := func(format string, args ...any) {
		a.out("%s %s\n", a.paint(a.colorOut, "33", "!"), fmt.Sprintf(format, args...))
	}
	work := acct
	if isNew {
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
	step.name = "set up shared files and the push guard"
	p := a.provisioner(ag)
	p.Relink = o.relink
	rep, err := p.Provision(context.Background(), work)
	if err != nil {
		return fail(err)
	}
	step.name = "build settings"
	if profile != "" && (profile != rec.Profile || !slices.Equal(options, rec.Options)) {
		if err := account.SetManagedProfile(work, profile, options); err != nil {
			return fail(err)
		}
	}
	if d, _ := provider.DriverOf(acct.Spec.ID); isNew && d.Seed != nil {
		if err := d.Seed.Seed(a.store.Home, work.StateDir()); err != nil {
			return fail(err)
		}
	}
	srep, err := a.syncSettingsAs(work, acct)
	if err != nil {
		return fail(err)
	}
	if isNew {
		step.name = "move the new account into place"
		if err := os.Rename(work.Dir, acct.Dir); err != nil {
			return fail(err)
		}
		step.created = true
		if r, ok := ag.(provider.Relocator); ok {
			if err := r.Relocate(work.Dir, acct.Dir); err != nil {
				return fail(err)
			}
		}
		note("created %s (private)", a.tilde(acct.Dir))
	}
	a.cache.Forget(acct.Label())
	switch {
	case isNew || srep.Created || srep.Backup != "":
		what := "settings from " + acct.Provider + "/" + account.Main
		if mainAcct, _ := a.store.MainOf(acct.Provider + "/" + account.Main); !fsx.IsFile(account.SettingsPath(mainAcct)) {
			// No main account (or no settings in it) to start from.
			what = "settings created"
			if profile != "" {
				what = "settings: " + profileName(profile, options)
			}
		} else if profile != "" {
			what += " + " + profileName(profile, options)
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
	a.reportProvision(acct, rep, isNew, note, warn)
	if isNew || rep.PluginInstalled {
		if guard := a.guardLine(acct, ag, rep.PluginInstalled); guard != "" {
			note("push guard: %s", guard)
		} else if acct.Spec.Caps.Plugins {
			warn("push guard: the %s plugin is not available here; re-run install.sh, then: %s account add %s", config.Slug, a.cmd, acct.Label())
		}
	}
	// A new account copies main's status line, so it may already run the recorder.
	switch tracking := a.usageTracking(acct); {
	case trackUsage && !tracking:
		if err := a.enableUsage(acct); err != nil {
			warn("usage tracking: %v; run: %s usage setup %s", err, a.cmd, acct.Label())
		} else {
			note("usage tracking on")
		}
	case noUsage && tracking:
		if err := a.disableUsage(acct); err != nil {
			warn("usage tracking: %v; run: %s usage setup --undo %s", err, a.cmd, acct.Label())
		} else {
			note("usage tracking off")
		}
	case isNew && tracking:
		note("usage tracking on")
	}

	// Verify.
	if doLogin {
		a.out("%s opening login…\n", a.paint(a.colorOut, "36", "↗"))
		loginAt := time.Now()
		err := a.runLogin(ag, acct)
		waited += time.Since(loginAt)
		a.cache.Forget(acct.Label())
		if err != nil {
			warn("login did not finish (%v); run: %s account login %s", err, a.cmd, acct.Label())
		}
		ctx, cancel := context.WithTimeout(context.Background(), authTimeout)
		status, _ = ag.Auth(ctx, acct.ConfigDir())
		cancel()
		if status.LoggedIn {
			note("logged in as %s", loginName(status))
		}
	}
	if !isNew && changes == 0 {
		a.out("%s %s is up to date\n", a.paint(a.colorOut, "32", "✓"), acct.Label())
	}
	next := "start: " + a.startCommand(acct)
	if !status.LoggedIn {
		next = "log in: " + a.cmd + " account login " + acct.Label()
	}
	a.out("done in %.1fs · %s · check: %s doctor %s\n", (time.Since(start) - waited).Seconds(), next, a.cmd, acct.Label())
	return nil
}

// accountAddMain sets up a provider's default account (~/.claude, ~/.codex), which the CLI
// creates on its own: squadrn's push guard, usage tracking and the login. Its settings stay the
// user's own, so profiles and shared links do not apply.
func (a *app) accountAddMain(acct account.Account, o addOptions, start time.Time) error {
	if o.profile != "" || o.with != nil || o.relink {
		return usagef("%s is the default account (%s): --profile, --with and --relink apply to other accounts", acct.Label(), a.tilde(acct.Dir))
	}
	ag, err := a.agent(acct.Spec)
	if err != nil {
		return err
	}
	head := acct.Spec.Name
	ctx, cancel := context.WithTimeout(context.Background(), authTimeout)
	out, _ := ag.Output(ctx, "", "--version")
	if v := provider.ParseVersion(out); v != "" {
		head += " " + v
	}
	status, _ := ag.Auth(ctx, acct.ConfigDir())
	cancel()
	a.out("%s · %s, the default account → %s\n", head, acct.Label(), a.tilde(acct.Dir))

	ask := a.interactive && !o.yes
	in := bufio.NewReader(a.stdin)
	var waited time.Duration
	asked := time.Now()
	trackUsage := o.usage != nil && *o.usage
	noUsage := o.usage != nil && !*o.usage
	if statusLineOf(acct.Spec) != nil && o.usage == nil && ask && !a.usageTracking(acct) {
		if trackUsage, err = a.askYes(in, "usage limits", "track for "+acct.Label()+"?"); err != nil {
			return err
		}
	}
	doLogin := o.login != nil && *o.login
	if !status.LoggedIn && o.login == nil && ask {
		if doLogin, err = a.askYes(in, "log in now?", ""); err != nil {
			return err
		}
	}
	if ask {
		waited = time.Since(asked)
	}

	changes := 0
	note := func(format string, args ...any) {
		changes++
		a.out("%s %s\n", a.paint(a.colorOut, "32", "✓"), fmt.Sprintf(format, args...))
	}
	warn := func(format string, args ...any) {
		a.out("%s %s\n", a.paint(a.colorOut, "33", "!"), fmt.Sprintf(format, args...))
	}
	if err := os.MkdirAll(acct.Dir, 0o700); err != nil {
		return err
	}
	installed := false
	if gi, ok := ag.(provider.GuardInstaller); ok && acct.Spec.Caps.Plugins {
		if mp := a.marketplaceDir(); mp == "" {
			warn("push guard: the %s plugin is not available here; re-run install.sh, then: %s account add %s", config.Slug, a.cmd, acct.Label())
		} else {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			installed, err = gi.InstallPlugin(ctx, acct.ConfigDir(), mp)
			cancel()
			if err != nil {
				return fmt.Errorf("push guard for %s: %w", acct.Label(), err)
			}
		}
	}
	if installed {
		note("push guard: %s", a.guardLine(acct, ag, true))
	}
	switch tracking := a.usageTracking(acct); {
	case trackUsage && !tracking:
		if err := a.enableUsage(acct); err != nil {
			warn("usage tracking: %v; run: %s usage setup %s", err, a.cmd, acct.Label())
		} else {
			note("usage tracking on")
		}
	case noUsage && tracking:
		if err := a.disableUsage(acct); err != nil {
			warn("usage tracking: %v; run: %s usage setup --undo %s", err, a.cmd, acct.Label())
		} else {
			note("usage tracking off")
		}
	}
	if doLogin {
		a.out("%s opening login…\n", a.paint(a.colorOut, "36", "↗"))
		loginAt := time.Now()
		err := a.runLogin(ag, acct)
		waited += time.Since(loginAt)
		a.cache.Forget(acct.Label())
		if err != nil {
			warn("login did not finish (%v); run: %s account login %s", err, a.cmd, acct.Label())
		}
		ctx, cancel := context.WithTimeout(context.Background(), authTimeout)
		status, _ = ag.Auth(ctx, acct.ConfigDir())
		cancel()
		if status.LoggedIn {
			note("logged in as %s", loginName(status))
		}
	}
	if changes == 0 {
		a.out("%s %s is up to date\n", a.paint(a.colorOut, "32", "✓"), acct.Label())
	}
	next := "start: " + a.startCommand(acct)
	if !status.LoggedIn {
		next = "log in: " + a.cmd + " account login " + acct.Label()
	}
	a.out("done in %.1fs · %s · check: %s doctor %s\n", (time.Since(start) - waited).Seconds(), next, a.cmd, acct.Label())
	return nil
}

// addFailed reports which step failed, what state the account is in and how to retry.
func (a *app) addFailed(acct account.Account, step addStep, err error) error {
	a.out("%s %s failed: %v\n", a.paint(a.colorOut, "31", "✗"), step.name, err)
	if step.created {
		a.out("  state: %s exists; what is missing is repaired by a re-run\n", a.tilde(acct.Dir))
	} else {
		a.out("  state: nothing was created\n")
	}
	a.out("  retry: %s account add %s\n", a.cmd, acct.Label())
	return errQuiet
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
