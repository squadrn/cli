package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/squadrn/cli/internal/account"
	"github.com/squadrn/cli/internal/config"
	"github.com/squadrn/cli/internal/proc"
	"github.com/squadrn/cli/internal/provider"
	"github.com/squadrn/cli/internal/provider/claude"
	"github.com/squadrn/cli/internal/usage"
)

// thenDeadline bounds the wrapped status line command; Claude Code cancels it sooner when a
// newer update arrives.
const thenDeadline = 10 * time.Second

func usageStore(home string) usage.Store {
	return usage.Store{Dir: filepath.Join(config.DirsFor(home, config.Slug).State, "usage")}
}

// hookAccount is the account a hook runs in, from the provider's config-dir variable: unset is
// main; a dir that is no known account gives ok false.
func hookAccount(store account.Store, spec provider.Spec) (account.Account, bool) {
	dir := os.Getenv(spec.ConfigEnv)
	accts, err := store.List()
	if err != nil {
		return account.Account{}, false
	}
	for _, a := range accts {
		if a.Provider != spec.ID {
			continue
		}
		if dir == "" && a.IsMain() || dir != "" && samePath(a.Dir, dir) {
			return a, true
		}
	}
	return account.Account{}, false
}

func samePath(a, b string) bool {
	if filepath.Clean(a) == filepath.Clean(b) {
		return true
	}
	ra, err1 := filepath.EvalSymlinks(a)
	rb, err2 := filepath.EvalSymlinks(b)
	return err1 == nil && err2 == nil && ra == rb
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
		_, _ = fmt.Fprintln(h.stderr, hookUsage)
		return exitUsage
	}
	raw, _ := io.ReadAll(io.LimitReader(h.stdin, 4<<20))
	name, snap := h.recordStatus(raw)
	if then != "" {
		bin, argv := statusLineShell(then)
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
	obs, err := claude.ParseStatusLine(raw, now)
	if err != nil {
		return "", usage.Snapshot{}
	}
	snap = obs
	store, err := newStore(config.Load())
	if err != nil {
		return "", snap
	}
	acct, ok := hookAccount(store, provider.Claude)
	if !ok {
		return "", snap
	}
	obs.Account = acct.Label()
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
	if s.Five != nil && (s.Five.ResetsAt.IsZero() || s.Five.ResetsAt.After(now)) {
		parts = append(parts, fmt.Sprintf("5h %.0f%%", s.Five.UsedPct))
	}
	if s.Seven != nil && (s.Seven.ResetsAt.IsZero() || s.Seven.ResetsAt.After(now)) {
		parts = append(parts, fmt.Sprintf("7d %.0f%%", s.Seven.UsedPct))
	}
	return strings.Join(parts, " · ")
}

// limit records a turn that stopped on a usage limit (StopFailure, matcher rate_limit) and
// tells the user which command moves the session to another account.
func (h hookEnv) limit() int {
	raw, _ := io.ReadAll(io.LimitReader(h.stdin, 4<<20))
	msg := h.recordLimit(raw)
	if msg == "" {
		return exitOK
	}
	argv := notifyCommand(runtime.GOOS, "Claude: usage limit", msg)
	if argv == nil {
		return exitOK
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _ = proc.Output(ctx, proc.Options{}, argv[0], argv[1:]...)
	return exitOK
}

func (h hookEnv) recordLimit(raw []byte) (msg string) {
	defer func() { _ = recover() }()
	now := h.now()
	f, err := claude.ParseStopFailure(raw, now, time.Local)
	if err != nil || !f.Limited {
		return ""
	}
	cfg, cfgErr := config.Load()
	store, err := newStore(cfg, cfgErr)
	if err != nil {
		return ""
	}
	label := "this account"
	snap := f.Observation
	if acct, ok := hookAccount(store, provider.Claude); ok {
		label = acct.Label()
		snap.Account = label
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
	msg = label + " hit its usage limit"
	if r := snap.LimitResets(); !r.IsZero() {
		msg += " (resets " + clock(r, now) + ")"
	}
	return msg + " — " + cfg.CommandName() + " swap"
}

// clock shows t as 15:45, with the weekday when it is not within the next 24 hours.
func clock(t, now time.Time) string {
	t = t.Local()
	if d := t.Sub(now); d >= 0 && d < 24*time.Hour {
		return t.Format("15:04")
	}
	return t.Format("Mon 15:04")
}
