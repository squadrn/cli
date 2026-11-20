package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"sync"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/squadrn/squadrn/internal/account"
	"github.com/squadrn/squadrn/internal/fsx"
	"github.com/squadrn/squadrn/internal/provider"
	"github.com/squadrn/squadrn/internal/usage"
	"github.com/squadrn/squadrn/internal/worktree"
)

const watchEvery = 5 * time.Second

func (a *app) usageCmd() *cobra.Command {
	var asJSON, refresh, watch bool
	cmd := &cobra.Command{
		Use:   "usage [<account>]",
		Short: "Show each account's usage limits and which one to use next",
		Long: fmt.Sprintf(`Show what each account last reported about its usage limits (5-hour and 7-day windows),
when it last hit a limit, and, inside a repository, the best account to continue with there.

Usage is recorded by the status line, so it needs a one-time setup per account:
%[1]s usage setup wraps the status line command (your own one keeps running and showing).`, a.cmd),
		Example: fmt.Sprintf(`  %[1]s usage                 all accounts
  %[1]s usage work            details of claude/work
  %[1]s usage setup --all     record usage for every account
  %[1]s usage setup --undo    restore the previous status line`, a.cmd),
		Args:              optionalArg,
		ValidArgsFunction: a.completeAccounts,
		RunE: func(_ *cobra.Command, args []string) error {
			target := ""
			if len(args) == 1 {
				target = args[0]
			}
			switch {
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
	f.BoolVar(&refresh, "refresh", false, "query login status, plan and organization again")
	f.BoolVar(&watch, "watch", false, "redraw every 5 seconds")
	cmd.AddCommand(a.usageSetupCmd())
	return cmd
}

func (a *app) usageSetupCmd() *cobra.Command {
	var undo, all, yes bool
	cmd := &cobra.Command{
		Use:   "setup [<account>]",
		Short: "Record an account's usage through its status line",
		Long: `Record an account's usage through its status line: the status line command in the account's
settings.json is wrapped, so squadrn sees what Claude Code reports while your own command keeps
running and showing. Each account has its own settings.json, so each is set up on its own.`,
		Args:              optionalArg,
		ValidArgsFunction: a.completeAccounts,
		RunE: func(_ *cobra.Command, args []string) error {
			target := ""
			if len(args) == 1 {
				target = args[0]
			}
			if all && target != "" {
				return usagef("give an account or --all, not both")
			}
			return a.usageSetup(target, all, undo, yes)
		},
	}
	cmd.Flags().BoolVar(&undo, "undo", false, "restore the previous status line")
	cmd.Flags().BoolVar(&all, "all", false, "every account")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "do not ask for confirmation")
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
	a.queryUsage(accts, statuses, refresh)
	stop()
	rep := usageReport{Accounts: make([]usageRow, len(accts))}
	cands := a.candidates(accts, statuses)
	cwd, _ := os.Getwd()
	var fromAt time.Time
	for i, acct := range accts {
		st, c, snap := statuses[i], cands[i], cands[i].Snapshot
		r := usageRow{
			Account: acct.Label(), Plan: st.Status.SubscriptionType, Org: st.Status.OrgName, Email: st.Status.Email,
			State: c.State(now), Tracking: acct.Spec.Caps.UsageQuery || a.usageTracking(acct),
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
			if seen.dir != "" && fsx.Within(cwd, seen.dir) && seen.at.After(fromAt) {
				rep.From, fromAt = acct.Label(), seen.at
			}
		}
	}
	// Without an account seen here, the best one is of the default provider, like swap's pick;
	// outside a repository nothing was seen and there is nothing to continue.
	pool := cands
	if rep.From == "" {
		def := a.defaultSpec().ID
		pool = slices.DeleteFunc(slices.Clone(cands), func(c usage.Candidate) bool { return c.Provider != def })
	}
	ranked, next := usage.Best(pool, rep.From, now)
	if len(ranked) == 0 {
		rep.NextReset = next
	} else if rep.From != "" || worktree.Toplevel(context.Background(), cwd) != "" {
		rep.Best = ranked[0].Account
	}
	return rep
}

const (
	// usageQueryTimeout bounds reading one account's usage limits from its CLI.
	usageQueryTimeout = 5 * time.Second
	// usageQueryFresh: a recorded snapshot younger than this is shown without asking the CLI.
	usageQueryFresh = 5 * time.Minute
)

// queryUsage reads the usage limits of logged-in accounts whose CLI reports them on request
// (Caps.UsageQuery) and records them, unless the account was checked recently and refresh is
// off. A CLI that reports no limits is recorded as asked (QueriedAt), so it is not asked again
// for a while either; one that fails to answer keeps what was recorded and is asked next time.
func (a *app) queryUsage(accts []account.Account, statuses []provider.AuthResult, refresh bool) {
	now := a.now()
	var wg sync.WaitGroup
	for i, acct := range accts {
		st := statuses[i]
		if !acct.Spec.Caps.UsageQuery || st.Err != nil || !st.Status.LoggedIn || st.Status.Metered() {
			continue
		}
		if snap, ok, _ := a.usageStore().Load(acct.Provider, acct.Name); ok && !refresh && now.Sub(snap.Checked()) < usageQueryFresh {
			continue
		}
		ag, err := a.agent(acct.Spec)
		rl, ok := ag.(provider.RateLimiter)
		if err != nil || !ok {
			continue
		}
		wg.Go(func() {
			ctx, cancel := context.WithTimeout(context.Background(), usageQueryTimeout)
			defer cancel()
			snap, err := rl.RateLimits(ctx, acct.ConfigDir())
			switch {
			case err == nil:
				snap.QueriedAt = now
			case errors.Is(err, usage.ErrNoLimits):
				snap = usage.Snapshot{QueriedAt: now}
			default:
				return
			}
			_, _, _ = a.usageStore().Record(acct.Provider, acct.Name, snap)
		})
	}
	wg.Wait()
}

// candidates describes accts, with their login statuses, for usage.Rank.
func (a *app) candidates(accts []account.Account, statuses []provider.AuthResult) []usage.Candidate {
	cands := make([]usage.Candidate, len(accts))
	for i, acct := range accts {
		snap, _, _ := a.usageStore().Load(acct.Provider, acct.Name)
		st := statuses[i]
		cands[i] = usage.Candidate{
			Account: acct.Label(), Provider: acct.Provider,
			LoggedIn: st.Err != nil || st.Status.LoggedIn, Metered: st.Status.Metered(),
			Unsupported: !acct.Spec.Caps.UsageReport && !acct.Spec.Caps.UsageQuery, Snapshot: snap,
		}
	}
	return cands
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
			a.pct(r.FiveHour, usage.HighFive, now), resets(r.FiveHour, now), a.pct(r.SevenDay, usage.HighSeven, now), resets(r.SevenDay, now),
			a.state(r.State), ago(r.ObservedAt, now))
	}
	_ = w.Flush()
	switch {
	case rep.Best != "":
		a.out("best for this repo: %s\n", rep.Best)
	case !rep.NextReset.IsZero():
		a.out("every account is at its limit; the first resets at %s\n", clock(rep.NextReset, now))
	}
	if untracked := a.untracked(rep); len(untracked) > 0 {
		target := "--all"
		if len(untracked) == 1 {
			target = untracked[0]
		}
		a.out("not tracked: %s (run: %s usage setup %s)\n", strings.Join(untracked, ", "), a.cmd, target)
	}
}

