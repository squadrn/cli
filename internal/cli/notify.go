package cli

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/squadrn/squadrn/internal/account"
	"github.com/squadrn/squadrn/internal/config"
	"github.com/squadrn/squadrn/internal/notify"
	"github.com/squadrn/squadrn/internal/proc"
	"github.com/squadrn/squadrn/internal/provider"
)

// delivery is one notification handed to the desktop notifier, by the hook to a detached
// `squadrn hook deliver` so the hook returns at once.
type delivery struct {
	Title string      `json:"title"`
	Body  string      `json:"body"`
	Kind  notify.Kind `json:"kind"`
	Sound bool        `json:"sound,omitempty"`
	// Group is the session: a newer notification of the session replaces the older one.
	Group string `json:"group,omitempty"`
	// Activate is the bundle id of the terminal app running the session and Execute the
	// command that shows its tmux pane (macOS click actions).
	Activate string `json:"activate,omitempty"`
	Execute  string `json:"execute,omitempty"`
}

// Test seams: the test binary replaces both (TestMain), so no test ever reaches a real
// notifier or leaves a detached process behind.
var (
	// sendDesktop shows d; replaceID is the notifier's id of the session's previous notification
	// where it replaces by id. It returns the tool used and the new id.
	sendDesktop = func(ctx context.Context, d delivery, replaceID string) (tool, id string, err error) {
		n, ok := findNotifier()
		if !ok {
			return "", "", fmt.Errorf("no desktop notifier found (%s)", n.Note)
		}
		id, err = n.send(ctx, d, replaceID)
		return n.Tool, id, err
	}
	// startDelivery runs `squadrn hook deliver` for d in the background.
	startDelivery = func(d delivery) error {
		b, err := json.Marshal(d)
		if err != nil {
			return err
		}
		cmd := exec.Command(selfPath(), "hook", "deliver", base64.RawURLEncoding.EncodeToString(b))
		return startDetached(cmd)
	}
)

// deliverDeadline bounds one desktop notification.
const deliverDeadline = 10 * time.Second

func notifyStore(home string) notify.Store {
	return notify.Store{Dir: config.DirsFor(home, config.Slug).State}
}

// notifyEvent handles a hook of spec's CLI: it maps the payload to an event, records a usage
// limit and the session's state (the tmux pane shows it), applies the policy and delivers. It
// never fails the hook. terminal: the hook runs synchronously, so a notification can be the
// terminal sequence it returns (printed by the caller).
func (h hookEnv) notifyEvent(spec provider.Spec, raw []byte, terminal bool) (output string) {
	defer func() { _ = recover() }()
	d, _ := provider.DriverOf(spec.ID)
	if d.Events == nil {
		return ""
	}
	e, err := d.Events.ParseEvent(raw)
	if err != nil || e.Kind == "" && e.State() == "" {
		return ""
	}
	cfg, cfgErr := config.Load()
	store, err := newStore(cfg, cfgErr)
	if err != nil {
		return ""
	}
	ns := notifyStore(store.Home)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	pane, inTmux := tmuxPane(ctx)
	if e.Kind == "" {
		// A working agent: only a change is recorded (PostToolUse runs after every tool).
		if a, ok := ns.Load().Agents[e.Session]; ok && a.State == notify.Working && a.Pane == pane.id {
			return ""
		}
	}
	if e.Kind == notify.Failed && e.Reason == notify.ReasonRateLimit && statusLineOf(spec) != nil {
		e.Resets = h.recordLimit(raw)
	}
	e.Agent = spec.Name
	if acct, ok := store.ByConfigDir(spec, os.Getenv(spec.ConfigEnv)); ok {
		e.Account, e.AccountRef = acct.Name, acct.Label()
	}
	if e.Cwd == "" {
		e.Cwd, _ = os.Getwd()
	}
	e.Locate(worktreeDirs())
	e.Pane = pane.name
	agent := notify.Agent{
		State: e.State(), At: h.now(), Account: e.AccountRef, Cwd: e.Cwd,
		Pane: pane.id, Socket: pane.socket, Bundle: terminalBundle(),
	}
	return h.dispatch(ctx, e, agent, cfg, ns, pane, inTmux, terminal)
}

