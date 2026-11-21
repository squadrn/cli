// Package notify decides whether an agent event becomes a notification and what it says: the
// event model, the settings, the policy (dedupe, throttle, quiet hours, focus) and its state and
// log. It never prints and never delivers; cli does (ADR 0012).
package notify

import (
	"fmt"
	"slices"
	"strings"
	"time"
)

// Kind is what an event asks of the user, highest priority first.
type Kind string

const (
	// Input: the agent waits for the user (an approval, an answer, a reply).
	Input Kind = "input"
	// Failed: the turn stopped on an error (usage limit, login, billing, server).
	Failed Kind = "failed"
	// Done: the turn finished.
	Done Kind = "done"
	// Info: anything else the agent reports (logged in, plugin messages).
	Info Kind = "info"
)

// Working is the state of an agent busy with a turn; the other states are the kinds of the
// event it last reported (input, failed, done).
const Working = "working"

// Kinds lists every kind, highest priority first.
var Kinds = []Kind{Input, Failed, Done, Info}

// Reasons refine a kind; the agent's own names are kept where they exist.
const (
	ReasonPermission  = "permission_prompt"
	ReasonIdle        = "idle_prompt"
	ReasonElicitation = "elicitation_dialog"
	ReasonRateLimit   = "rate_limit"
	ReasonLogin       = "authentication_failed"
	ReasonBilling     = "billing_error"
	ReasonTest        = "test"
)

// Event is one thing an agent reported, normalised across providers.
type Event struct {
	Kind   Kind   `json:"kind"`
	Reason string `json:"reason,omitempty"`
	// Agent is the product name (Claude Code, Codex CLI); Account the account's name.
	Agent   string `json:"agent,omitempty"`
	Account string `json:"account,omitempty"`
	// AccountRef is the account as commands take it (claude/work), for hints.
	AccountRef string `json:"account_ref,omitempty"`
	// Session identifies the conversation; Turn the user prompt it answers ("" when unknown).
	Session string `json:"session,omitempty"`
	Turn    string `json:"turn,omitempty"`
	Cwd     string `json:"cwd,omitempty"`
	// Repo and Worktree name where the agent works; Pane is its squadrn grid pane, if any.
	Repo     string `json:"repo,omitempty"`
	Worktree string `json:"worktree,omitempty"`
	Pane     string `json:"pane,omitempty"`
	// Tool is what an approval is for (Bash, Edit), when known.
	Tool string `json:"tool,omitempty"`
	// Resets is when a usage limit ends (zero when unknown).
	Resets time.Time `json:"resets,omitzero"`
	// Message is the agent's own text (assistant reply, notification text): shown only with
	// the preview setting.
	Message string `json:"message,omitempty"`
	// Busy: the agent keeps working (a prompt was submitted, a tool ran, a turn ended with
	// background work left).
	Busy bool `json:"busy,omitempty"`
}

// State is the agent's state after e: working, input, failed or done ("" when e says nothing
// about it, like an info notice).
func (e Event) State() string {
	switch {
	case e.Kind == Input || e.Kind == Failed || e.Kind == Done:
		return string(e.Kind)
	case e.Busy:
		return Working
	}
	return ""
}

// Settings are the [notify] table of squadrn's config.
type Settings struct {
	Enabled    *bool    `toml:"enabled,omitempty"`
	Events     []string `toml:"events,omitempty"`
	Sound      string   `toml:"sound,omitempty"`
	When       string   `toml:"when,omitempty"`
	QuietHours string   `toml:"quiet_hours,omitempty"`
	Preview    bool     `toml:"preview,omitempty"`
	Method     string   `toml:"method,omitempty"`
}

// Setting values.
const (
	SoundInput = "input"
	SoundAll   = "all"
	SoundNone  = "none"

	WhenUnfocused = "unfocused"
	WhenAlways    = "always"

	MethodAuto     = "auto"
	MethodTerminal = "terminal"
	MethodDesktop  = "desktop"
	MethodTmux     = "tmux"
	MethodOff      = "off"
)

