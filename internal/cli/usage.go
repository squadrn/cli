package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/squadrn/cli/internal/account"
	"github.com/squadrn/cli/internal/fsx"
	"github.com/squadrn/cli/internal/provider"
	"github.com/squadrn/cli/internal/provider/claude"
	"github.com/squadrn/cli/internal/usage"
)

const watchEvery = 5 * time.Second

func (a *app) usageCmd() *cobra.Command {
	var asJSON, refresh, watch, setup, undo, all, yes bool
	cmd := &cobra.Command{
		Use:   "usage [<account>]",
		Short: "Show each account's usage limits and which one to use next",
		Long: fmt.Sprintf(`Show what the status line last reported about each account's usage limits (5-hour and
7-day windows), when it last hit a limit, and the best account to continue with in this repository.

Usage is recorded by the status line, so it needs a one-time setup per settings file:
%[1]s usage --setup wraps the status line command (your own one keeps running and showing).`, a.cmd),
		Example: fmt.Sprintf(`  %[1]s usage                 all accounts
  %[1]s usage work            details of claude/work
  %[1]s usage --setup --all   record usage for every account
  %[1]s usage --setup --undo  restore the previous status line`, a.cmd),
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: a.completeAccounts,
		RunE: func(_ *cobra.Command, args []string) error {
			target := ""
			if len(args) == 1 {
				target = args[0]
			}
			switch {
			case undo && !setup:
				return usagef("--undo goes with --setup")
			case all && !setup:
				return usagef("--all goes with --setup")
			case setup && all && target != "":
				return usagef("give an account or --all, not both")
			case setup:
				return a.usageSetup(target, all, undo, yes)
			case watch && asJSON:
				return usagef("--watch and --json cannot be combined")
			case watch:
				for {
					if a.colorOut {
						a.out("\033[H\033[2J")
					}
					if err := a.usageShow(target, false, refresh); err != nil {
						return err
					}
					refresh = false
					time.Sleep(watchEvery)
				}
			}
			return a.usageShow(target, asJSON, refresh)
		},
	}
	f := cmd.Flags()
	f.BoolVar(&asJSON, "json", false, "print JSON")
	f.BoolVar(&refresh, "refresh", false, "query login status, plan and organisation again")
	f.BoolVar(&watch, "watch", false, "redraw every 5 seconds")
	f.BoolVar(&setup, "setup", false, "record usage through the account's status line")
	f.BoolVar(&undo, "undo", false, "with --setup: restore the previous status line")
	f.BoolVar(&all, "all", false, "with --setup: every account")
	f.BoolVarP(&yes, "yes", "y", false, "do not ask for confirmation")
	return cmd
}

type usageLimit struct {
	At       time.Time `json:"at"`
	Session  string    `json:"session,omitempty"`
	Cwd      string    `json:"cwd,omitempty"`
	ResetsAt time.Time `json:"resets_at,omitzero"`
}

type usageRow struct {
	Account    string        `json:"account"`
	Plan       string        `json:"plan,omitempty"`
	Org        string        `json:"org,omitempty"`
	Email      string        `json:"email,omitempty"`
	LoggedIn   *bool         `json:"logged_in"` // null when the status is unavailable
	State      usage.State   `json:"state"`
	Tracking   bool          `json:"tracking"`
	FiveHour   *usage.Window `json:"five_hour,omitempty"`
	SevenDay   *usage.Window `json:"seven_day,omitempty"`
	Spend      *usage.Spend  `json:"spend,omitempty"`
	ObservedAt time.Time     `json:"observed_at,omitzero"`
	LastLimit  *usageLimit   `json:"last_limit,omitempty"`
	Error      string        `json:"error,omitempty"`
}

type usageReport struct {
	Accounts []usageRow `json:"accounts"`
	// From is the account last limited or active in this directory; Best the one to use next.
	From      string    `json:"from,omitempty"`
	Best      string    `json:"best,omitempty"`
	NextReset time.Time `json:"next_reset,omitzero"`
}

func (a *app) usageStore() usage.Store { return usageStore(a.store.Home) }

