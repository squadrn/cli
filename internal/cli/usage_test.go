package cli

import (
	"strings"
	"testing"
	"time"

	"github.com/squadrn/squadrn/internal/provider"
	"github.com/squadrn/squadrn/internal/usage"
)

func TestUnwrapStatusLine(t *testing.T) {
	tests := []struct {
		cmd, old string
		ours     bool
	}{
		{"/x/squadrn hook statusline", "", true},
		{"'/a b/squadrn' hook statusline --then 'echo hi'", "echo hi", true},
		{"~/.local/share/squadrn/bin/squadrn.exe hook statusline --then 'x'", "x", true},
		{"/x/squadrn hook statusline --then 'a'\\''b'", "a'b", true},
		{"/x/squadrn hook statusline --then 'unterminated", "", false},
		{"/x/squadrn hook statusline --then 'a' extra", "", false},
		{"/x/squadrn hook statusline --other", "", false},
		{"/x/squadrn hook statuslinex", "", false},
		{"~/statusline.sh", "", false},
		{"'unterminated hook statusline", "", false},
		{"", "", false},
	}
	for _, tt := range tests {
		old, ours := unwrapStatusLine(tt.cmd)
		if old != tt.old || ours != tt.ours {
			t.Errorf("%q: got %q %v, want %q %v", tt.cmd, old, ours, tt.old, tt.ours)
		}
	}
}

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
	if got := plain.pct(w, now); got != "85%" {
		t.Errorf("pct = %q", got)
	}
	color := &app{colorOut: true}
	if got := color.pct(w, now); !strings.Contains(got, "████") || !strings.Contains(got, "\033[33m") {
		t.Errorf("bar = %q", got)
	}
	if got := color.pct(&usage.Window{UsedPct: 140}, now); !strings.Contains(got, "\033[31m█████\033[0m") {
		t.Errorf("full bar = %q", got)
	}
	if plain.pct(nil, now) != "-" || resets(nil, now) != "-" || resets(&usage.Window{UsedPct: 1}, now) != "-" {
		t.Error("missing window")
	}
	if got := color.state(usage.Limited); !strings.Contains(got, "\033[31m") {
		t.Errorf("state = %q", got)
	}
	if got := color.state(usage.NoData); got != "nodata" {
		t.Errorf("state = %q", got)
	}
}

func TestMetered(t *testing.T) {
	tests := []struct {
		s    provider.AuthStatus
		want bool
	}{
		{provider.AuthStatus{LoggedIn: true, AuthMethod: "api_key"}, true},
		{provider.AuthStatus{LoggedIn: true, AuthMethod: "claude.ai", SubscriptionType: "max"}, false},
		{provider.AuthStatus{LoggedIn: true, AuthMethod: "api_key", SubscriptionType: "pro"}, false},
		{provider.AuthStatus{LoggedIn: true}, false},
		{provider.AuthStatus{AuthMethod: "api_key"}, false},
	}
	for _, tt := range tests {
		if got := metered(tt.s); got != tt.want {
			t.Errorf("%+v: %v", tt.s, got)
		}
	}
}

func TestWithin(t *testing.T) {
	dir := t.TempDir()
	tests := []struct {
		dir, base string
		want      bool
	}{
		{dir, dir, true},
		{dir + "/sub", dir, true},
		{dir, dir + "/sub", false},
		{"/etc", "/", false},
		{"", dir, false},
		{dir + "x", dir, false},
	}
	for _, tt := range tests {
		if got := within(tt.dir, tt.base); got != tt.want {
			t.Errorf("within(%q, %q) = %v", tt.dir, tt.base, got)
		}
	}
}
