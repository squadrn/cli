package notify

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func ptr[T any](v T) *T { return &v }

func at(h, m int) time.Time { return time.Date(2026, 11, 20, h, m, 0, 0, time.Local) }

func TestSettingsDefaults(t *testing.T) {
	var s Settings
	if !s.On() || s.SoundName() != SoundInput || s.WhenName() != WhenUnfocused || s.MethodName() != MethodAuto {
		t.Fatalf("defaults: %+v", s)
	}
	for k, want := range map[Kind]bool{Input: true, Failed: true, Done: true, Info: false} {
		if s.Wants(k) != want {
			t.Errorf("Wants(%s) = %v", k, !want)
		}
	}
	for k, want := range map[Kind]bool{Input: true, Failed: true, Done: false, Info: false} {
		if s.SoundFor(k) != want {
			t.Errorf("SoundFor(%s) = %v", k, !want)
		}
	}
	if (Settings{Sound: SoundAll}).SoundFor(Done) != true || (Settings{Sound: SoundNone}).SoundFor(Input) {
		t.Error("sound all/none")
	}
	if (Settings{Enabled: ptr(false)}).On() || (Settings{Method: MethodOff}).On() {
		t.Error("off")
	}
	if got := (Settings{Events: []string{"info"}}).EventNames(); len(got) != 1 || got[0] != "info" {
		t.Errorf("events = %v", got)
	}
}

func TestValidate(t *testing.T) {
	for _, tc := range []struct {
		s    Settings
		want string
	}{
		{Settings{}, ""},
		{Settings{Events: []string{"input", "info"}, Sound: "all", When: "always", Method: "desktop", QuietHours: "22:00-08:00"}, ""},
		{Settings{Events: []string{"bogus"}}, `unknown event "bogus"`},
		{Settings{Sound: "loud"}, `notify.sound "loud" is not one of input, all, none`},
		{Settings{When: "never"}, `notify.when "never"`},
		{Settings{Method: "pigeon"}, `notify.method "pigeon"`},
		{Settings{QuietHours: "22-08"}, `notify.quiet_hours "22-08" is not HH:MM-HH:MM`},
		{Settings{QuietHours: "08:00-08:00"}, "not HH:MM-HH:MM"},
		{Settings{QuietHours: "25:00-08:00"}, "not HH:MM-HH:MM"},
	} {
		err := tc.s.Validate()
		switch {
		case tc.want == "" && err != nil:
			t.Errorf("%+v: %v", tc.s, err)
		case tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)):
			t.Errorf("%+v: got %v, want %q", tc.s, err, tc.want)
		}
	}
}

func TestQuiet(t *testing.T) {
	night := Settings{QuietHours: "22:00-08:00"}
	day := Settings{QuietHours: "12:00-13:30"}
	for _, tc := range []struct {
		s    Settings
		now  time.Time
		want bool
	}{
		{night, at(23, 0), true},
		{night, at(2, 0), true},
		{night, at(8, 0), false},
		{night, at(21, 59), false},
		{night, at(22, 0), true},
		{day, at(12, 30), true},
		{day, at(13, 30), false},
		{day, at(11, 0), false},
		{Settings{}, at(3, 0), false},
		{Settings{QuietHours: "bad"}, at(3, 0), false},
	} {
		if got := tc.s.Quiet(tc.now); got != tc.want {
			t.Errorf("%s at %s = %v", tc.s.QuietHours, tc.now.Format("15:04"), got)
		}
	}
}