// dispatch records the session's state, applies the policy to e and delivers it.
func (h hookEnv) dispatch(ctx context.Context, e notify.Event, agent notify.Agent, cfg config.Config, ns notify.Store, pane paneState, inTmux, terminal bool) (output string) {
	s := cfg.Notify
	now := h.now()
	focus := notify.FocusUnknown
	if e.Kind != "" && s.On() && s.Wants(e.Kind) && s.WhenName() == notify.WhenUnfocused {
		focus = probeFocus(ctx, pane, inTmux)
	}
	var dec notify.Decision
	changed := false
	err := ns.Update(now, func(st *notify.State) {
		changed = st.SetAgent(e.Session, agent)
		if e.Kind != "" {
			dec = notify.Decide(e, s, st, now, focus)
		}
	})
	if err != nil && e.Kind != "" {
		dec = notify.Decide(e, s, &notify.State{}, now, focus)
	}
	if inTmux && (changed || err != nil) {
		setPaneState(ctx, pane.id, agent.State)
	}
	if e.Kind == "" {
		return ""
	}
	title := notify.Title(e)
	entry := notify.Entry{At: now, Kind: e.Kind, Reason: e.Reason, Title: title}
	if !dec.Deliver {
		entry.Result, entry.Why = "skipped", dec.Reason
		ns.Log(entry)
		return ""
	}
	body := notify.Body(e, cfg.CommandName(), s.Preview, now)
	_, protocol := notify.Terminal(os.Getenv)
	ch := s.Route(e.Kind, terminal && protocol != "", inTmux)
	var methods []string
	if ch.Bell && ringBell(ctx, pane.tty) == nil {
		methods = append(methods, notify.MethodTmux)
	}
	if ch.Terminal {
		output = notify.Sequence(protocol, title, body, dec.Sound)
		methods = append(methods, notify.MethodTerminal)
	}
	entry.Result, entry.Method = "sent", strings.Join(methods, "+")
	if ch.Desktop {
		d := delivery{
			Title: title, Body: body, Kind: e.Kind, Sound: dec.Sound, Group: e.Session,
			Activate: agent.Bundle, Execute: focusCommand(e.Session, agent.Pane),
		}
		if err := startDelivery(d); err != nil {
			entry.Result, entry.Why, entry.Method = "failed", err.Error(), notify.MethodDesktop
		} else if len(methods) == 0 {
			return output // the delivery logs itself
		}
	}
	if len(methods) == 0 && !ch.Desktop {
		entry.Result, entry.Why = "skipped", "no channel for method "+s.MethodName()
	}
	ns.Log(entry)
	return output
}

// focusCommand is the click action of a desktop notification: `squadrn focus <session>` shows
// the session's tmux pane. "" when there is no pane or the session id is not a plain token.
func focusCommand(session, pane string) string {
	if pane == "" || !validToken(session) {
		return ""
	}
	return proc.ShellQuote(selfPath(), "focus", session)
}

func validToken(s string) bool {
	if s == "" || len(s) > 128 {
		return false
	}
	for _, r := range s {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '-' && r != '_' {
			return false
		}
	}
	return true
}

// worktreeDirs are where every provider's CLI creates worktrees.
func worktreeDirs() []string {
	var out []string
	for _, s := range provider.All() {
		if s.WorktreeDir != "" {
			out = append(out, s.WorktreeDir)
		}
	}
	return out
}

// deliver is `squadrn hook deliver <payload>`: the detached half of a notification.
func (h hookEnv) deliver(arg string) int {
	b, err := base64.RawURLEncoding.DecodeString(arg)
	var d delivery
	if err == nil {
		err = json.Unmarshal(b, &d)
	}
	if err != nil {
		_, _ = fmt.Fprintln(h.stderr, hookUsage)
		return exitUsage
	}
	home, err := config.Home()
	if err != nil {
		return exitError
	}
	_, _ = h.send(notifyStore(home), d)
	return exitOK
}

// send shows d now and logs the result.
func (h hookEnv) send(ns notify.Store, d delivery) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), deliverDeadline)
	defer cancel()
	var replace string
	if d.Group != "" {
		replace = ns.Load().Sessions[d.Group].ReplaceID
	}
	tool, id, err := sendDesktop(ctx, d, replace)
	if err == nil && id != "" && d.Group != "" {
		_ = ns.Update(h.now(), func(st *notify.State) {
			if l, ok := st.Sessions[d.Group]; ok {
				l.ReplaceID = id
				st.Sessions[d.Group] = l
			}
		})
	}
	entry := notify.Entry{At: h.now(), Kind: d.Kind, Result: "sent", Method: tool, Title: d.Title}
	if err != nil {
		entry.Result, entry.Why = "failed", err.Error()
	}
	ns.Log(entry)
	return tool, err
}

