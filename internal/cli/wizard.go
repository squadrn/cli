package cli

import (
	"errors"
	"fmt"
	"os"
	"os/signal"
	"slices"
	"strings"
	"time"

	"github.com/squadrn/squadrn-cli/internal/account"
	"github.com/squadrn/squadrn-cli/internal/fsx"
	"github.com/squadrn/squadrn-cli/internal/provider"
	"github.com/squadrn/squadrn-cli/internal/ui/prompt"
)

// intent is what the wizard ends up doing with the name it was given.
type intent int

const (
	intentCreate intent = iota
	intentRepair        // 2a: the account exists
	intentAdopt         // 2b: an unmanaged dir has its name
	intentMain          // 2c: the provider's default account
)

// wizard is account add without a ref (ADR 0014 §3): a slice of steps, each skipped or run
// against the answers so far. Esc goes back to the nearest step not skipped; the answers stay, so
// going back shows them.
type wizard struct {
	a     *app
	p     prompt.Prompter
	o     addOptions
	start time.Time
	// installed are the agent CLIs on PATH; clis all of them.
	clis, installed []detected
	// counts are the accounts per provider, main included; seconds leaves main out.
	counts, seconds map[string]int

	spec        provider.Spec
	name        string
	intent      intent
	custom      bool
	profile     string
	options     []string
	usage       bool
	makeDefault bool
	plan        *addPlan
}

type wizardStep struct {
	skip func(w *wizard) bool
	run  func(w *wizard) error
}

// prompter is how commands ask at this terminal: drawn in place when it understands ANSI, else
// line prompts.
func (a *app) prompter() prompt.Prompter {
	if a.prompt != nil {
		return a.prompt
	}
	g := prompt.DetectGlyphs(os.Getenv)
	if a.ttyErr && os.Getenv("TERM") != "dumb" {
		return prompt.NewTerm(a.stdin, a.stderr, a.colorErr, g)
	}
	return prompt.NewLines(a.stdin, a.stderr, g)
}

// addWizard runs account add without a ref: at a terminal the guided setup, else a usage error.
func (a *app) addWizard(o addOptions) error {
	if a.cfgErr != nil {
		return a.cfgErr
	}
	if !a.interactive || o.yes || o.json {
		return usagef("name the account: %s account add [<provider>/]<name> (the guided setup needs a terminal)", a.cmd)
	}
	_, err := a.runWizard(o)
	return err
}

// runWizard runs the wizard at this terminal and returns the plan it applied.
func (a *app) runWizard(o addOptions) (*addPlan, error) {
	w := &wizard{a: a, p: a.prompter(), o: o, start: time.Now()}
	err := w.run()
	return w.plan, err
}

func (w *wizard) run() error {
	a := w.a
	stop := a.spin("checking agent CLIs…")
	w.clis = a.detectCLIs()
	stop()
	w.p.Intro("New account")
	for _, d := range w.clis {
		if d.path != "" {
			w.installed = append(w.installed, d)
		}
	}
	if len(w.installed) == 0 {
		w.p.Warn("No agent CLI found")
		for _, d := range w.clis {
			w.p.Log(fmt.Sprintf("%s: %s", d.spec.Name, d.spec.InstallHint))
		}
		w.p.Outro("Install one, then run: " + a.cmd + " account add")
		return errQuiet
	}
	w.counts, w.seconds = map[string]int{}, map[string]int{}
	accts, _ := a.store.List()
	for _, acct := range accts {
		w.counts[acct.Provider]++
		if !acct.IsMain() {
			w.seconds[acct.Provider]++
		}
	}
	if err := w.preset(); err != nil {
		return err
	}
	steps := w.steps()
	for i := 0; i < len(steps); {
		if steps[i].skip != nil && steps[i].skip(w) {
			i++
			continue
		}
		err := steps[i].run(w)
		switch {
		case errors.Is(err, prompt.ErrBack):
			prev := i - 1
			for prev >= 0 && steps[prev].skip != nil && steps[prev].skip(w) {
				prev--
			}
			if prev < 0 {
				return w.cancel()
			}
			i = prev
			continue
		case errors.Is(err, prompt.ErrCancelled):
			return w.cancel()
		case errors.Is(err, errDryRun):
			return nil
		case err != nil:
			return err
		}
		i++
	}
	return w.apply()
}

// preset takes what the flags already answer.
func (w *wizard) preset() error {
	if w.o.provider != "" {
		spec, ok := provider.Get(w.o.provider)
		if !ok {
			return usagef("unknown provider %q (use %s)", w.o.provider, strings.Join(providerIDs(), ", "))
		}
		w.spec = spec
	} else if len(w.installed) == 1 {
		w.spec = w.installed[0].spec
	}
	w.profile = w.o.profile
	if w.profile == "" {
		w.profile = w.a.cfg.ProfileName()
	}
	w.options = w.o.with
	w.usage = w.o.usage == nil || *w.o.usage
	return nil
}

