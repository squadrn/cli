package notify

import "time"

// Focus is what is known about whether the user is looking at the session.
type Focus int

const (
	// FocusUnknown: nothing is known; nothing is suppressed.
	FocusUnknown Focus = iota
	// FocusAway: the session is not in view (another tmux window, another app).
	FocusAway
	// FocusTerminal: the terminal app running the session is frontmost, but which tab or pane
	// is not known. Only done and info are suppressed.
	FocusTerminal
	// FocusPane: the session's tmux pane is the active pane of a focused client.
	FocusPane
)

func (f Focus) String() string {
	return [...]string{"unknown", "away", "terminal frontmost", "pane in view"}[f]
}

// Throttle is the shortest gap between two notifications of one session, except input.
const Throttle = 30 * time.Second

// keep is how long a session's last notification is remembered.
const keep = 24 * time.Hour

// Decision is the policy's verdict on one event.
type Decision struct {
	Deliver bool
	// Reason says why an event is skipped ("" when delivered).
	Reason string
	Sound  bool
}

// Decide applies the settings and the session's history to e. It records a delivered event in
// st, so the caller saves st afterwards.
func Decide(e Event, s Settings, st *State, now time.Time, f Focus) Decision {
	skip := func(why string) Decision { return Decision{Reason: why} }
	switch {
	case e.Kind == "":
		return skip("not a notification")
	case !s.On():
		return skip("notifications are off")
	case !s.Wants(e.Kind):
		return skip(string(e.Kind) + " events are off")
	case s.Quiet(now):
		return skip("quiet hours")
	}
	last, seen := st.Sessions[e.Session]
	if e.Session != "" && seen && now.Sub(last.At) < keep {
		switch {
		case e.Kind == Input && e.Reason == ReasonIdle && last.Kind == Done && (e.Turn == "" || last.Turn == "" || e.Turn == last.Turn):
			return skip("the turn's done notification was shown")
		case e.Kind == last.Kind && e.Reason == last.Reason && e.Turn != "" && e.Turn == last.Turn:
			return skip("already notified")
		case e.Kind != Input && now.Sub(last.At) < Throttle:
			return skip("throttled")
		}
	}
	if s.WhenName() == WhenUnfocused {
		switch {
		case f == FocusPane:
			return skip("the session is in view")
		case f == FocusTerminal && (e.Kind == Done || e.Kind == Info):
			return skip("the terminal is frontmost")
		}
	}
	st.record(e, now)
	return Decision{Deliver: true, Sound: s.SoundFor(e.Kind)}
}
