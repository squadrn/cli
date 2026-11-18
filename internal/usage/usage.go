// Package usage keeps what is known about each account's usage limits: the latest observed
// windows (5 hours, 7 days, spend), the last time it hit a limit, and how that ranks accounts
// for a swap. It knows nothing about any provider and never prints.
package usage

import (
	"cmp"
	"slices"
	"time"
)

const (
	// HighFive and HighSeven are the used percentages from which an account counts as high.
	HighFive  = 80.0
	HighSeven = 90.0
	// StaleAfter is how old an observation may be before it no longer describes the account.
	StaleAfter = 6 * time.Hour
)

// Window is one rate-limit window.
type Window struct {
	UsedPct  float64   `json:"used_pct"`
	ResetsAt time.Time `json:"resets_at"`
}

// Spend is a gateway spend limit.
type Spend struct {
	UsedPct  float64   `json:"used_pct"`
	ResetsAt time.Time `json:"resets_at,omitzero"`
	UsedUSD  *float64  `json:"used_usd,omitempty"`
	LimitUSD *float64  `json:"limit_usd,omitempty"`
	Period   string    `json:"period,omitempty"`
}

// Snapshot is what is known about one account. An observation is a Snapshot too: Merge folds
// it into the stored one.
type Snapshot struct {
	Account    string    `json:"account"`
	Five       *Window   `json:"five_hour,omitempty"`
	Seven      *Window   `json:"seven_day,omitempty"`
	Spend      *Spend    `json:"spend,omitempty"`
	ObservedAt time.Time `json:"observed_at,omitzero"`
	// Session and Cwd are where the latest observation came from.
	Session string `json:"session,omitempty"`
	Cwd     string `json:"cwd,omitempty"`
	// LimitedAt is when a session last stopped on a usage limit, in LimitedSession at LimitedCwd.
	LimitedAt      time.Time `json:"limited_at,omitzero"`
	LimitedSession string    `json:"limited_session,omitempty"`
	LimitedCwd     string    `json:"limited_cwd,omitempty"`
	// ResetEstimate is when that limit lifts, as far as is known.
	ResetEstimate time.Time `json:"reset_estimate,omitzero"`
	Source        string    `json:"source,omitempty"`
}

type State string

const (
	Limited   State = "limited"
	High      State = "high"
	OK        State = "ok"
	Reset     State = "reset"
	Stale     State = "stale"
	NoData    State = "nodata"
	Metered   State = "metered"
	NA        State = "na"
	LoggedOut State = "loggedout"
)

func live(w *Window, now time.Time) bool {
	return w != nil && (w.ResetsAt.IsZero() || w.ResetsAt.After(now))
}

func expired(w *Window, now time.Time) bool {
	return w != nil && !w.ResetsAt.IsZero() && !w.ResetsAt.After(now)
}

// LimitResets is when the recorded limit lifts: the estimate, else the soonest full window.
func (s Snapshot) LimitResets() time.Time {
	if !s.ResetEstimate.IsZero() {
		return s.ResetEstimate
	}
	var t time.Time
	for _, w := range []*Window{s.Five, s.Seven} {
		if w != nil && w.UsedPct >= 100 && !w.ResetsAt.IsZero() && (t.IsZero() || w.ResetsAt.Before(t)) {
			t = w.ResetsAt
		}
	}
	return t
}

func (s Snapshot) empty() bool {
	return s.Five == nil && s.Seven == nil && s.Spend == nil && s.LimitedAt.IsZero()
}

// Effective is the snapshot's state at now. A negative age (clock skew) counts as fresh.
func (s Snapshot) Effective(now time.Time) State {
	if s.empty() {
		return NoData
	}
	if !s.LimitedAt.IsZero() {
		r := s.LimitResets()
		if r.IsZero() || r.After(now) {
			return Limited
		}
	}
	for _, w := range []*Window{s.Five, s.Seven} {
		if live(w, now) && w.UsedPct >= 100 {
			return Limited
		}
	}
	if s.Spend != nil && s.Spend.UsedPct >= 100 && (s.Spend.ResetsAt.IsZero() || s.Spend.ResetsAt.After(now)) {
		return Limited
	}
	high := (live(s.Five, now) && s.Five.UsedPct >= HighFive) || (live(s.Seven, now) && s.Seven.UsedPct >= HighSeven)
	wasHigh := !s.LimitedAt.IsZero() ||
		(expired(s.Five, now) && s.Five.UsedPct >= HighFive) || (expired(s.Seven, now) && s.Seven.UsedPct >= HighSeven)
	switch {
	case high:
		if now.Sub(s.ObservedAt) > StaleAfter {
			return Stale
		}
		return High
	case wasHigh:
		return Reset
	case now.Sub(s.ObservedAt) > StaleAfter:
		return Stale
	}
	return OK
}