func TestDecide(t *testing.T) {
	now := at(15, 0)
	recent := func(k Kind, reason, turn string, ago time.Duration) *State {
		return &State{Sessions: map[string]Last{"s": {Kind: k, Reason: reason, Turn: turn, At: now.Add(-ago)}}}
	}
	done := Event{Kind: Done, Session: "s", Turn: "t2"}
	input := Event{Kind: Input, Reason: ReasonPermission, Session: "s", Turn: "t2"}
	idle := Event{Kind: Input, Reason: ReasonIdle, Session: "s", Turn: "t1"}
	failed := Event{Kind: Failed, Reason: ReasonRateLimit, Session: "s", Turn: "t2"}
	for _, tc := range []struct {
		name  string
		e     Event
		s     Settings
		st    *State
		focus Focus
		why   string
		sound bool
	}{
		{"no kind", Event{Session: "s"}, Settings{}, &State{}, FocusUnknown, "not a notification", false},
		{"off", done, Settings{Enabled: ptr(false)}, &State{}, FocusUnknown, "notifications are off", false},
		{"method off", done, Settings{Method: MethodOff}, &State{}, FocusUnknown, "notifications are off", false},
		{"info off by default", Event{Kind: Info, Session: "s"}, Settings{}, &State{}, FocusUnknown, "info events are off", false},
		{"quiet hours", input, Settings{QuietHours: "14:00-16:00"}, &State{}, FocusUnknown, "quiet hours", false},
		{"done", done, Settings{}, &State{}, FocusUnknown, "", false},
		{"input sounds", input, Settings{}, &State{}, FocusAway, "", true},
		{"failed sounds", failed, Settings{}, &State{}, FocusUnknown, "", true},
		{"idle after done of the turn", idle, Settings{}, recent(Done, "", "t1", time.Minute), FocusUnknown, "the turn's done notification was shown", false},
		{"idle after done, turn unknown", Event{Kind: Input, Reason: ReasonIdle, Session: "s"}, Settings{}, recent(Done, "", "t1", time.Minute), FocusUnknown, "the turn's done notification was shown", false},
		{"idle of a later turn", Event{Kind: Input, Reason: ReasonIdle, Session: "s", Turn: "t3"}, Settings{}, recent(Done, "", "t1", time.Minute), FocusUnknown, "", true},
		{"same event twice", done, Settings{}, recent(Done, "", "t2", 2*time.Minute), FocusUnknown, "already notified", false},
		{"throttled", done, Settings{}, recent(Input, ReasonPermission, "t2", 10*time.Second), FocusUnknown, "throttled", false},
		{"input is never throttled", input, Settings{}, recent(Done, "", "t1", time.Second), FocusUnknown, "", true},
		{"after the throttle", done, Settings{}, recent(Input, ReasonPermission, "t2", Throttle), FocusUnknown, "", false},
		{"another session", done, Settings{}, &State{Sessions: map[string]Last{"x": {Kind: Done, Turn: "t2", At: now}}}, FocusUnknown, "", false},
		{"stale history", done, Settings{}, recent(Done, "", "t2", 25*time.Hour), FocusUnknown, "", false},
		{"pane in view", input, Settings{}, &State{}, FocusPane, "the session is in view", false},
		{"pane in view, always", input, Settings{When: WhenAlways}, &State{}, FocusPane, "", true},
		{"terminal frontmost skips done", done, Settings{}, &State{}, FocusTerminal, "the terminal is frontmost", false},
		{"terminal frontmost keeps input", input, Settings{}, &State{}, FocusTerminal, "", true},
		{"terminal frontmost keeps failed", failed, Settings{}, &State{}, FocusTerminal, "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := Decide(tc.e, tc.s, tc.st, now, tc.focus)
			if d.Deliver != (tc.why == "") || d.Reason != tc.why || d.Sound != tc.sound {
				t.Fatalf("got %+v, want reason %q sound %v", d, tc.why, tc.sound)
			}
			last, ok := tc.st.Sessions[tc.e.Session]
			if recorded := ok && last.At.Equal(now); recorded != d.Deliver && tc.e.Session != "" {
				t.Errorf("recorded = %v, delivered = %v", recorded, d.Deliver)
			}
		})
	}
}