func (a *app) usageReport(accts []account.Account, refresh bool) usageReport {
	now := a.now()
	stop := a.spin(fmt.Sprintf("checking %d account(s)…", len(accts)))
	statuses := a.statuses(accts, refresh)
	stop()
	rep := usageReport{Accounts: make([]usageRow, len(accts))}
	cands := make([]usage.Candidate, len(accts))
	cwd, _ := os.Getwd()
	var fromAt time.Time
	for i, acct := range accts {
		st := statuses[i]
		snap, _, _ := a.usageStore().Load(acct.Provider, acct.Name)
		c := usage.Candidate{
			Account: acct.Label(), Provider: acct.Provider, Org: st.Status.OrgName,
			LoggedIn: st.Err != nil || st.Status.LoggedIn, Metered: metered(st.Status),
			Unsupported: !acct.Spec.Caps.UsageReport, Snapshot: snap,
		}
		cands[i] = c
		r := usageRow{
			Account: acct.Label(), Plan: st.Status.SubscriptionType, Org: st.Status.OrgName, Email: st.Status.Email,
			State: c.State(now), Tracking: a.usageTracking(acct),
			FiveHour: snap.Five, SevenDay: snap.Seven, Spend: snap.Spend, ObservedAt: snap.ObservedAt,
		}
		if c.Metered {
			r.Plan = "api"
		}
		if st.Err != nil {
			r.Error = st.Err.Error()
			r.State = snap.Effective(now)
		} else {
			r.LoggedIn = &st.Status.LoggedIn
		}
		if !snap.LimitedAt.IsZero() {
			r.LastLimit = &usageLimit{At: snap.LimitedAt, Session: snap.LimitedSession, Cwd: snap.LimitedCwd, ResetsAt: snap.LimitResets()}
		}
		rep.Accounts[i] = r
		for _, seen := range []struct {
			dir string
			at  time.Time
		}{{snap.LimitedCwd, snap.LimitedAt}, {snap.Cwd, snap.ObservedAt}} {
			if seen.dir != "" && within(cwd, seen.dir) && seen.at.After(fromAt) {
				rep.From, fromAt = acct.Label(), seen.at
			}
		}
	}
	ranked, next := usage.Rank(cands, rep.From, now, usage.RankOptions{})
	if len(ranked) > 0 {
		rep.Best = ranked[0].Account
	}
	rep.NextReset = next
	return rep
}

// metered: logged in without a subscription, billed per use (no usage limits to track).
func metered(s provider.AuthStatus) bool {
	m := strings.ToLower(s.AuthMethod)
	return s.LoggedIn && s.SubscriptionType == "" && (strings.Contains(m, "api") || strings.Contains(m, "key"))
}

// within: dir is base or inside it.
func within(dir, base string) bool {
	if dir == "" || base == "" {
		return false
	}
	if samePath(dir, base) {
		return true
	}
	rd, err1 := filepath.EvalSymlinks(dir)
	rb, err2 := filepath.EvalSymlinks(base)
	if err1 == nil && err2 == nil {
		dir, base = rd, rb
	}
	base = filepath.Clean(base)
	rel, err := filepath.Rel(base, dir)
	return err == nil && rel != "." && !strings.HasPrefix(rel, "..") && !filepath.IsAbs(rel) && filepath.Dir(base) != base
}

func (a *app) usageShow(target string, asJSON, refresh bool) error {
	accts, err := a.store.List()
	if err != nil {
		return err
	}
	var one *account.Account
	if target != "" {
		acct, err := a.store.Get(target)
		if err != nil {
			return err
		}
		one = &acct
	}
	rep := a.usageReport(accts, refresh)
	if one != nil {
		for _, r := range rep.Accounts {
			if r.Account == one.Label() {
				if asJSON {
					return a.printJSON(r)
				}
				a.usageDetails(r, rep)
				return nil
			}
		}
	}
	if asJSON {
		return a.printJSON(rep)
	}
	a.usageTable(rep)
	return nil
}