var (
	sounds  = []string{SoundInput, SoundAll, SoundNone}
	whens   = []string{WhenUnfocused, WhenAlways}
	methods = []string{MethodAuto, MethodTerminal, MethodDesktop, MethodTmux, MethodOff}
)

// DefaultEvents are the kinds notified unless events names others.
func DefaultEvents() []string { return []string{string(Input), string(Failed), string(Done)} }

// On reports whether notifications are on (the default) and not turned off by method.
func (s Settings) On() bool { return (s.Enabled == nil || *s.Enabled) && s.MethodName() != MethodOff }

func (s Settings) EventNames() []string {
	if len(s.Events) == 0 {
		return DefaultEvents()
	}
	return s.Events
}

func (s Settings) Wants(k Kind) bool { return slices.Contains(s.EventNames(), string(k)) }

func (s Settings) SoundName() string { return or(s.Sound, SoundInput) }
func (s Settings) WhenName() string  { return or(s.When, WhenUnfocused) }

func (s Settings) MethodName() string { return or(s.Method, MethodAuto) }

// Sound reports whether a notification of kind k plays a sound.
func (s Settings) SoundFor(k Kind) bool {
	switch s.SoundName() {
	case SoundAll:
		return true
	case SoundInput:
		return k == Input || k == Failed
	}
	return false
}

func or(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

// Validate reports the first invalid value.
// Choices lists the values a setting accepts (sound, when, method, events); nil for free text.
func Choices(setting string) []string {
	switch setting {
	case "sound":
		return slices.Clone(sounds)
	case "when":
		return slices.Clone(whens)
	case "method":
		return slices.Clone(methods)
	case "events":
		out := make([]string, len(Kinds))
		for i, k := range Kinds {
			out[i] = string(k)
		}
		return out
	}
	return nil
}

func (s Settings) Validate() error {
	for _, e := range s.Events {
		if !slices.Contains(Kinds, Kind(e)) {
			return fmt.Errorf("notify.events: unknown event %q (known: input, failed, done, info)", e)
		}
	}
	for _, c := range []struct {
		key, v string
		ok     []string
	}{
		{"notify.sound", s.Sound, sounds}, {"notify.when", s.When, whens}, {"notify.method", s.Method, methods},
	} {
		if c.v != "" && !slices.Contains(c.ok, c.v) {
			return fmt.Errorf("%s %q is not one of %s", c.key, c.v, strings.Join(c.ok, ", "))
		}
	}
	if s.QuietHours != "" {
		if _, _, err := parseQuietHours(s.QuietHours); err != nil {
			return err
		}
	}
	return nil
}

// parseQuietHours reads "22:00-08:00" as minutes after midnight.
func parseQuietHours(v string) (from, to int, err error) {
	a, b, ok := strings.Cut(v, "-")
	if ok {
		if from, err = clockMinutes(a); err == nil {
			to, err = clockMinutes(b)
		}
	}
	if !ok || err != nil || from == to {
		return 0, 0, fmt.Errorf("notify.quiet_hours %q is not HH:MM-HH:MM", v)
	}
	return from, to, nil
}

func clockMinutes(v string) (int, error) {
	t, err := time.Parse("15:04", strings.TrimSpace(v))
	if err != nil {
		return 0, err
	}
	return t.Hour()*60 + t.Minute(), nil
}

// Quiet reports whether now falls in the quiet hours (local time); the range may wrap midnight.
func (s Settings) Quiet(now time.Time) bool {
	from, to, err := parseQuietHours(s.QuietHours)
	if s.QuietHours == "" || err != nil {
		return false
	}
	now = now.Local()
	m := now.Hour()*60 + now.Minute()
	if from < to {
		return m >= from && m < to
	}
	return m >= from || m < to
}