func TestDecideKeepsReplaceID(t *testing.T) {
	now := at(15, 0)
	st := &State{Sessions: map[string]Last{"s": {Kind: Done, At: now.Add(-time.Hour), ReplaceID: "42"}}}
	if d := Decide(Event{Kind: Input, Session: "s"}, Settings{}, st, now, FocusUnknown); !d.Deliver {
		t.Fatal(d)
	}
	if st.Sessions["s"].ReplaceID != "42" || st.Sessions["s"].Kind != Input {
		t.Errorf("state = %+v", st.Sessions["s"])
	}
	// An event without a session is delivered and not remembered.
	if d := Decide(Event{Kind: Done}, Settings{}, st, now, FocusUnknown); !d.Deliver || len(st.Sessions) != 1 {
		t.Errorf("%+v %v", d, st.Sessions)
	}
}

func TestFocusString(t *testing.T) {
	if FocusUnknown.String() != "unknown" || FocusPane.String() != "pane in view" {
		t.Error(FocusUnknown, FocusPane)
	}
}

func TestContent(t *testing.T) {
	now := at(14, 0)
	base := Event{Agent: "Claude Code", Account: "work", AccountRef: "claude/work", Repo: "api", Worktree: "fix-auth"}
	with := func(f func(*Event)) Event { e := base; f(&e); return e }
	for _, tc := range []struct {
		name, title, body string
		e                 Event
		preview           bool
	}{
		{
			"approval with tool", "Claude Code · work · api/fix-auth", "Needs approval: Bash",
			with(func(e *Event) { e.Kind, e.Reason, e.Tool = Input, ReasonPermission, "Bash" }), false,
		},
		{
			"approval", "Claude Code · work · api/fix-auth", "Needs your approval",
			with(func(e *Event) { e.Kind, e.Reason = Input, ReasonPermission }), false,
		},
		{
			"idle", "Claude Code · work · api/fix-auth", "Waiting for your reply",
			with(func(e *Event) { e.Kind, e.Reason = Input, ReasonIdle }), false,
		},
		{
			"elicitation", "Claude Code · work · api/fix-auth", "Needs your input",
			with(func(e *Event) { e.Kind, e.Reason = Input, "elicitation_url_dialog" }), false,
		},
		{
			"done hides the reply", "Claude Code · work · api/fix-auth", "Done",
			with(func(e *Event) { e.Kind, e.Message = Done, "secret plan" }), false,
		},
		{
			"done with preview", "Claude Code · work · api/fix-auth", "Done · All tests pass",
			with(func(e *Event) { e.Kind, e.Message = Done, "All   tests\npass" }), true,
		},
		{
			"usage limit", "Claude Code · work · api/fix-auth", "Usage limit · resets 15:45 · srn swap",
			with(func(e *Event) { e.Kind, e.Reason, e.Resets = Failed, ReasonRateLimit, at(15, 45) }), false,
		},
		{
			"usage limit tomorrow", "Claude Code · work · api/fix-auth", "Usage limit · resets " + at(15, 45).AddDate(0, 0, 1).Format("Mon") + " 15:45 · srn swap",
			with(func(e *Event) { e.Kind, e.Reason, e.Resets = Failed, ReasonRateLimit, at(15, 45).AddDate(0, 0, 1) }), false,
		},
		{
			"usage limit, reset unknown", "Claude Code · work · api/fix-auth", "Usage limit · srn swap",
			with(func(e *Event) { e.Kind, e.Reason = Failed, ReasonRateLimit }), true,
		},
		{
			"login", "Claude Code · work · api/fix-auth", "Login expired · srn account login claude/work",
			with(func(e *Event) { e.Kind, e.Reason = Failed, ReasonLogin }), false,
		},
		{
			"org", "Claude Code · work · api/fix-auth", "Organization not allowed · srn account login claude/work",
			with(func(e *Event) { e.Kind, e.Reason = Failed, "oauth_org_not_allowed" }), false,
		},
		{
			"hold", "Claude Code · work · api/fix-auth", "Account on hold · srn account login claude/work",
			with(func(e *Event) { e.Kind, e.Reason = Failed, "account_on_hold" }), false,
		},
		{
			"billing", "Claude Code · work · api/fix-auth", "Billing problem · check the account's plan",
			with(func(e *Event) { e.Kind, e.Reason = Failed, ReasonBilling }), false,
		},
		{
			"server error", "Claude Code · work · api/fix-auth", "Stopped: server error",
			with(func(e *Event) { e.Kind, e.Reason, e.Message = Failed, "server_error", "boom" }), true,
		},
		{
			"unknown error", "Claude Code · work · api/fix-auth", "Stopped on an error",
			with(func(e *Event) { e.Kind, e.Reason = Failed, "unknown" }), false,
		},
		{
			"info message", "Claude Code · work · api/fix-auth", "Logged in",
			with(func(e *Event) { e.Kind, e.Reason, e.Message = Info, "auth_success", "Logged in" }), false,
		},
		{
			"info without message", "Claude Code · work · api/fix-auth", "auth success",
			with(func(e *Event) { e.Kind, e.Reason = Info, "auth_success" }), false,
		},
		{
			"grid pane wins", "Claude Code · work · api/pane-2", "Done",
			with(func(e *Event) { e.Kind, e.Pane = Done, "pane-2" }), false,
		},
		{
			"repo only", "Claude Code · work · api", "Done",
			with(func(e *Event) { e.Kind, e.Worktree = Done, "api" }), false,
		},
		{"nothing known", "Squadrn", "Done", Event{Kind: Done}, false},
		{"worktree without repo", "fix-auth", "Done", Event{Kind: Done, Worktree: "fix-auth"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := Title(tc.e); got != tc.title {
				t.Errorf("title = %q, want %q", got, tc.title)
			}
			if got := Body(tc.e, "srn", tc.preview, now); got != tc.body {
				t.Errorf("body = %q, want %q", got, tc.body)
			}
		})
	}
}