func (a *app) notifyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "notify",
		Short: "Check and test notifications (input needed, failed, done)",
		Example: fmt.Sprintf(`  %[1]s notify status
  %[1]s notify test
  %[1]s notify test done
  %[1]s config set notify.quiet_hours 22:00-08:00`, a.cmd),
		Args: subcommandArgs,
		RunE: func(c *cobra.Command, _ []string) error { return c.Help() },
	}
	test := &cobra.Command{
		Use:       "test [input|failed|done]",
		Short:     "Send a sample notification now (ignores focus, throttling and quiet hours)",
		Args:      optionalArg,
		ValidArgs: []string{string(notify.Input), string(notify.Failed), string(notify.Done)},
		RunE: func(_ *cobra.Command, args []string) error {
			kind := notify.Input
			if len(args) == 1 {
				kind = notify.Kind(args[0])
			}
			return a.notifyTest(kind)
		},
	}
	status := &cobra.Command{
		Use:   "status",
		Short: "Show the notification settings, the notifier in use and the last notifications",
		Args:  noArgs,
		RunE:  func(*cobra.Command, []string) error { return a.notifyStatus() },
	}
	cmd.AddCommand(test, status)
	return cmd
}

// sampleEvent is what `notify test` sends for kind.
func (a *app) sampleEvent(kind notify.Kind) (notify.Event, error) {
	spec := a.defaultSpec()
	e := notify.Event{Kind: kind, Agent: spec.Name, Account: account.Main, AccountRef: spec.ID + "/" + account.Main, Session: "squadrn-test"}
	switch kind {
	case notify.Input:
		e.Reason, e.Tool = notify.ReasonPermission, "Bash"
	case notify.Failed:
		e.Reason, e.Resets = notify.ReasonRateLimit, a.now().Add(2*time.Hour)
	case notify.Done:
		e.Reason = notify.ReasonTest
	default:
		return e, usagef("notify test takes input, failed or done, not %q", kind)
	}
	e.Cwd, _ = os.Getwd()
	e.Locate(worktreeDirs())
	return e, nil
}

func (a *app) notifyTest(kind notify.Kind) error {
	e, err := a.sampleEvent(kind)
	if err != nil {
		return err
	}
	s := a.cfg.Notify
	if !s.On() {
		a.warnf("notifications are off (%s config set notify.enabled true); sending the test anyway", a.cmd)
	}
	d := delivery{
		Title: notify.Title(e), Body: notify.Body(e, a.cmd, false, a.now()), Kind: kind,
		Sound: s.SoundFor(kind), Group: e.Session, Activate: terminalBundle(),
	}
	h := hookEnv{stdout: a.stdout, stderr: a.stderr, now: a.now}
	tool, err := h.send(notifyStore(a.store.Home), d)
	if err != nil {
		return fmt.Errorf("notification not sent: %w", err)
	}
	a.out("%s sent via %s: %s — %s\n", a.paint(a.colorOut, "32", "✓"), tool, d.Title, d.Body)
	return nil
}

func onOff(b bool) string {
	if b {
		return "on"
	}
	return "off"
}

