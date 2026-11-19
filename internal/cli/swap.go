package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/squadrn/squadrn/internal/account"
	"github.com/squadrn/squadrn/internal/config"
	"github.com/squadrn/squadrn/internal/fsx"
	"github.com/squadrn/squadrn/internal/grid"
	"github.com/squadrn/squadrn/internal/inuse"
	"github.com/squadrn/squadrn/internal/proc"
	"github.com/squadrn/squadrn/internal/provider"
	"github.com/squadrn/squadrn/internal/ui"
	"github.com/squadrn/squadrn/internal/usage"
	"github.com/squadrn/squadrn/internal/worktree"
)

// stopWait is how long a stopped session gets to exit before the next step.
var stopWait = 10 * time.Second

type swapOptions struct {
	session, to, worktree string
	yes, force            bool
}

func (a *app) swapCmd() *cobra.Command {
	var o swapOptions
	cmd := &cobra.Command{
		Use:   "swap",
		Short: "Move a conversation to another account and resume it there",
		Long: `Move a conversation to another account and resume it there, e.g. when an account hits its
usage limit. Without arguments the conversation is the one that last hit its limit in this
directory (or below it), or one running here; several candidates are offered in a picker. The
target account is --to, else picked from the accounts ranked by their usage.

A running session is stopped first (asks; in a grid its pane is restarted in place). Moving a
conversation to an account of another organization asks first too. When the target account
already holds the conversation and it went on there, the swap is refused unless --force.

--worktree <name> moves the latest conversation of that agent worktree instead.`,
		Example: fmt.Sprintf(`  %[1]s swap                              the conversation that just hit its limit here
  %[1]s swap --to personal                the same, to claude/personal
  %[1]s swap --worktree feat-a --to work  latest conversation of worktree feat-a
  %[1]s swap --session <id> --to work     any conversation, by session ID`, a.cmd),
		Args: noArgs,
		RunE: func(*cobra.Command, []string) error { return a.swap(o) },
	}
	cmd.Flags().StringVar(&o.session, "session", "", "session ID of the conversation to move")
	cmd.Flags().StringVar(&o.worktree, "worktree", "", "move the latest conversation of this agent worktree")
	cmd.Flags().StringVar(&o.to, "to", "", "account to move it to")
	cmd.Flags().BoolVarP(&o.yes, "yes", "y", false, "do not ask for confirmation")
	cmd.Flags().BoolVarP(&o.force, "force", "f", false, "move it even if the target's copy went on and differs")
	_ = cmd.RegisterFlagCompletionFunc("worktree", a.completeWorktrees)
	_ = cmd.RegisterFlagCompletionFunc("to", a.completeAccounts)
	return cmd
}

// swapSource is a conversation that can be moved.
type swapSource struct {
	session provider.Session
	// cwd is the directory it runs in, where it is resumed.
	cwd string
	// worktree names the agent worktree it runs in ("" when unknown), for its grid pane.
	worktree         string
	limited, running bool
	at               time.Time
}

