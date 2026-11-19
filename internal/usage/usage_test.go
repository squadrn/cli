package usage

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

var now = time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)

func w(pct float64, resetIn time.Duration) *Window {
	return &Window{UsedPct: pct, ResetsAt: now.Add(resetIn)}
}

func TestEffective(t *testing.T) {
	fresh := now.Add(-time.Minute)
	old := now.Add(-7 * time.Hour)
	tests := []struct {
		name string
		s    Snapshot
		want State
	}{
		{"empty", Snapshot{}, NoData},
		{"observed without windows", Snapshot{ObservedAt: fresh, Session: "s"}, NoData},
		{"ok", Snapshot{Five: w(42, time.Hour), Seven: w(18, 48*time.Hour), ObservedAt: fresh}, OK},
		{"only seven", Snapshot{Seven: w(10, time.Hour), ObservedAt: fresh}, OK},
		{"only five high", Snapshot{Five: w(80, time.Hour), ObservedAt: fresh}, High},
		{"seven high", Snapshot{Five: w(10, time.Hour), Seven: w(90, time.Hour), ObservedAt: fresh}, High},
		{"seven just below", Snapshot{Seven: w(89.9, time.Hour), ObservedAt: fresh}, OK},
		{"five full", Snapshot{Five: w(100, time.Hour), ObservedAt: fresh}, Limited},
		{"seven full and old", Snapshot{Seven: w(100, time.Hour), ObservedAt: old}, Limited},
		{"five full reset just passed", Snapshot{Five: w(100, -time.Second), ObservedAt: fresh}, Reset},
		{"five high reset exactly now", Snapshot{Five: w(85, 0), ObservedAt: fresh}, Reset},
		{"expired low window", Snapshot{Five: w(20, -time.Minute), Seven: w(10, time.Hour), ObservedAt: fresh}, OK},
		{"no reset time", Snapshot{Five: &Window{UsedPct: 50}, ObservedAt: fresh}, OK},
		{"limited with estimate ahead", Snapshot{LimitedAt: fresh, ResetEstimate: now.Add(time.Hour)}, Limited},
		{"limited without any reset time", Snapshot{LimitedAt: fresh}, Limited},
		{"limited falls back to window", Snapshot{LimitedAt: fresh, Five: w(100, 30*time.Minute), ObservedAt: fresh}, Limited},
		{"limit lifted", Snapshot{LimitedAt: old, ResetEstimate: now.Add(-time.Minute), ObservedAt: old}, Reset},
		{"limit lifted by window", Snapshot{LimitedAt: fresh, Five: w(100, -time.Minute), ObservedAt: fresh}, Reset},
		{"stale", Snapshot{Five: w(10, 10*time.Hour), ObservedAt: old}, Stale},
		{"stale high", Snapshot{Five: w(95, 10*time.Hour), ObservedAt: old}, Stale},
		{"stale but reset passed", Snapshot{Five: w(95, -time.Hour), ObservedAt: old}, Reset},
		{"clock skew future observation", Snapshot{Five: w(10, time.Hour), ObservedAt: now.Add(time.Hour)}, OK},
		{"spend over", Snapshot{Spend: &Spend{UsedPct: 120}, ObservedAt: fresh}, Limited},
		{"spend over reset passed", Snapshot{Spend: &Spend{UsedPct: 120, ResetsAt: now.Add(-time.Hour)}, ObservedAt: fresh}, OK},
		{"spend ok", Snapshot{Spend: &Spend{UsedPct: 20}, ObservedAt: fresh}, OK},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.s.Effective(now); got != tt.want {
				t.Fatalf("Effective = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestCandidateState(t *testing.T) {
	snap := Snapshot{Five: w(10, time.Hour), ObservedAt: now}
	tests := []struct {
		c    Candidate
		want State
	}{
		{Candidate{Snapshot: snap}, LoggedOut},
		{Candidate{LoggedIn: true, Unsupported: true, Snapshot: snap}, NA},
		{Candidate{LoggedIn: true, Metered: true, Snapshot: snap}, Metered},
		{Candidate{LoggedIn: true, Snapshot: snap}, OK},
	}
	for _, tt := range tests {
		if got := tt.c.State(now); got != tt.want {
			t.Errorf("%+v: State = %s, want %s", tt.c, got, tt.want)
		}
	}
}

func TestLimitResets(t *testing.T) {
	s := Snapshot{Five: w(100, 2*time.Hour), Seven: w(100, time.Hour)}
	if got := s.LimitResets(); !got.Equal(now.Add(time.Hour)) {
		t.Fatalf("soonest full window: %v", got)
	}
	s.ResetEstimate = now.Add(3 * time.Hour)
	if got := s.LimitResets(); !got.Equal(s.ResetEstimate) {
		t.Fatalf("estimate wins: %v", got)
	}
	if got := (Snapshot{Five: w(50, time.Hour)}).LimitResets(); !got.IsZero() {
		t.Fatalf("no full window: %v", got)
	}
}

func TestMerge(t *testing.T) {
	t0 := now.Add(-10 * time.Minute)
	t1 := now.Add(-5 * time.Minute)
	t.Run("newer wins per window", func(t *testing.T) {
		old := Snapshot{Five: w(10, time.Hour), Seven: w(5, 48*time.Hour), ObservedAt: t0, Session: "s0", Cwd: "/r"}
		obs := Snapshot{Five: w(20, time.Hour), ObservedAt: t1}
		m := Merge(old, obs)
		if m.Five.UsedPct != 20 || m.Seven.UsedPct != 5 || m.Session != "s0" || !m.ObservedAt.Equal(t1) {
			t.Fatalf("merge = %+v", m)
		}
	})
	t.Run("older observation only fills gaps", func(t *testing.T) {
		old := Snapshot{Five: w(30, time.Hour), ObservedAt: t1}
		obs := Snapshot{Five: w(10, time.Hour), Seven: w(7, time.Hour), ObservedAt: t0}
		m := Merge(old, obs)
		if m.Five.UsedPct != 30 || m.Seven.UsedPct != 7 || !m.ObservedAt.Equal(t1) {
			t.Fatalf("merge = %+v", m)
		}
	})
	t.Run("reset window is not carried over", func(t *testing.T) {
		old := Snapshot{Five: &Window{UsedPct: 90, ResetsAt: t0.Add(time.Minute)}, ObservedAt: t0}
		m := Merge(old, Snapshot{ObservedAt: t1})
		if m.Five != nil {
			t.Fatalf("five kept: %+v", m.Five)
		}
	})
	t.Run("spend kept", func(t *testing.T) {
		m := Merge(Snapshot{Spend: &Spend{UsedPct: 5}, ObservedAt: t0}, Snapshot{ObservedAt: t1})
		if m.Spend == nil {
			t.Fatal("spend dropped")
		}
	})
	limited := Snapshot{Five: w(100, time.Hour), ObservedAt: t0, LimitedAt: t0, LimitedSession: "s", LimitedCwd: "/r", ResetEstimate: now.Add(time.Hour)}
	t.Run("limit recorded", func(t *testing.T) {
		m := Merge(Snapshot{Five: w(90, time.Hour), ObservedAt: now.Add(-time.Hour)}, limited)
		if !m.LimitedAt.Equal(t0) || m.LimitedSession != "s" || m.Five.UsedPct != 100 {
			t.Fatalf("merge = %+v", m)
		}
	})
	t.Run("limit kept while full", func(t *testing.T) {
		m := Merge(limited, Snapshot{Five: w(100, time.Hour), ObservedAt: t1})
		if m.LimitedAt.IsZero() {
			t.Fatal("limit cleared")
		}
	})
	t.Run("limit kept when no window reported", func(t *testing.T) {
		m := Merge(limited, Snapshot{ObservedAt: t1, Session: "x"})
		if m.LimitedAt.IsZero() {
			t.Fatal("limit cleared")
		}
	})
	t.Run("limit cleared below 100", func(t *testing.T) {
		m := Merge(limited, Snapshot{Five: w(3, 5*time.Hour), ObservedAt: t1})
		if !m.LimitedAt.IsZero() || m.LimitedSession != "" || !m.ResetEstimate.IsZero() {
			t.Fatalf("limit kept: %+v", m)
		}
	})
	t.Run("earlier observation does not clear", func(t *testing.T) {
		m := Merge(limited, Snapshot{Five: w(3, 5*time.Hour), ObservedAt: t0.Add(-time.Minute)})
		if m.LimitedAt.IsZero() {
			t.Fatal("limit cleared by an older observation")
		}
	})
	t.Run("older limit ignored", func(t *testing.T) {
		m := Merge(limited, Snapshot{LimitedAt: t0.Add(-time.Hour), LimitedSession: "old"})
		if m.LimitedSession != "s" {
			t.Fatalf("merge = %+v", m)
		}
	})
}

func TestSameValues(t *testing.T) {
	a := Snapshot{Five: w(1, time.Hour), Spend: &Spend{UsedPct: 1, UsedUSD: new(1.5)}, ObservedAt: now}
	b := a
	b.ObservedAt = now.Add(time.Minute)
	if !SameValues(a, b) {
		t.Fatal("observed time alone counts as a change")
	}
	for _, mut := range []func(*Snapshot){
		func(s *Snapshot) { s.Five = w(2, time.Hour) },
		func(s *Snapshot) { s.Five = nil },
		func(s *Snapshot) { s.Seven = w(1, time.Hour) },
		func(s *Snapshot) { s.Spend = &Spend{UsedPct: 1, UsedUSD: new(2.0)} },
		func(s *Snapshot) { s.Spend = &Spend{UsedPct: 1} },
		func(s *Snapshot) { s.LimitedAt = now },
		func(s *Snapshot) { s.Session = "x" },
	} {
		c := a
		mut(&c)
		if SameValues(a, c) {
			t.Errorf("change not seen: %+v", c)
		}
	}
}

func TestRank(t *testing.T) {
	mk := func(name string, s Snapshot) Candidate {
		return Candidate{Account: name, Provider: "claude", LoggedIn: true, Snapshot: s}
	}
	fresh := now.Add(-time.Minute)
	cands := []Candidate{
		mk("claude/from", Snapshot{LimitedAt: fresh, ResetEstimate: now.Add(2 * time.Hour)}),
		mk("claude/high", Snapshot{Five: w(85, time.Hour), ObservedAt: fresh}),
		mk("claude/nodata", Snapshot{}),
		mk("claude/ok50", Snapshot{Five: w(50, time.Hour), ObservedAt: fresh}),
		mk("claude/ok10", Snapshot{Five: w(10, time.Hour), ObservedAt: fresh}),
		mk("claude/ok10b", Snapshot{Five: w(10, time.Hour), Seven: w(5, time.Hour), ObservedAt: fresh}),
		mk("claude/ok10c", Snapshot{Five: w(10, time.Hour), Seven: w(5, time.Hour), ObservedAt: now}),
		mk("claude/limited", Snapshot{Five: w(100, 30*time.Minute), ObservedAt: fresh}),
		{Account: "claude/out", Provider: "claude"},
		{Account: "codex/x", Provider: "codex", LoggedIn: true},
		mk("claude/metered", Snapshot{}),
	}
	cands[len(cands)-1].Metered = true
	names := func(cs []Candidate) []string {
		var out []string
		for _, c := range cs {
			out = append(out, c.Account)
		}
		return out
	}
	got, next := Rank(cands, "claude/from", now)
	want := []string{"claude/ok10", "claude/ok10c", "claude/ok10b", "claude/ok50", "claude/nodata", "claude/high", "claude/metered"}
	if g := names(got); !equal(g, want) || !next.IsZero() {
		t.Fatalf("Rank = %v next %v, want %v", g, next, want)
	}
	got, _ = Rank(cands, "", now)
	if g := names(got); !contains(g, "codex/x") || contains(g, "claude/from") {
		t.Fatalf("no source: %v", g)
	}
	got, next = Rank(cands[:1:1], "", now)
	if len(got) != 0 || !next.Equal(now.Add(2*time.Hour)) {
		t.Fatalf("all limited: %v next %v", names(got), next)
	}
	only := []Candidate{cands[0], cands[7]}
	if got, next = Rank(only, "claude/from", now); len(got) != 0 || !next.Equal(now.Add(30*time.Minute)) {
		t.Fatalf("earliest reset: %v next %v", names(got), next)
	}
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func contains(s []string, x string) bool {
	for _, v := range s {
		if v == x {
			return true
		}
	}
	return false
}

func TestStore(t *testing.T) {
	s := Store{Dir: filepath.Join(t.TempDir(), "usage")}
	if _, ok, err := s.Load("claude", "work"); ok || err != nil {
		t.Fatalf("empty store: ok=%v err=%v", ok, err)
	}
	obs := Snapshot{Five: w(10, time.Hour), ObservedAt: now}
	if _, wrote, err := s.Record("claude", "work", obs); !wrote || err != nil {
		t.Fatalf("first record: wrote=%v err=%v", wrote, err)
	}
	if runtime.GOOS != "windows" {
		fi, err := os.Stat(filepath.Join(s.Dir, "claude-work.json"))
		if err != nil || fi.Mode().Perm() != 0o600 {
			t.Fatalf("file mode: %v %v", fi, err)
		}
	}
	obs.ObservedAt = now.Add(30 * time.Second)
	if _, wrote, _ := s.Record("claude", "work", obs); wrote {
		t.Fatal("unchanged observation written within a minute")
	}
	obs.ObservedAt = now.Add(WriteEvery)
	if _, wrote, _ := s.Record("claude", "work", obs); !wrote {
		t.Fatal("unchanged observation not refreshed after a minute")
	}
	obs.Five = w(11, time.Hour)
	obs.ObservedAt = now.Add(WriteEvery + time.Second)
	snap, wrote, err := s.Record("claude", "work", obs)
	if !wrote || err != nil || snap.Five.UsedPct != 11 {
		t.Fatalf("changed value: wrote=%v err=%v snap=%+v", wrote, err, snap)
	}
	got, ok, err := s.Load("claude", "work")
	if !ok || err != nil || got.Five.UsedPct != 11 {
		t.Fatalf("load: %+v ok=%v err=%v", got, ok, err)
	}

	if err := os.WriteFile(filepath.Join(s.Dir, "claude-bad.json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Load("claude", "bad"); err == nil {
		t.Fatal("corrupt file loaded")
	}
	if _, wrote, err := s.Record("claude", "bad", obs); !wrote || err != nil {
		t.Fatalf("corrupt file not replaced: %v", err)
	}

	if err := s.Rename("claude", "work", "job"); err != nil {
		t.Fatal(err)
	}
	if got, ok, _ := s.Load("claude", "job"); !ok || got.Five.UsedPct != 11 {
		t.Fatalf("renamed snapshot: %+v ok=%v", got, ok)
	}
	if err := s.Rename("claude", "ghost", "other"); err != nil {
		t.Fatalf("rename of nothing: %v", err)
	}
	if err := s.Rename("claude", "job", "work"); err != nil {
		t.Fatal(err)
	}
	if err := s.Remove("claude", "work"); err != nil {
		t.Fatal(err)
	}
	if err := s.Remove("claude", "work"); err != nil {
		t.Fatalf("second remove: %v", err)
	}
	if _, ok, _ := s.Load("claude", "work"); ok {
		t.Fatal("removed snapshot still loads")
	}
}

func TestStoreErrors(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	s := Store{Dir: filepath.Join(file, "usage")}
	if err := s.Save("claude", "x", Snapshot{}); err == nil {
		t.Fatal("save under a file succeeded")
	}
	if err := os.Mkdir(filepath.Join(dir, "claude-dir.json"), 0o700); err != nil {
		t.Fatal(err)
	}
	s = Store{Dir: dir}
	if _, _, err := s.Load("claude", "dir"); err == nil {
		t.Fatal("loading a directory succeeded")
	}
	if _, _, err := s.Record("claude", "dir", Snapshot{}); err == nil {
		t.Fatal("record over a directory succeeded")
	}
}