func (a *app) usageTable(rep usageReport) {
	now := a.now()
	w := tabwriter.NewWriter(a.stdout, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "ACCOUNT\tPLAN\tORG\t5H\tRESETS\t7D\tRESETS\tSTATE\tSEEN")
	for _, r := range rep.Accounts {
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n", r.Account, dash(r.Plan), dash(r.Org),
			a.pct(r.FiveHour, now), resets(r.FiveHour, now), a.pct(r.SevenDay, now), resets(r.SevenDay, now),
			a.state(r.State), ago(r.ObservedAt, now))
	}
	_ = w.Flush()
	switch {
	case rep.Best != "":
		a.out("best for this repo: %s\n", rep.Best)
	case !rep.NextReset.IsZero():
		a.out("every account is at its limit; the first resets at %s\n", clock(rep.NextReset, now))
	}
	if untracked := a.untracked(rep); untracked != "" {
		a.out("not tracked: %s (run: %s usage --setup)\n", untracked, a.cmd)
	}
}

func (a *app) untracked(rep usageReport) string {
	var out []string
	for _, r := range rep.Accounts {
		if !r.Tracking && r.State != usage.NA {
			out = append(out, r.Account)
		}
	}
	return strings.Join(out, ", ")
}

func (a *app) usageDetails(r usageRow, rep usageReport) {
	now := a.now()
	a.out("account   %s\n", r.Account)
	if r.Email != "" {
		a.out("login     %s\n", r.Email)
	}
	a.out("plan      %s\n", dash(r.Plan))
	a.out("org       %s\n", dash(r.Org))
	a.out("state     %s\n", a.state(r.State))
	a.out("5 hours   %s (resets %s)\n", a.pct(r.FiveHour, now), resets(r.FiveHour, now))
	a.out("7 days    %s (resets %s)\n", a.pct(r.SevenDay, now), resets(r.SevenDay, now))
	if s := r.Spend; s != nil {
		line := fmt.Sprintf("%.0f%%", s.UsedPct)
		if s.UsedUSD != nil && s.LimitUSD != nil {
			line += fmt.Sprintf(" ($%.2f / $%.2f)", *s.UsedUSD, *s.LimitUSD)
		}
		if s.Period != "" {
			line += " " + s.Period
		}
		a.out("spend     %s\n", line)
	}
	a.out("seen      %s\n", ago(r.ObservedAt, now))
	if l := r.LastLimit; l != nil {
		line := ago(l.At, now)
		if !l.ResetsAt.IsZero() {
			line += ", resets " + clock(l.ResetsAt, now)
		}
		a.out("limit hit %s\n", line)
		if l.Cwd != "" {
			a.out("          in %s (session %s)\n", a.tilde(l.Cwd), dash(l.Session))
		}
	}
	if h, ok, _ := a.ledger().Last(r.Account); ok {
		a.out("handoff   %s: %s → %s (session %s)\n", ago(h.At, now), h.From, h.To, shortID(h.Session))
	}
	tracking := "on"
	if !r.Tracking {
		tracking = "off (run: " + a.cmd + " usage --setup " + r.Account + ")"
	}
	a.out("tracking  %s\n", tracking)
	if rep.Best != "" && rep.Best != r.Account {
		a.out("best for this repo: %s\n", rep.Best)
	}
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func live(w *usage.Window, now time.Time) bool {
	return w != nil && (w.ResetsAt.IsZero() || w.ResetsAt.After(now))
}

func (a *app) pct(w *usage.Window, now time.Time) string {
	if !live(w, now) {
		return "-"
	}
	s := fmt.Sprintf("%.0f%%", w.UsedPct)
	if !a.colorOut {
		return s
	}
	const width = 5
	n := min(max(int(w.UsedPct/100*width+0.5), 0), width)
	code := "32"
	switch {
	case w.UsedPct >= 100:
		code = "31"
	case w.UsedPct >= usage.HighFive:
		code = "33"
	}
	return fmt.Sprintf("%4s ", s) + a.paint(true, code, strings.Repeat("█", n)) + strings.Repeat("░", width-n)
}

func resets(w *usage.Window, now time.Time) string {
	if !live(w, now) || w.ResetsAt.IsZero() {
		return "-"
	}
	return clock(w.ResetsAt, now)
}

func (a *app) state(s usage.State) string {
	codes := map[usage.State]string{usage.Limited: "31", usage.High: "33", usage.OK: "32", usage.Reset: "32"}
	if c, ok := codes[s]; ok {
		return a.paint(a.colorOut, c, string(s))
	}
	return string(s)
}

func ago(t, now time.Time) string {
	if t.IsZero() {
		return "-"
	}
	d := max(now.Sub(t), 0)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	}
	return fmt.Sprintf("%dd ago", int(d.Hours()/24))
}