func TestClip(t *testing.T) {
	if got := clip(strings.Repeat("é", 200), 10); got != strings.Repeat("é", 9)+"…" {
		t.Errorf("clip = %q", got)
	}
}

func TestToolFromMessage(t *testing.T) {
	for msg, want := range map[string]string{
		"Claude needs your permission to use Bash":              "Bash",
		"Claude needs your permission to use mcp__github__push": "mcp__github__push",
		"Claude is waiting for your input":                      "",
	} {
		if got := ToolFromMessage(msg); got != want {
			t.Errorf("%q: %q", msg, got)
		}
	}
}

func TestLocate(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "api")
	sub := filepath.Join(repo, "internal", "x")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	dirs := []string{".claude/worktrees", ".codex/worktrees"}
	for _, tc := range []struct{ cwd, repo, wt string }{
		{filepath.Join(repo, ".claude", "worktrees", "fix-auth", "cmd"), "api", "fix-auth"},
		{filepath.Join(repo, ".codex", "worktrees", "spike"), "api", "spike"},
		{sub, "api", ""},
		{root, filepath.Base(root), ""},
		{"", "", ""},
	} {
		e := Event{Cwd: tc.cwd}
		e.Locate(dirs)
		if e.Repo != tc.repo || e.Worktree != tc.wt {
			t.Errorf("%s: %q %q", tc.cwd, e.Repo, e.Worktree)
		}
	}
}