func (a *app) untracked(rep usageReport) []string {
	var out []string
	for _, r := range rep.Accounts {
		if !r.Tracking && r.State != usage.NA && r.State != usage.LoggedOut {
			out = append(out, r.Account)
		}
	}
	return out
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
	a.out("5 hours   %s (resets %s)\n", a.pct(r.FiveHour, usage.HighFive, now), resets(r.FiveHour, now))
	a.out("7 days    %s (resets %s)\n", a.pct(r.SevenDay, usage.HighSeven, now), resets(r.SevenDay, now))
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
		tracking = "off (run: " + a.cmd + " usage setup " + r.Account + ")"
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

func (a *app) pct(w *usage.Window, high float64, now time.Time) string {
	if !w.Live(now) {
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
	case w.UsedPct >= high:
		code = "33"
	}
	return fmt.Sprintf("%4s ", s) + a.paint(true, code, strings.Repeat("█", n)) + strings.Repeat("░", width-n)
}

func resets(w *usage.Window, now time.Time) string {
	if !w.Live(now) || w.ResetsAt.IsZero() {
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

func (a *app) usageTargets(target string, all bool) ([]account.Account, error) {
	if all {
		return a.store.List()
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
	if len(groups) > 1 && !yes {
		// One question for all of them, naming every settings file and the accounts it covers.
		var pending []string
		for _, g := range groups {
			_, _, has, ours, err := g.tracked()
			if err != nil {
				return err
			}
			if on := has && ours; on == undo {
				pending = append(pending, a.groupWhere(g))
			}
		}
		if len(pending) > 0 {
			what := "Record usage limits through the status line of "
			if undo {
				what = "Restore the previous status line in "
			}
			if err := a.confirm(false, "%s%s", what, strings.Join(pending, "; ")); err != nil {
				return err
			}
		}
		yes = true
	}
	for _, g := range groups {
		if err := a.setupGroup(g, undo, yes, false); err != nil {
			return err
		}
	}
	return nil
}