func (w *wizard) cancel() error {
	w.p.Cancel("Cancelled · nothing changed")
	return prompt.ErrCancelled
}

func (w *wizard) label() string { return w.spec.ID + "/" + w.name }

func (w *wizard) creating() bool { return w.intent == intentCreate }

func (w *wizard) steps() []wizardStep {
	return []wizardStep{
		{skip: func(w *wizard) bool { return w.o.provider != "" || len(w.installed) == 1 }, run: (*wizard).askProvider},
		{run: (*wizard).askName},
		{skip: func(w *wizard) bool { return !w.creating() }, run: (*wizard).askSetup},
		{skip: func(w *wizard) bool { return !w.creating() || !w.custom || w.o.profile != "" }, run: (*wizard).askProfile},
		{skip: func(w *wizard) bool { return !w.creating() || !w.custom || w.o.with != nil }, run: (*wizard).askOptions},
		{skip: func(w *wizard) bool {
			return !w.creating() || !w.custom || w.o.usage != nil || statusLineOf(w.spec) == nil
		}, run: (*wizard).askUsage},
		{skip: func(w *wizard) bool { return w.intent == intentRepair || !w.custom || w.o.makeDefault }, run: (*wizard).askDefault},
		{run: (*wizard).review},
	}
}

func (w *wizard) askProvider() error {
	var opts []prompt.Option
	def := 0
	for _, d := range w.clis {
		o := prompt.Option{Label: d.spec.Name, Value: d.spec.ID, Detail: d.version, Answer: strings.TrimSpace(d.spec.Name + " " + d.version)}
		switch {
		case d.path == "":
			o.Disabled, o.Status = true, "not installed · "+d.spec.InstallHint
		case w.counts[d.spec.ID] == 0:
			o.Status = "new"
		case w.counts[d.spec.ID] == 1:
			o.Status = "1 account"
		default:
			o.Status = fmt.Sprintf("%d accounts", w.counts[d.spec.ID])
		}
		if d.spec.ID == w.spec.ID || (w.spec.ID == "" && d.spec.ID == w.a.store.DefaultProvider && d.path != "") {
			def = len(opts)
		}
		opts = append(opts, o)
	}
	i, err := w.p.Select(prompt.Select{Title: "Provider", Options: opts, Default: def})
	if err != nil {
		return err
	}
	w.spec = w.clis[i].spec
	return nil
}

// validName checks a name typed for spec: main is allowed (it is set up, 2c).
func validName(name string) error {
	if name == account.Main {
		return nil
	}
	return account.ValidateName(name)
}

// otherProvider is another provider's account named name, if any.
func (w *wizard) otherProvider(name string) string {
	accts, _ := w.a.store.List()
	for _, acct := range accts {
		if acct.Name == name && acct.Provider != w.spec.ID && !acct.IsMain() {
			return acct.Label()
		}
	}
	return ""
}

func (w *wizard) nameHint(name string) prompt.Hint {
	if name == "" || validName(name) != nil {
		return prompt.Hint{}
	}
	if name == account.Main {
		return prompt.Hint{Text: fmt.Sprintf("→ ~/%s · the %s default account", w.spec.MainDir, w.spec.Name)}
	}
	label := w.spec.ID + "/" + name
	if other := w.otherProvider(name); other != "" {
		return prompt.Hint{Text: fmt.Sprintf("%q is also %s: start it with %s %s", name, other, w.a.cmd, label), Warn: true}
	}
	acct, err := w.a.store.Define(label)
	if err != nil {
		return prompt.Hint{}
	}
	return prompt.Hint{Text: fmt.Sprintf("→ %s · start with: %s %s", w.a.tilde(acct.Dir), w.a.cmd, w.startFor(acct))}
}

// startFor is the start command's argument once acct exists: its bare name when no other
// account has that name.
func (w *wizard) startFor(acct account.Account) string {
	if w.otherProvider(acct.Name) != "" {
		return acct.Label()
	}
	return acct.Name
}

func (w *wizard) askName() error {
	for {
		name, err := w.p.Text(prompt.Text{
			Title: "Account name", Placeholder: "e.g. work", Initial: w.name,
			Validate: validName, Hint: w.nameHint,
		})
		if err != nil {
			return err
		}
		w.name, w.intent = name, intentCreate
		again, err := w.branch()
		if err != nil {
			return err
		}
		if !again {
			return nil
		}
	}
}

