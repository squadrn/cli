package claude

import (
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/squadrn/cli/internal/usage"
)

// Status line input (documented: code.claude.com/docs/en/statusline). rate_limits is present
// only for claude.ai subscribers (or a gateway with a spend limit), after the session's first
// API response; each window may be missing, and is dropped once its resets_at has passed.
type statusLineInput struct {
	SessionID  string `json:"session_id"`
	Cwd        string `json:"cwd"`
	RateLimits *struct {
		FiveHour   *rateWindow `json:"five_hour"`
		SevenDay   *rateWindow `json:"seven_day"`
		SpendLimit *struct {
			UsedPercentage *float64 `json:"used_percentage"`
			ResetsAt       *float64 `json:"resets_at"`
			UsedUSD        *float64 `json:"used_usd"`
			LimitUSD       *float64 `json:"limit_usd"`
			Period         string   `json:"period"`
		} `json:"spend_limit"`
	} `json:"rate_limits"`
}

type rateWindow struct {
	UsedPercentage *float64 `json:"used_percentage"`
	ResetsAt       *float64 `json:"resets_at"` // Unix seconds
}

func unix(f *float64) time.Time {
	if f == nil || *f <= 0 {
		return time.Time{}
	}
	return time.Unix(int64(*f), 0)
}

func (r *rateWindow) window() *usage.Window {
	if r == nil || r.UsedPercentage == nil {
		return nil
	}
	return &usage.Window{UsedPct: *r.UsedPercentage, ResetsAt: unix(r.ResetsAt)}
}

// ParseStatusLine turns the status line's JSON input into a usage observation made at now.
func ParseStatusLine(b []byte, now time.Time) (usage.Snapshot, error) {
	var in statusLineInput
	if err := json.Unmarshal(b, &in); err != nil {
		return usage.Snapshot{}, err
	}
	s := usage.Snapshot{ObservedAt: now, Session: in.SessionID, Cwd: in.Cwd, Source: "statusline"}
	if rl := in.RateLimits; rl != nil {
		s.Five, s.Seven = rl.FiveHour.window(), rl.SevenDay.window()
		if sp := rl.SpendLimit; sp != nil && sp.UsedPercentage != nil {
			s.Spend = &usage.Spend{UsedPct: *sp.UsedPercentage, ResetsAt: unix(sp.ResetsAt), UsedUSD: sp.UsedUSD, LimitUSD: sp.LimitUSD, Period: sp.Period}
		}
	}
	return s, nil
}

// StopFailure hook input (documented: hooks reference). error is the matcher value; there is
// no reset time field, so it is read from the message Claude Code shows.
type stopFailureInput struct {
	SessionID            string `json:"session_id"`
	Cwd                  string `json:"cwd"`
	Error                string `json:"error"`
	ErrorDetails         string `json:"error_details"`
	LastAssistantMessage string `json:"last_assistant_message"`
}

// StopFailure is a turn that ended on an API error.
type StopFailure struct {
	Error   string
	Details string
	// Limited: the error is a usage limit (rate_limit), and Observation records it.
	Limited     bool
	Observation usage.Snapshot
}

// ParseStopFailure reads a StopFailure hook input received at now, in loc.
func ParseStopFailure(b []byte, now time.Time, loc *time.Location) (StopFailure, error) {
	var in stopFailureInput
	if err := json.Unmarshal(b, &in); err != nil {
		return StopFailure{}, err
	}
	f := StopFailure{Error: in.Error, Details: in.ErrorDetails, Limited: in.Error == "rate_limit"}
	if f.Limited {
		f.Observation = usage.Snapshot{
			ObservedAt: now, Session: in.SessionID, Cwd: in.Cwd, Source: "stopfailure",
			LimitedAt: now, LimitedSession: in.SessionID, LimitedCwd: in.Cwd,
		}
		if t, ok := ParseResetTime(in.LastAssistantMessage, now, loc); ok {
			f.Observation.ResetEstimate = t
		}
	}
	return f, nil
}

// The limit message ("You've hit your session limit · resets 3:45pm", "… · resets Mon
// 12:00am", sometimes with a zone in parentheses) is not a documented format.
var resetsPattern = regexp.MustCompile(`(?i)\bresets\s+(?:at\s+)?(?:(mon|tue|wed|thu|fri|sat|sun)[a-z]*,?\s+)?(?:(jan|feb|mar|apr|may|jun|jul|aug|sep|oct|nov|dec)[a-z]*\s+(\d{1,2}),?\s+(?:at\s+)?)?(\d{1,2})(?::(\d{2}))?\s*([ap])\.?m\.?(?:\s*\(([^)\s]+)\))?`)

var weekdays = map[string]time.Weekday{"sun": time.Sunday, "mon": time.Monday, "tue": time.Tuesday, "wed": time.Wednesday, "thu": time.Thursday, "fri": time.Friday, "sat": time.Saturday}

var months = map[string]time.Month{"jan": 1, "feb": 2, "mar": 3, "apr": 4, "may": 5, "jun": 6, "jul": 7, "aug": 8, "sep": 9, "oct": 10, "nov": 11, "dec": 12}

// ParseResetTime finds "resets <time>" in msg and returns its next occurrence after now, in loc
// (or the zone named in the message).
func ParseResetTime(msg string, now time.Time, loc *time.Location) (time.Time, bool) {
	m := resetsPattern.FindStringSubmatch(msg)
	if m == nil {
		return time.Time{}, false
	}
	if m[7] != "" {
		if z, err := time.LoadLocation(m[7]); err == nil {
			loc = z
		}
	}
	if loc == nil {
		loc = time.Local
	}
	hour, _ := strconv.Atoi(m[4])
	minute := 0
	if m[5] != "" {
		minute, _ = strconv.Atoi(m[5])
	}
	if hour < 1 || hour > 12 || minute > 59 {
		return time.Time{}, false
	}
	hour %= 12
	if strings.EqualFold(m[6], "p") {
		hour += 12
	}
	local := now.In(loc)
	if m[2] != "" {
		day, _ := strconv.Atoi(m[3])
		mon := months[strings.ToLower(m[2])]
		for _, y := range []int{local.Year(), local.Year() + 1} {
			t := time.Date(y, mon, day, hour, minute, 0, 0, loc)
			if t.Day() != day {
				return time.Time{}, false
			}
			if t.After(now) {
				return t, true
			}
		}
		return time.Time{}, false
	}
	t := time.Date(local.Year(), local.Month(), local.Day(), hour, minute, 0, 0, loc)
	if m[1] != "" {
		want := weekdays[strings.ToLower(m[1])]
		t = t.AddDate(0, 0, (int(want)-int(t.Weekday())+7)%7)
		if !t.After(now) {
			t = t.AddDate(0, 0, 7)
		}
		return t, true
	}
	if !t.After(now) {
		t = t.AddDate(0, 0, 1)
	}
	return t, true
}