func (a *app) swap(o swapOptions) error {
	if o.session != "" && o.worktree != "" {
		return usagef("--session and --worktree name the conversation two ways; pass one")
	}
	if o.worktree != "" {
		if err := worktree.ValidateName(o.worktree); err != nil {
			return err
		}
	}
	spec := a.defaultSpec()
	var to *account.Account
	if o.to != "" {
		acct, err := a.store.Get(o.to)
		if err != nil {
			return err
		}
		to, spec = &acct, acct.Spec
	}
	if !spec.Caps.SessionTransfer {
		return fmt.Errorf("%s cannot transfer conversations", spec.Name)
	}
	if o.session != "" && !spec.SessionID.MatchString(o.session) {
		return usagef("--session %q: %v", o.session, provider.ErrBadSessionID)
	}
	ag, err := a.agent(spec)
	if err != nil {
		return err
	}
	all, err := a.store.List()
	if err != nil {
		return err
	}
	// Conversations only move between accounts of the same provider: formats differ across CLIs.
	var accts []account.Account
	for _, acct := range all {
		if acct.Provider == spec.ID {
			accts = append(accts, acct)
		}
	}
	dirs := make([]string, len(accts))
	for i, acct := range accts {
		dirs[i] = acct.Dir
	}
	src, err := a.swapSource(ag, o, spec, accts, dirs)
	if err != nil {
		return err
	}
	from := accts[src.session.Owner]
	if to == nil {
		acct, err := a.pickTarget(accts, from)
		if err != nil {
			return err
		}
		to = &acct
	}
	st := a.statuses([]account.Account{from, *to}, true)
	if st[1].Err == nil && !st[1].Status.LoggedIn {
		return fmt.Errorf("%s is not logged in; run: %s account login %s", to.Label(), a.cmd, to.Label())
	}
	if from.Label() != to.Label() {
		fromOrg, toOrg := st[0].Status.OrgName, st[1].Status.OrgName
		if st[0].Err == nil && st[1].Err == nil && fromOrg != toOrg {
			if err := a.confirm(o.yes, "Move the conversation from %s (%s) to %s (%s), another organization", from.Label(), orgOr(fromOrg), to.Label(), orgOr(toOrg)); err != nil {
				return err
			}
		}
		if model := ag.UnavailableModel(src.session, st[1].Status); model != "" {
			a.warnf("the conversation uses %s; %s is on the Pro plan, which may not include it", model, to.Label())
		}
	}
	pane := ""
	if src.worktree != "" {
		pane = a.paneIn(src.cwd, src.worktree)
	}
	if pane != "" {
		if err := a.confirm(o.yes, "Restart pane %s (%q) as %s; its current process is stopped", pane, src.worktree, to.Label()); err != nil {
			return err
		}
	} else if err := a.quiesce(a.holders(ag, src, from, accts, dirs), o.yes); err != nil {
		return err
	}
	if from.Label() != to.Label() {
		if err := ag.TransferSession(src.session, to.Dir, o.force); errors.Is(err, provider.ErrDiverged) {
			return fmt.Errorf("the conversation went on in %s after it was copied there, so moving %s's version would lose that part; re-run with --force to overwrite it", to.Label(), from.Label())
		} else if err != nil {
			return err
		}
		h := usage.Handoff{At: a.now(), Session: src.session.ID, From: from.Label(), To: to.Label(), Cwd: src.cwd}
		if err := a.ledger().Append(h); err != nil {
			a.warnf("cannot record the handoff: %v", err)
		}
	}
	what := "swap"
	if o.worktree != "" {
		what += " " + o.worktree
	}
	a.out("%s: %s → %s (session %s)\n", what, from.Label(), to.Label(), src.session.ID)
	if pane == "" {
		if err := os.Chdir(src.cwd); err != nil {
			return err
		}
		return a.session(ag, *to, ag.ResumeArgs(src.session.ID), "")
	}
	command := proc.ShellQuote(append([]string{selfPath(), "-a", to.Label()}, ag.ResumeArgs(src.session.ID)...)...)
	if err := (grid.Tmux{}).Respawn(context.Background(), pane, src.cwd, grid.Agent{Account: to.Label(), Name: src.worktree}, command); err != nil {
		return err
	}
	a.out("pane %s now runs %s as %s\n", pane, src.worktree, to.Label())
	return nil
}

func (a *app) ledger() usage.Ledger {
	return usage.Ledger{Path: filepath.Join(config.DirsFor(a.store.Home, config.Slug).State, "handoffs.jsonl")}
}

// swapSource finds the conversation to move: --session, --worktree, else the candidates in the
// working directory.
func (a *app) swapSource(ag provider.Agent, o swapOptions, spec provider.Spec, accts []account.Account, dirs []string) (swapSource, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return swapSource{}, err
	}
	switch {
	case o.worktree != "":
		repo, err := a.repoHere(context.Background(), spec)
		if err != nil {
			return swapSource{}, err
		}
		dir := repo.Dir(o.worktree)
		if _, err := os.Stat(dir); err != nil {
			return swapSource{}, fmt.Errorf("worktree not found: %s", a.tilde(dir))
		}
		s, err := ag.LatestSessionIn(dirs, dir)
		if errors.Is(err, provider.ErrNoSession) {
			return swapSource{}, fmt.Errorf("no conversation found for worktree %q in any account", o.worktree)
		} else if err != nil {
			return swapSource{}, err
		}
		return swapSource{session: s, cwd: dir, worktree: o.worktree}, nil
	case o.session != "":
		s, err := ag.FindSession(dirs, o.session)
		if err != nil {
			return swapSource{}, fmt.Errorf("%w in any %s account", err, spec.Name)
		}
		return a.located(ag, swapSource{session: s}, spec, cwd)
	}
	cands := a.swapCandidates(ag, accts, dirs, cwd)
	switch len(cands) {
	case 0:
		return swapSource{}, fmt.Errorf("no conversation hit its limit or runs in %s; pass --session <id> or --worktree <name>", a.tilde(cwd))
	case 1:
		return a.located(ag, cands[0], spec, cwd)
	}
	if !a.interactive {
		var b strings.Builder
		for _, c := range cands {
			fmt.Fprintf(&b, "\n  --session %s  %s", c.session.ID, a.sourceDetail(c, accts))
		}
		return swapSource{}, usagef("several conversations could be moved; pass one with --session:%s", b.String())
	}
	items := make([]ui.Item, len(cands))
	for i, c := range cands {
		items[i] = ui.Item{Label: accts[c.session.Owner].Label() + " " + shortID(c.session.ID), Detail: a.sourceDetail(c, accts), Active: true}
	}
	choice, err := ui.Pick(a.cmd+" swap › conversation", items, a.stdin, a.stderr, a.colorErr)
	if err != nil {
		return swapSource{}, err
	}
	return a.located(ag, cands[choice], spec, cwd)
}