// branch asks what to do with a name that is taken (2a, 2b) or main (2c); again means choose
// another name.
func (w *wizard) branch() (again bool, err error) {
	label := w.label()
	var title string
	var first prompt.Option
	def := 1
	var chosen intent
	if w.name == account.Main {
		title, first, def, chosen = account.Main+" is the default account", prompt.Option{Label: "Set up " + label}, 0, intentMain
	} else {
		acct, err := w.a.store.Define(label)
		if err != nil {
			return false, err
		}
		switch {
		case w.a.store.Managed(acct):
			title, first, chosen = label+" already exists", prompt.Option{Label: "Repair it"}, intentRepair
		case fsx.IsDir(acct.Dir):
			if w.o.adopt {
				w.intent = intentAdopt
				return false, nil
			}
			title, first, chosen = w.a.tilde(acct.Dir)+" exists, not managed", prompt.Option{Label: "Adopt it"}, intentAdopt
		default:
			return false, nil
		}
	}
	i, err := w.p.Select(prompt.Select{Title: title, Options: []prompt.Option{first, {Label: "Choose another name"}}, Default: def})
	switch {
	case errors.Is(err, prompt.ErrBack):
		return true, nil
	case err != nil:
		return false, err
	case i == 1:
		return true, nil
	}
	w.intent = chosen
	return false, nil
}

func (w *wizard) askSetup() error {
	def := 0
	if w.custom {
		def = 1
	}
	i, err := w.p.Select(prompt.Select{Title: "Setup", Default: def, Options: []prompt.Option{
		{Label: "Recommended", Detail: "the " + w.profile + " profile, usage tracking on"},
		{Label: "Customize", Detail: "profile, options, usage tracking, default account"},
	}})
	if err != nil {
		return err
	}
	w.custom = i == 1
	return nil
}

func (w *wizard) askProfile() error {
	var opts []prompt.Option
	def := 0
	for i, p := range provider.ProfileNames {
		opts = append(opts, prompt.Option{Label: p.Name, Detail: w.spec.ProfileSummary(p.Name)})
		if p.Name == w.profile {
			def = i
		}
	}
	i, err := w.p.Select(prompt.Select{Title: "Profile", Options: opts, Default: def})
	if err != nil {
		return err
	}
	w.profile = provider.ProfileNames[i].Name
	return nil
}

func (w *wizard) askOptions() error {
	var opts []prompt.Option
	var checked []int
	for i, o := range provider.Options {
		opts = append(opts, prompt.Option{Label: o.Name, Detail: o.Summary})
		if slices.Contains(w.options, o.Name) {
			checked = append(checked, i)
		}
	}
	got, err := w.p.MultiSelect(prompt.MultiSelect{Title: "Options", Options: opts, Checked: checked})
	if err != nil {
		return err
	}
	w.options = nil
	for _, i := range got {
		w.options = append(w.options, provider.Options[i].Name)
	}
	return nil
}

func (w *wizard) askUsage() error {
	yes, err := w.p.Confirm(prompt.Confirm{Title: "Track usage limits", Default: w.usage})
	if err != nil {
		return err
	}
	w.usage = yes
	return nil
}

// firstAccount: the provider has no account besides main yet, so the new one is made the default.
func (w *wizard) firstAccount() bool { return w.seconds[w.spec.ID] == 0 && w.creating() }

func (w *wizard) askDefault() error {
	yes, err := w.p.Confirm(prompt.Confirm{Title: "Make it the default account", Default: w.makeDefault || w.firstAccount()})
	if err != nil {
		return err
	}
	w.makeDefault = yes
	return nil
}

// options turns the answers into account add's options; the login is asked after the review.
func (w *wizard) addOptions() addOptions {
	o := addOptions{yes: true, login: new(bool), adopt: w.intent == intentAdopt, repair: w.intent == intentRepair}
	if w.creating() {
		o.profile, o.with = w.profile, w.options
		if statusLineOf(w.spec) != nil {
			usage := w.usage
			o.usage = &usage
		}
	} else {
		o.usage = w.o.usage
	}
	o.makeDefault = w.o.makeDefault || w.makeDefault || (!w.custom && w.firstAccount())
	return o
}

func (w *wizard) verb() string {
	switch w.intent {
	case intentRepair:
		return "Repair"
	case intentAdopt:
		return "Adopt"
	case intentMain:
		return "Set up"
	}
	return "Create"
}

func (w *wizard) review() error {
	a := w.a
	p, err := a.planAdd(w.label(), w.addOptions())
	if err != nil {
		return err
	}
	w.plan = p
	acct := p.acct
	body := []string{fmt.Sprintf("%-9s %s → %s (private)", "account", acct.Label(), a.tilde(acct.Dir))}
	for _, l := range a.planLines(p) {
		if l[0] == "login" {
			continue // asked once the account exists
		}
		body = append(body, fmt.Sprintf("%-9s %s", l[0], l[1]))
	}
	if w.o.dryRun {
		w.p.Note("Review", body...)
		w.p.Outro("Dry run · nothing was changed", "Same without questions: "+w.command(false))
		return errDryRun
	}
	yes, err := w.p.Confirm(prompt.Confirm{Title: "Review", Body: body, Question: w.verb() + " " + acct.Label() + "?", Default: true})
	if err != nil {
		return err
	}
	if !yes {
		return prompt.ErrCancelled
	}
	return nil
}

