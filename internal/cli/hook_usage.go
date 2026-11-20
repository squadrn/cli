package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/squadrn/squadrn/internal/account"
	"github.com/squadrn/squadrn/internal/config"
	"github.com/squadrn/squadrn/internal/proc"
	"github.com/squadrn/squadrn/internal/provider"
	"github.com/squadrn/squadrn/internal/usage"
)

// thenDeadline bounds the wrapped status line command; Claude Code cancels it sooner when a
// newer update arrives.
const thenDeadline = 10 * time.Second

func usageStore(home string) usage.Store {
	return usage.Store{Dir: filepath.Join(config.DirsFor(home, config.Slug).State, "usage")}
}

// statusline records the usage the status line reports, then shows the wrapped command's output
// (--then) or a compact usage line. Recording never changes what is shown.
func (h hookEnv) statusline(args []string) int {
	var then string
	switch {
	case len(args) == 0:
	case len(args) == 2 && args[0] == "--then":
		then = args[1]
	case len(args) == 1 && strings.HasPrefix(args[0], "--then="):
		then = strings.TrimPrefix(args[0], "--then=")
	default:
		_, _ = fmt.Fprintln(h.stderr, hookUsage())
		return exitUsage
	}
	raw, _ := h.readInput()
	name, snap := h.recordStatus(raw)
	if then != "" {
		bin, argv := hookStatusLine().Shell(then)
		ctx, cancel := context.WithTimeout(context.Background(), thenDeadline)
		defer cancel()
		out, err := proc.Output(ctx, proc.Options{Stdin: strings.NewReader(string(raw))}, bin, argv...)
		_, _ = h.stdout.Write(out)
		if err != nil {
			if code := proc.ExitCode(err); code > 0 {
				return code
			}
			return exitError
		}
		return exitOK
	}
	_, _ = fmt.Fprintln(h.stdout, compactUsage(name, snap, h.now()))
	return exitOK
}

func (h hookEnv) recordStatus(raw []byte) (name string, snap usage.Snapshot) {
	defer func() { _ = recover() }()
	now := h.now()
	obs, err := hookStatusLine().Parse(raw, now)
	if err != nil {
		return "", usage.Snapshot{}
	}
	snap = obs
	store, err := newStore(config.Load())
	if err != nil {
		return "", snap
	}
	acct, ok := hookAccount(store)
	if !ok {
		return "", snap
	}
	if merged, _, err := usageStore(store.Home).Record(acct.Provider, acct.Name, obs); err == nil {
		snap = merged
	}
	return acct.Name, snap
}

// compactUsage is "<account> · 5h 42% · 7d 18%", leaving out what is unknown.
func compactUsage(name string, s usage.Snapshot, now time.Time) string {
	var parts []string
	if name != "" {
		parts = append(parts, name)
	}
	if s.Five.Live(now) {
		parts = append(parts, "5h "+plainPct(s.Five, now))
	}
	if s.Seven.Live(now) {
		parts = append(parts, "7d "+plainPct(s.Seven, now))
	}
	return strings.Join(parts, " · ")
}

// limit is the rc.3 plugin's StopFailure hook (matcher rate_limit): the usage limit is recorded
// and notified like any StopFailure through notify.
func (h hookEnv) limit() int {
	raw, _ := h.readInput()
	h.notifyEvent(hookSpec(), raw)
	return exitOK
}

// recordLimit records a turn that stopped on a usage limit in the account's usage file and
// returns when the limit resets (zero when unknown).
func (h hookEnv) recordLimit(raw []byte) (resets time.Time) {
	defer func() { _ = recover() }()
	now := h.now()
	f, err := hookStatusLine().ParseLimit(raw, now, time.Local)
	if err != nil || !f.Limited {
		return time.Time{}
	}
	snap := f.Observation
	store, err := newStore(config.Load())
	if err != nil {
		return snap.LimitResets()
	}
	if acct, ok := hookAccount(store); ok {
		us := usageStore(store.Home)
		if snap.ResetEstimate.IsZero() {
			if old, ok, _ := us.Load(acct.Provider, acct.Name); ok && old.Five != nil && old.Five.ResetsAt.After(now) {
				snap.ResetEstimate = old.Five.ResetsAt
			}
		}
		if merged, _, err := us.Record(acct.Provider, acct.Name, snap); err == nil {
			snap = merged
		}
	}
	return snap.LimitResets()
}

// clock shows t as 15:45 when it is today, else with the weekday (Thu 15:45).
func clock(t, now time.Time) string {
	t, now = t.Local(), now.Local()
	if y, m, d := t.Date(); y == now.Year() && m == now.Month() && d == now.Day() {
		return t.Format("15:04")
	}
	return t.Format("Mon 15:04")
}

// hookSpec is the provider whose CLI runs the usage hooks: the one with a status line.
func hookSpec() provider.Spec {
	for _, spec := range provider.All() {
		if statusLineOf(spec) != nil {
			return spec
		}
	}
	panic("no provider reports usage through a status line")
}

func hookStatusLine() provider.StatusLine { return statusLineOf(hookSpec()) }

// hookAccount is the account a hook runs in, from the CLI's config-dir variable.
func hookAccount(store account.Store) (account.Account, bool) {
	spec := hookSpec()
	return store.ByConfigDir(spec, os.Getenv(spec.ConfigEnv))
}