// located fills in where a conversation runs: the directory in its transcript (falling back to
// what the candidate knew, then the working directory) and the agent worktree that is.
func (a *app) located(ag provider.Agent, src swapSource, spec provider.Spec, cwd string) (swapSource, error) {
	if dir := ag.SessionCwd(src.session); dir != "" {
		if _, err := os.Stat(dir); err != nil {
			return swapSource{}, fmt.Errorf("conversation %s was started in %s, which no longer exists", src.session.ID, dir)
		}
		src.cwd = dir
	}
	if src.cwd == "" {
		src.cwd = cwd
	}
	if parent := filepath.Dir(src.cwd); strings.HasSuffix(filepath.ToSlash(parent), "/"+spec.WorktreeDir) {
		src.worktree = filepath.Base(src.cwd)
	}
	return src, nil
}

// swapCandidates lists the conversations swap could mean in dir: the one that most recently hit
// its limit there (unless it was moved since), then the sessions running there, newest first.
func (a *app) swapCandidates(ag provider.Agent, accts []account.Account, dirs []string, dir string) []swapSource {
	var out []swapSource
	var lim usage.Snapshot
	for _, acct := range accts {
		snap, ok, _ := a.usageStore().Load(acct.Provider, acct.Name)
		if ok && snap.LimitedSession != "" && fsx.Within(snap.LimitedCwd, dir) && snap.LimitedAt.After(lim.LimitedAt) {
			lim = snap
		}
	}
	if lim.LimitedSession != "" {
		_, moved, _ := a.ledger().Latest(func(h usage.Handoff) bool {
			return h.Session == lim.LimitedSession && h.At.After(lim.LimitedAt)
		})
		if s, err := ag.FindSession(dirs, lim.LimitedSession); err == nil && !moved {
			out = append(out, swapSource{session: s, cwd: lim.LimitedCwd, limited: true, at: lim.LimitedAt})
		}
	}
	var running []swapSource
	for _, ls := range ag.RunningSessions(dirs, func(pid int) bool { return proc.Alive(pid, "") }) {
		if !fsx.Within(ls.Cwd, dir) {
			continue
		}
		if i := slices.IndexFunc(out, func(c swapSource) bool { return c.session.ID == ls.SessionID }); i >= 0 {
			out[i].running = true
			continue
		}
		if slices.ContainsFunc(running, func(c swapSource) bool { return c.session.ID == ls.SessionID }) {
			continue
		}
		if s, err := ag.FindSession(dirs, ls.SessionID); err == nil {
			running = append(running, swapSource{session: s, cwd: ls.Cwd, running: true, at: s.ModTime})
		}
	}
	slices.SortStableFunc(running, func(x, y swapSource) int { return y.at.Compare(x.at) })
	return append(out, running...)
}

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

// sourceDetail describes a candidate: "claude/work ~/repo · 5m ago · limited, running".
func (a *app) sourceDetail(c swapSource, accts []account.Account) string {
	var state []string
	if c.limited {
		state = append(state, "limited")
	}
	if c.running {
		state = append(state, "running")
	}
	parts := []string{accts[c.session.Owner].Label()}
	if c.cwd != "" {
		parts = append(parts, a.tilde(c.cwd))
	}
	parts = append(parts, "· "+ago(c.at, a.now()))
	if len(state) > 0 {
		parts = append(parts, "· "+strings.Join(state, ", "))
	}
	return strings.Join(parts, " ")
}