// errDryRun ends the wizard after the review of a dry run.
var errDryRun = errors.New("dry run")

// command is the same account add without questions.
func (w *wizard) command(login bool) string {
	o := w.addOptions()
	verb := " account add "
	if w.intent == intentRepair {
		verb = " account repair "
	}
	parts := []string{w.a.cmd + verb + w.label()}
	if o.adopt {
		parts = append(parts, "--adopt")
	}
	if w.creating() {
		if o.profile != w.a.cfg.ProfileName() {
			parts = append(parts, "--profile "+o.profile)
		}
		if len(o.with) > 0 {
			parts = append(parts, "--with "+strings.Join(o.with, ","))
		}
	}
	if o.usage != nil && statusLineOf(w.spec) != nil {
		parts = append(parts, map[bool]string{true: "--usage", false: "--no-usage"}[*o.usage])
	}
	if o.makeDefault {
		parts = append(parts, "--default")
	}
	parts = append(parts, map[bool]string{true: "--login", false: "--no-login"}[login])
	return strings.Join(parts, " ")
}

// apply carries out the reviewed plan, then offers the login. Ctrl-C while applying a new
// account removes what was made.
func (w *wizard) apply() error {
	a, p := w.a, w.plan
	r := &frameReporter{a: a, g: prompt.DetectGlyphs(os.Getenv)}
	start := time.Now()
	before := a.cfg.DefaultAccount
	interrupted := make(chan os.Signal, 1)
	signal.Notify(interrupted, os.Interrupt)
	res, err := a.applyAdd(p, r)
	signal.Stop(interrupted)
	if len(interrupted) > 0 {
		if p.isNew && err == nil {
			_ = os.RemoveAll(p.acct.Dir)
			if p.makeDefault && a.cfg.DefaultAccount != before {
				_ = a.saveDefaultAccount(before)
			}
		}
		return w.cancel()
	}
	if err != nil {
		w.p.Note(w.verb()+" "+p.acct.Label()+" failed", r.take()...)
		w.p.Cancel(err.Error())
		return errQuiet
	}
	title := map[intent]string{intentCreate: "Created", intentRepair: "Repaired", intentAdopt: "Adopted", intentMain: "Set up"}[w.intent]
	if lines := r.take(); len(lines) > 0 {
		w.p.Note(title+" "+p.acct.Label(), lines...)
	} else {
		w.p.Note(p.acct.Label() + " is up to date")
	}
	login := w.o.login != nil && *w.o.login
	if !p.status.LoggedIn && w.o.login == nil {
		asked := time.Now()
		yes, err := w.p.Confirm(prompt.Confirm{Title: "Log in now?", Default: true})
		p.waited += time.Since(asked)
		login = err == nil && yes
	}
	if login && !p.status.LoggedIn {
		w.p.Log(r.g.Open + " " + p.acct.Spec.Binary + " " + strings.Join(p.ag.LoginArgs(), " ") + " " + r.g.Ellipsis)
		a.addLogin(p, r, &res)
		for _, l := range r.take() {
			w.p.Log(l)
		}
	}
	if !p.status.LoggedIn {
		w.p.Warn("not logged in · " + a.cmd + " account login " + p.acct.Label())
	}
	w.p.Outro(fmt.Sprintf("Ready in %.1fs · start: %s", (time.Since(start)-p.waited).Seconds(), a.startCommand(p.acct)),
		"Same without questions: "+w.command(login))
	return nil
}

// frameReporter collects what applying reports, for the wizard to draw under a step.
type frameReporter struct {
	a     *app
	g     prompt.Glyphs
	lines []string
}

func (*frameReporter) header(*addPlan)         {}
func (*frameReporter) planLine(string, string) {}
func (r *frameReporter) note(text string)      { r.lines = append(r.lines, r.g.Check+" "+text) }

func (r *frameReporter) warn(text string)             { r.lines = append(r.lines, r.g.Warn+" "+text) }
func (r *frameReporter) info(text string)             { r.lines = append(r.lines, text) }
func (*frameReporter) loginStart()                    {}
func (*frameReporter) done(*addPlan, addResult) error { return nil }

func (r *frameReporter) failed(acct account.Account, step addStep, err error) error {
	r.lines = append(r.lines, r.g.Warn+" "+step.name+" failed: "+err.Error(), "retry: "+retryCommand(r.a, acct, step))
	return fmt.Errorf("%s failed", step.name)
}

// take returns the lines collected so far and starts over.
func (r *frameReporter) take() []string {
	out := r.lines
	r.lines = nil
	return out
}