// settingsPath is the account's settings file, which holds its status line.
func settingsPath(acct account.Account) string {
	return filepath.Join(acct.Dir, acct.Spec.Settings)
}

// usageTracking: the account's status line already runs sprawl's recorder.
func (a *app) usageTracking(acct account.Account) bool {
	if !acct.Spec.Caps.UsageReport {
		return false
	}
	cmd, ok, err := claude.StatusLineCommand(settingsPath(acct))
	if err != nil || !ok {
		return false
	}
	_, ours := unwrapStatusLine(cmd)
	return ours
}

// statusLineBinary is what the status line runs: the installed binary (a fixed path that
// survives command renames and updates), else this one.
func (a *app) statusLineBinary() string {
	if fsx.IsFile(a.layout.Binary()) {
		return a.layout.Binary()
	}
	if exe := selfPath(); fsx.IsFile(exe) {
		return exe
	}
	exe, _ := os.Executable()
	return exe
}

const statusLineHook = " hook statusline"

// wrapStatusLine is the status line command that records usage and then runs old ("" for none).
func (a *app) wrapStatusLine(old string) (string, error) {
	bin, err := statusLineWord(a.statusLineBinary(), a.store.Home)
	if err != nil {
		return "", err
	}
	cmd := bin + statusLineHook
	if old != "" {
		q, err := statusLineWord(old, a.store.Home)
		if err != nil {
			return "", err
		}
		cmd += " --then " + q
	}
	return cmd, nil
}

// unwrapStatusLine recognises a command made by wrapStatusLine and returns the wrapped one.
func unwrapStatusLine(cmd string) (old string, ours bool) {
	_, rest, ok := shellWord(cmd)
	if !ok || !strings.HasPrefix(rest, statusLineHook) {
		return "", false
	}
	rest = strings.TrimPrefix(rest, statusLineHook)
	if rest == "" {
		return "", true
	}
	if !strings.HasPrefix(rest, " --then ") {
		return "", false
	}
	old, rest, ok = shellWord(strings.TrimPrefix(rest, " --then "))
	if !ok || rest != "" {
		return "", false
	}
	return old, true
}

// shellWord splits off the first word: plain characters and '…' quoted runs (with '\” for a
// quote), as statusLineWord writes them.
func shellWord(s string) (word, rest string, ok bool) {
	var b strings.Builder
	i := 0
	for i < len(s) && s[i] != ' ' {
		switch {
		case s[i] == '\'':
			j := strings.IndexByte(s[i+1:], '\'')
			if j < 0 {
				return "", "", false
			}
			b.WriteString(s[i+1 : i+1+j])
			i += j + 2
		case s[i] == '\\' && i+1 < len(s) && s[i+1] == '\'':
			b.WriteByte('\'')
			i += 2
		default:
			b.WriteByte(s[i])
			i++
		}
	}
	return b.String(), s[i:], i > 0
}

// settingsGroup is one settings file and the accounts that use it (through links to main's).
type settingsGroup struct {
	path     string
	accounts []string
}

func realPath(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return p
}

func (a *app) settingsGroups(targets []account.Account) ([]settingsGroup, error) {
	all, err := a.store.List()
	if err != nil {
		return nil, err
	}
	var groups []settingsGroup
	seen := map[string]bool{}
	for _, t := range targets {
		if !t.Spec.Caps.UsageReport {
			continue
		}
		key := realPath(settingsPath(t))
		if seen[key] {
			continue
		}
		seen[key] = true
		g := settingsGroup{path: settingsPath(t)}
		for _, x := range all {
			if x.Spec.Caps.UsageReport && (realPath(settingsPath(x)) == key || fsx.SameFile(settingsPath(x), key)) {
				g.accounts = append(g.accounts, x.Label())
			}
		}
		groups = append(groups, g)
	}
	return groups, nil
}