func (a *app) notifyStatus() error {
	if a.cfgErr != nil {
		return a.cfgErr
	}
	s := a.cfg.Notify
	quiet := s.QuietHours
	if quiet == "" {
		quiet = "none"
	}
	a.out("%-14s %s\n", "notifications", onOff(s.On()))
	a.out("%-14s %s\n", "events", strings.Join(s.EventNames(), ", "))
	a.out("%-14s %s · when %s · quiet hours %s · preview %s\n", "sound", s.SoundName(), s.WhenName(), quiet, onOff(s.Preview))
	n, ok := findNotifier()
	switch {
	case !ok:
		a.out("%-14s none: %s\n", "desktop", n.Note)
	case n.Note != "":
		a.out("%-14s %s (%s)\n%-14s %s\n", "desktop", n.Tool, a.tilde(n.Path), "", n.Note)
	default:
		a.out("%-14s %s (%s)\n", "desktop", n.Tool, a.tilde(n.Path))
	}
	a.out("%-14s %s\n", "method", s.MethodName())
	switch name, protocol := notify.Terminal(os.Getenv); {
	case protocol != "":
		a.out("%-14s %s: shows input and failures itself (outside tmux, method auto or terminal)\n", "terminal", name)
	case name != "":
		a.out("%-14s %s: no notification sequence; the desktop notifier is used\n", "terminal", name)
	default:
		a.out("%-14s unknown; the desktop notifier is used\n", "terminal")
	}
	a.out("%-14s agent state on each pane (grid borders), bell for input and failures; %s next jumps there\n", "tmux", a.cmd)
	a.out("%-14s %s\n", "focus", focusSources())
	recent, _ := notifyStore(a.store.Home).Recent(5)
	if len(recent) == 0 {
		a.out("%-14s none yet (try: %s notify test)\n", "last", a.cmd)
		return nil
	}
	a.out("last\n")
	for _, e := range recent {
		line := fmt.Sprintf("  %s  %-7s %-6s %s", e.At.Local().Format("Jan 02 15:04"), e.Result, e.Kind, e.Title)
		if e.Method != "" {
			line += " (" + e.Method + ")"
		}
		if e.Why != "" {
			line += ": " + e.Why
		}
		a.out("%s\n", line)
	}
	a.out("log: %s\n", a.tilde(notifyStore(a.store.Home).LogPath()))
	return nil
}

// notifier is the desktop notifier found on this machine; Note tells what the user should know
// about it (or, when none is found, what to install).
type notifier struct{ Tool, Path, Note string }

// probeFocus reports whether the user looks at the session. Inside tmux, a pane that is not in
// view is away whatever the terminal app shows.
func probeFocus(ctx context.Context, t paneState, inTmux bool) notify.Focus {
	if inTmux && !t.visible {
		return notify.FocusAway
	}
	if inTmux && t.focused {
		return notify.FocusPane
	}
	front, known := terminalFrontmost(ctx)
	switch {
	case !known:
		return notify.FocusUnknown
	case !front:
		return notify.FocusAway
	case inTmux:
		return notify.FocusPane
	}
	return notify.FocusTerminal
}

// paneState is what tmux says about the pane a hook runs in.
type paneState struct {
	// visible: the active pane of the active window of an attached session; focused: a client
	// showing it has focus (tmux reports it with focus-events on).
	visible, focused bool
	// name is the squadrn grid pane name (@squadrn_name), if any; id the pane (%3), socket the
	// tmux server's socket and tty the pane's terminal.
	name, id, socket, tty string
}

// errNoClient: no tmux client shows the server the pane is in.
var errNoClient = errors.New("no tmux client is attached")

// hookNotifyArgs are the arguments of `hook notify`.
type hookNotifyArgs struct {
	spec     provider.Spec
	terminal bool
	payload  string
}

// parseNotifyArgs reads [--terminal] [--agent <provider>] [<payload>]. --terminal: a synchronous
// hook, which may answer with a terminal sequence; --agent: the CLI whose hook runs (Claude
// Code by default); a JSON argument is the payload (Codex's notify program), else it comes on
// stdin. The rc.3 plugin passed a title (notify 'Done'), which is ignored.
func parseNotifyArgs(args []string) (hookNotifyArgs, bool) {
	n := hookNotifyArgs{spec: hookSpec()}
	positional := 0
	for i := 0; i < len(args); i++ {
		switch a := args[i]; {
		case a == "--terminal":
			n.terminal = true
		case a == "--agent" && i+1 < len(args):
			i++
			spec, ok := provider.Get(args[i])
			if d, _ := provider.DriverOf(spec.ID); !ok || d.Events == nil {
				return n, false
			}
			n.spec = spec
		case strings.HasPrefix(a, "-"):
			return n, false
		default:
			positional++
			if strings.HasPrefix(strings.TrimSpace(a), "{") {
				n.payload = a
			}
		}
	}
	return n, positional <= 1
}

// hookReply is what the hook of spec's CLI must print for raw, "" for nothing.
func hookReply(spec provider.Spec, raw []byte) string {
	d, _ := provider.DriverOf(spec.ID)
	if r, ok := d.Events.(provider.HookReplier); ok {
		return r.HookReply(raw)
	}
	return ""
}
