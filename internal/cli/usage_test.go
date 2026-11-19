package cli

import (
	"strings"
	"testing"
	"time"

	"github.com/squadrn/squadrn/internal/usage"
)

func TestCompactUsage(t *testing.T) {
	now := time.Unix(1000, 0)
	s := usage.Snapshot{Five: &usage.Window{UsedPct: 42.4, ResetsAt: now.Add(time.Hour)}, Seven: &usage.Window{UsedPct: 18}}
	if got := compactUsage("work", s, now); got != "work · 5h 42% · 7d 18%" {
		t.Errorf("got %q", got)
	}
	s.Five.ResetsAt = now
	if got := compactUsage("", s, now); got != "7d 18%" {
		t.Errorf("expired five: %q", got)
	}
	if got := compactUsage("work", usage.Snapshot{}, now); got != "work" {
		t.Errorf("no data: %q", got)
	}
}

func TestUsageFormatting(t *testing.T) {
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.Local)
	if got := clock(now.Add(3*time.Hour+45*time.Minute), now); got != "15:45" {
		t.Errorf("clock today: %q", got)
	}
	if got := clock(now.Add(13*time.Hour), now); got != "Thu 01:00" {
		t.Errorf("clock tomorrow within 24h: %q", got)
	}
	if got := clock(now.Add(48*time.Hour), now); got != "Fri 12:00" {
		t.Errorf("clock later: %q", got)
	}
	for d, want := range map[time.Duration]string{-time.Hour: "just now", 30 * time.Second: "just now", 5 * time.Minute: "5m ago", 3 * time.Hour: "3h ago", 72 * time.Hour: "3d ago"} {
		if got := ago(now.Add(-d), now); got != want {
			t.Errorf("ago(%v) = %q, want %q", d, got, want)
		}
	}
	if ago(time.Time{}, now) != "-" || dash("") != "-" || dash("x") != "x" {
		t.Error("placeholders")
	}
	w := &usage.Window{UsedPct: 85, ResetsAt: now.Add(time.Hour)}
	plain := &app{}
	if got := plain.pct(w, usage.HighFive, now); got != "85%" {
		t.Errorf("pct = %q", got)
	}
	color := &app{colorOut: true}
	if got := color.pct(w, usage.HighFive, now); !strings.Contains(got, "████") || !strings.Contains(got, "\033[33m") {
		t.Errorf("bar = %q", got)
	}
	if got := color.pct(w, usage.HighSeven, now); !strings.Contains(got, "\033[32m") {
		t.Errorf("85%% of the weekly window is not high yet: %q", got)
	}
	if got := color.pct(&usage.Window{UsedPct: 140}, usage.HighFive, now); !strings.Contains(got, "\033[31m█████\033[0m") {
		t.Errorf("full bar = %q", got)
	}
	if plain.pct(nil, usage.HighFive, now) != "-" || resets(nil, now) != "-" || resets(&usage.Window{UsedPct: 1}, now) != "-" {
		t.Error("missing window")
	}
	if got := color.state(usage.Limited); !strings.Contains(got, "\033[31m") {
		t.Errorf("state = %q", got)
	}
	if got := color.state(usage.NoData); got != "nodata" {
		t.Errorf("state = %q", got)
	}
}