func TestStore(t *testing.T) {
	s := Store{Dir: filepath.Join(t.TempDir(), "state")}
	now := at(10, 0)
	if st := s.Load(); len(st.Sessions) != 0 {
		t.Fatal(st)
	}
	err := s.Update(now, func(st *State) {
		st.record(Event{Kind: Done, Session: "new", Turn: "t"}, now)
		st.Sessions["old"] = Last{Kind: Done, At: now.Add(-25 * time.Hour)}
	})
	if err != nil {
		t.Fatal(err)
	}
	st := s.Load()
	if _, ok := st.Sessions["old"]; ok || st.Sessions["new"].Turn != "t" {
		t.Errorf("state = %+v", st)
	}
	if err := os.WriteFile(filepath.Join(s.Dir, "notify.json"), []byte("{broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	if st := s.Load(); st.Sessions == nil || len(st.Sessions) != 0 {
		t.Errorf("broken state = %+v", st)
	}

	if got, err := s.Recent(5); err != nil || got != nil {
		t.Fatalf("no log: %v %v", got, err)
	}
	for i := range 7 {
		s.Log(Entry{At: now.Add(time.Duration(i) * time.Minute), Kind: Done, Result: "sent", Title: "t"})
	}
	got, err := s.Recent(5)
	if err != nil || len(got) != 5 || !got[4].At.Equal(now.Add(6*time.Minute)) {
		t.Fatalf("recent = %+v %v", got, err)
	}
	if err := os.WriteFile(s.LogPath(), make([]byte, maxLog+1), 0o600); err != nil {
		t.Fatal(err)
	}
	s.Log(Entry{At: now, Kind: Input, Result: "skipped", Why: "throttled"})
	if fi, err := os.Stat(s.LogPath() + ".1"); err != nil || fi.Size() <= maxLog {
		t.Errorf("not rotated: %v", err)
	}
	if got, _ := s.Recent(5); len(got) != 1 || got[0].Why != "throttled" {
		t.Errorf("after rotation = %+v", got)
	}
}

func TestRoute(t *testing.T) {
	for _, tc := range []struct {
		method         string
		k              Kind
		terminal, tmux bool
		want           Channels
	}{
		{"", Done, false, false, Channels{Desktop: true}},
		{"", Done, true, false, Channels{Terminal: true}},
		{"", Input, true, true, Channels{Desktop: true, Bell: true}},
		{"", Done, false, true, Channels{Desktop: true}},
		{MethodTerminal, Input, false, false, Channels{}},
		{MethodTerminal, Input, true, true, Channels{Terminal: true}},
		{MethodDesktop, Failed, true, true, Channels{Desktop: true}},
		{MethodTmux, Failed, true, true, Channels{Bell: true}},
		{MethodTmux, Done, true, true, Channels{}},
		{MethodOff, Input, true, true, Channels{}},
	} {
		if got := (Settings{Method: tc.method}).Route(tc.k, tc.terminal, tc.tmux); got != tc.want {
			t.Errorf("%s %s terminal=%v tmux=%v: %+v", tc.method, tc.k, tc.terminal, tc.tmux, got)
		}
	}
}

func TestAgents(t *testing.T) {
	now := at(10, 0)
	var st State
	if st.SetAgent("", Agent{State: "done"}) || st.SetAgent("s", Agent{}) {
		t.Error("recorded without a session or state")
	}
	if !st.SetAgent("s1", Agent{State: Working, At: now, Account: "claude/work", Cwd: "/r/a/"}) {
		t.Error("first state not a change")
	}
	if st.SetAgent("s1", Agent{State: Working, At: now.Add(time.Second), Account: "claude/work", Cwd: "/r/a"}) {
		t.Error("same state reported as a change")
	}
	st.SetAgent("s0", Agent{State: "done", At: now.Add(-time.Hour), Account: "claude/work", Cwd: "/r/a"})
	st.SetAgent("s2", Agent{State: "input", At: now, Account: "claude/main", Cwd: "/r/a"})
	if a, ok := st.AgentAt("claude/work", "/r/a"); !ok || a.State != Working {
		t.Errorf("AgentAt = %+v %v", a, ok)
	}
	if _, ok := st.AgentAt("claude/work", "/r/b"); ok {
		t.Error("other dir matched")
	}
	st.Agents["old"] = Agent{State: "done", At: now.Add(-48 * time.Hour)}
	st.prune(now)
	if _, ok := st.Agents["old"]; ok || len(st.Agents) != 3 {
		t.Errorf("agents after prune = %v", st.Agents)
	}
	for _, tc := range []struct {
		e    Event
		want string
	}{
		{Event{Kind: Input}, "input"},
		{Event{Kind: Done, Busy: true}, "done"},
		{Event{Busy: true}, Working},
		{Event{Kind: Info}, ""},
		{Event{}, ""},
	} {
		if got := tc.e.State(); got != tc.want {
			t.Errorf("%+v: %q", tc.e, got)
		}
	}
}