// pickTarget offers the accounts a conversation on from can move to, best first.
func (a *app) pickTarget(accts []account.Account, from account.Account) (account.Account, error) {
	now := a.now()
	stop := a.spin(fmt.Sprintf("checking %d account(s)…", len(accts)))
	statuses := a.statuses(accts, false)
	stop()
	cands := a.candidates(accts, statuses)
	byLabel := map[string]account.Account{}
	for _, acct := range accts {
		byLabel[acct.Label()] = acct
	}
	ranked, next := usage.Rank(cands, from.Label(), now)
	if len(ranked) == 0 {
		msg := fmt.Sprintf("no other account can take the conversation from %s", from.Label())
		if !next.IsZero() {
			msg += fmt.Sprintf("; the first limit resets at %s", clock(next, now))
		}
		return account.Account{}, errors.New(msg)
	}
	if !a.interactive {
		return account.Account{}, usagef("pass the account to move the conversation to with --to (best: %s)", ranked[0].Account)
	}
	items := make([]ui.Item, len(ranked))
	for i, c := range ranked {
		s := c.Snapshot
		items[i] = ui.Item{
			Label:  c.Account,
			Detail: fmt.Sprintf("5h %s · 7d %s · %s", plainPct(s.Five, now), plainPct(s.Seven, now), c.State(now)),
			Active: true,
		}
	}
	choice, err := ui.Pick(fmt.Sprintf("%s swap › move %s's conversation to", a.cmd, from.Label()), items, a.stdin, a.stderr, a.colorErr)
	if err != nil {
		return account.Account{}, err
	}
	return byLabel[ranked[choice].Account], nil
}

func plainPct(w *usage.Window, now time.Time) string {
	if !w.Live(now) {
		return "-"
	}
	return fmt.Sprintf("%.0f%%", w.UsedPct)
}

// holder is a live process that may write the conversation.
type holder struct {
	pid     int
	started string
	desc    string
}

// holders are the processes that may be writing the conversation: Claude Code sessions listing
// it in their registry, and sessions squadrn started as from in its directory (recorded both
// there and in from's config dir) unless the registry shows they hold another conversation.
func (a *app) holders(ag provider.Agent, src swapSource, from account.Account, accts []account.Account, dirs []string) []holder {
	var out []holder
	seen := map[int]bool{os.Getpid(): true}
	other := map[int]bool{}
	for _, ls := range ag.RunningSessions(dirs, func(pid int) bool { return proc.Alive(pid, "") }) {
		if ls.SessionID != src.session.ID {
			other[ls.PID] = true
			continue
		}
		if !seen[ls.PID] {
			seen[ls.PID] = true
			out = append(out, holder{pid: ls.PID, desc: fmt.Sprintf("%s session (pid %d)", accts[ls.Owner].Label(), ls.PID)})
		}
	}
	inAcct := map[int]bool{}
	for _, e := range inuse.Active(from.Dir) {
		inAcct[e.PID] = true
	}
	for _, e := range inuse.Active(src.cwd) {
		if seen[e.PID] || other[e.PID] || !inAcct[e.PID] || (e.Account != "" && e.Account != from.Label()) {
			continue
		}
		seen[e.PID] = true
		out = append(out, holder{pid: e.PID, started: e.Started, desc: fmt.Sprintf("%s session (pid %d)", from.Label(), e.PID)})
	}
	return out
}

// quiesce stops every holder (asking first) so the conversation never has two writers: an
// interrupt, then — asked again — termination; it refuses when one is still alive.
// errNoInterrupt: the platform cannot ask a session to stop, only terminate it.
var errNoInterrupt = errors.New("cannot interrupt another console's process")

func (a *app) quiesce(hs []holder, yes bool) error {
	for _, h := range hs {
		if !proc.Alive(h.pid, h.started) {
			continue // ended with another holder (Windows: squadrn and its claude child)
		}
		if err := a.confirm(yes, "Stop the %s that holds the conversation", h.desc); err != nil {
			return err
		}
		err := stopProcess(h.pid, false)
		if err == nil && a.waitGone(h) {
			continue
		}
		if !errors.Is(err, errNoInterrupt) {
			if err := a.confirm(yes, "The %s is still running; terminate it", h.desc); err != nil {
				return err
			}
		}
		if err := stopProcess(h.pid, true); err != nil && proc.Alive(h.pid, h.started) {
			return fmt.Errorf("stop the %s: %w", h.desc, err)
		}
		if !a.waitGone(h) {
			return fmt.Errorf("the %s is still running; stop it, then swap again", h.desc)
		}
	}
	return nil
}

func (a *app) waitGone(h holder) bool {
	deadline := time.Now().Add(stopWait)
	for proc.Alive(h.pid, h.started) {
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(100 * time.Millisecond)
	}
	return true
}

func orgOr(org string) string {
	if org == "" {
		return "personal"
	}
	return org
}