// Merge folds the observation obs into the stored snapshot old. The newer observation's windows
// win; an older one only fills windows the newer lacks and that have not reset. A limit stays
// recorded until a later observation shows every reported window below 100%.
func Merge(old, obs Snapshot) Snapshot {
	newer, older := obs, old
	if obs.ObservedAt.Before(old.ObservedAt) {
		newer, older = old, obs
	}
	m := newer
	if m.Account == "" {
		m.Account = older.Account
	}
	if m.Five == nil && live(older.Five, newer.ObservedAt) {
		m.Five = older.Five
	}
	if m.Seven == nil && live(older.Seven, newer.ObservedAt) {
		m.Seven = older.Seven
	}
	if m.Spend == nil {
		m.Spend = older.Spend
	}
	if m.Session == "" {
		m.Session, m.Cwd = older.Session, older.Cwd
	}
	if m.Source == "" {
		m.Source = older.Source
	}
	lim := old
	if obs.LimitedAt.After(old.LimitedAt) {
		lim = obs
	}
	m.LimitedAt, m.LimitedSession, m.LimitedCwd, m.ResetEstimate = lim.LimitedAt, lim.LimitedSession, lim.LimitedCwd, lim.ResetEstimate
	if m.LimitedAt.IsZero() || obs.LimitedAt.After(old.LimitedAt) {
		return m
	}
	if obs.ObservedAt.After(m.LimitedAt) && belowLimit(obs) {
		m.LimitedAt, m.LimitedSession, m.LimitedCwd, m.ResetEstimate = time.Time{}, "", "", time.Time{}
	}
	return m
}

func belowLimit(s Snapshot) bool {
	if s.Five == nil && s.Seven == nil {
		return false
	}
	for _, w := range []*Window{s.Five, s.Seven} {
		if w != nil && w.UsedPct >= 100 {
			return false
		}
	}
	return true
}

// SameValues reports whether two snapshots differ only in when they were observed.
func SameValues(a, b Snapshot) bool {
	a.ObservedAt, b.ObservedAt = time.Time{}, time.Time{}
	return equalWindow(a.Five, b.Five) && equalWindow(a.Seven, b.Seven) && equalSpend(a.Spend, b.Spend) &&
		a.Account == b.Account && a.Session == b.Session && a.Cwd == b.Cwd && a.Source == b.Source &&
		a.LimitedAt.Equal(b.LimitedAt) && a.LimitedSession == b.LimitedSession && a.LimitedCwd == b.LimitedCwd &&
		a.ResetEstimate.Equal(b.ResetEstimate)
}

func equalWindow(a, b *Window) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.UsedPct == b.UsedPct && a.ResetsAt.Equal(b.ResetsAt)
}

func equalSpend(a, b *Spend) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.UsedPct == b.UsedPct && a.ResetsAt.Equal(b.ResetsAt) && a.Period == b.Period &&
		equalFloat(a.UsedUSD, b.UsedUSD) && equalFloat(a.LimitUSD, b.LimitUSD)
}

func equalFloat(a, b *float64) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// Candidate is an account considered for a swap.
type Candidate struct {
	Account  string
	Provider string
	Org      string
	LoggedIn bool
	// Metered: billed per use (API key), so it has no subscription limits.
	Metered bool
	// Unsupported: the provider does not report usage.
	Unsupported bool
	Snapshot    Snapshot
}

// State is the candidate's state at now.
func (c Candidate) State(now time.Time) State {
	switch {
	case !c.LoggedIn:
		return LoggedOut
	case c.Unsupported:
		return NA
	case c.Metered:
		return Metered
	}
	return c.Snapshot.Effective(now)
}

type RankOptions struct {
	// SameOrgOnly drops accounts of another organisation than the one swapped from.
	SameOrgOnly bool
}

func group(s State) int {
	switch s {
	case Reset, OK:
		return 0
	case NoData, Stale, NA:
		return 1
	case High:
		return 2
	}
	return 3
}

// Rank orders the accounts a session on from could move to, best first. Logged-out and limited
// accounts, from itself and other providers' accounts are dropped. When none is left, next is
// the earliest time a limited account resets (zero if unknown).
func Rank(cands []Candidate, from string, now time.Time, opts RankOptions) (ranked []Candidate, next time.Time) {
	var src *Candidate
	for i := range cands {
		if cands[i].Account == from {
			src = &cands[i]
		}
	}
	for _, c := range cands {
		if c.Account == from {
			continue
		}
		if src != nil && (c.Provider != src.Provider || (opts.SameOrgOnly && c.Org != src.Org)) {
			continue
		}
		switch c.State(now) {
		case LoggedOut:
			continue
		case Limited:
			if r := c.Snapshot.LimitResets(); !r.IsZero() && (next.IsZero() || r.Before(next)) {
				next = r
			}
			continue
		}
		ranked = append(ranked, c)
	}
	slices.SortStableFunc(ranked, func(a, b Candidate) int {
		if d := cmp.Compare(group(a.State(now)), group(b.State(now))); d != 0 {
			return d
		}
		if d := cmp.Compare(used(a.Snapshot.Five, now), used(b.Snapshot.Five, now)); d != 0 {
			return d
		}
		if d := cmp.Compare(used(a.Snapshot.Seven, now), used(b.Snapshot.Seven, now)); d != 0 {
			return d
		}
		return b.Snapshot.ObservedAt.Compare(a.Snapshot.ObservedAt)
	})
	if len(ranked) > 0 {
		next = time.Time{}
	}
	return ranked, next
}

func used(w *Window, now time.Time) float64 {
	if !live(w, now) {
		return 0
	}
	return w.UsedPct
}