func (a *app) usageTargets(target string, all bool) ([]account.Account, error) {
	if all {
		return a.store.List()
	}
	if target == "" {
		target = a.account
	}
	acct, err := a.resolve(target)
	if err != nil {
		return nil, err
	}
	if !acct.Spec.Caps.UsageReport {
		return nil, fmt.Errorf("%s does not report usage limits", acct.Spec.Name)
	}
	return []account.Account{acct}, nil
}

func (a *app) usageSetup(target string, all, undo, yes bool) error {
	targets, err := a.usageTargets(target, all)
	if err != nil {
		return err
	}
	groups, err := a.settingsGroups(targets)
	if err != nil {
		return err
	}
	for _, g := range groups {
		if err := a.setupGroup(g, undo, yes); err != nil {
			return err
		}
	}
	return nil
}

func (a *app) setupGroup(g settingsGroup, undo, yes bool) error {
	where := fmt.Sprintf("%s (%s)", a.tilde(realPath(g.path)), strings.Join(g.accounts, ", "))
	cmd, has, err := claude.StatusLineCommand(g.path)
	if err != nil {
		return err
	}
	old, ours := unwrapStatusLine(cmd)
	if undo {
		if !has || !ours {
			a.out("usage tracking is not set up in %s\n", where)
			return nil
		}
		if err := a.confirm(yes, "Restore the previous status line in %s", where); err != nil {
			return err
		}
		if err := claude.SetStatusLineCommand(g.path, old); err != nil {
			return err
		}
		a.out("usage tracking off: %s\n", where)
		return nil
	}
	if has && ours {
		a.out("usage tracking already on: %s\n", where)
		return nil
	}
	wrapped, err := a.wrapStatusLine(cmd)
	if err != nil {
		return err
	}
	what := "Record usage limits through the status line of " + where
	if has {
		what += "; it keeps running " + cmd
	}
	if err := a.confirm(yes, "%s", what); err != nil {
		return err
	}
	if err := claude.SetStatusLineCommand(g.path, wrapped); err != nil {
		return err
	}
	a.out("usage tracking on: %s\n", where)
	return nil
}

// offerUsageTracking asks once, at account add, whether to record the account's usage. choice
// is the --usage/--no-usage flag; nil asks at a terminal and does nothing otherwise.
func (a *app) offerUsageTracking(acct account.Account, choice *bool) error {
	if !acct.Spec.Caps.UsageReport || a.usageTracking(acct) {
		return nil
	}
	if choice == nil {
		if !a.interactive {
			return nil
		}
		a.errf("Track usage limits for this account in its status line? [Y/n] ")
		line, err := bufio.NewReader(a.stdin).ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return err
		}
		yes := !strings.HasPrefix(strings.ToLower(strings.TrimSpace(line)), "n")
		choice = &yes
	}
	if !*choice {
		return nil
	}
	groups, err := a.settingsGroups([]account.Account{acct})
	if err != nil {
		return err
	}
	for _, g := range groups {
		if err := a.setupGroup(g, false, true); err != nil {
			return err
		}
	}
	return nil
}

// restoreStatusLines undoes usage tracking in every account (self-uninstall).
func (a *app) restoreStatusLines() {
	accts, err := a.store.List()
	if err != nil {
		return
	}
	groups, err := a.settingsGroups(accts)
	if err != nil {
		return
	}
	for _, g := range groups {
		cmd, has, err := claude.StatusLineCommand(g.path)
		old, ours := unwrapStatusLine(cmd)
		if err != nil || !has || !ours {
			continue
		}
		if err := claude.SetStatusLineCommand(g.path, old); err != nil {
			a.warnf("could not restore the status line in %s: %v", a.tilde(g.path), err)
			continue
		}
		a.out("  restored the status line in %s\n", a.tilde(realPath(g.path)))
	}
}
