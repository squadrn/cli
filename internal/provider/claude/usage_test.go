package claude

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestParseStatusLine(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	in := `{"session_id":"abc","cwd":"/r","model":{"id":"x"},"rate_limits":{
	  "five_hour":{"used_percentage":42.5,"resets_at":1800003600},
	  "seven_day":{"used_percentage":18},
	  "spend_limit":{"used_percentage":63,"resets_at":1800090000,"used_usd":314.12,"limit_usd":500,"period":"monthly"}}}`
	s, err := ParseStatusLine([]byte(in), now)
	if err != nil {
		t.Fatal(err)
	}
	if s.Session != "abc" || s.Cwd != "/r" || !s.ObservedAt.Equal(now) {
		t.Fatalf("meta = %+v", s)
	}
	if s.Five == nil || s.Five.UsedPct != 42.5 || !s.Five.ResetsAt.Equal(time.Unix(1800003600, 0)) {
		t.Fatalf("five = %+v", s.Five)
	}
	if s.Seven == nil || s.Seven.UsedPct != 18 || !s.Seven.ResetsAt.IsZero() {
		t.Fatalf("seven = %+v", s.Seven)
	}
	if s.Spend == nil || s.Spend.UsedPct != 63 || *s.Spend.UsedUSD != 314.12 || s.Spend.Period != "monthly" {
		t.Fatalf("spend = %+v", s.Spend)
	}

	s, err = ParseStatusLine([]byte(`{"session_id":"x","rate_limits":{"five_hour":{"resets_at":1}}}`), now)
	if err != nil || s.Five != nil || s.Seven != nil || s.Spend != nil {
		t.Fatalf("missing percentages: %+v %v", s, err)
	}
	s, err = ParseStatusLine([]byte(`{"session_id":"x"}`), now)
	if err != nil || s.Five != nil {
		t.Fatalf("no rate limits: %+v %v", s, err)
	}
	if _, err := ParseStatusLine([]byte(`nope`), now); err == nil {
		t.Fatal("garbage parsed")
	}
}

func TestParseStopFailure(t *testing.T) {
	loc := time.FixedZone("X", 2*3600)
	now := time.Date(2026, 11, 18, 10, 0, 0, 0, loc)
	in := `{"session_id":"s1","cwd":"/r","hook_event_name":"StopFailure","error":"rate_limit","error_details":"429 Too Many Requests","last_assistant_message":"You've hit your session limit · resets 3:45pm"}`
	f, err := ParseStopFailure([]byte(in), now, loc)
	if err != nil {
		t.Fatal(err)
	}
	o := f.Observation
	if !f.Limited || f.Details != "429 Too Many Requests" || o.LimitedSession != "s1" || o.LimitedCwd != "/r" || !o.LimitedAt.Equal(now) {
		t.Fatalf("failure = %+v", f)
	}
	if want := time.Date(2026, 11, 18, 15, 45, 0, 0, loc); !o.ResetEstimate.Equal(want) {
		t.Fatalf("reset = %v, want %v", o.ResetEstimate, want)
	}
	f, err = ParseStopFailure([]byte(`{"error":"rate_limit"}`), now, loc)
	if err != nil || !f.Limited || !f.Observation.ResetEstimate.IsZero() {
		t.Fatalf("no message: %+v %v", f, err)
	}
	f, err = ParseStopFailure([]byte(`{"error":"server_error"}`), now, loc)
	if err != nil || f.Limited || !f.Observation.LimitedAt.IsZero() {
		t.Fatalf("other error: %+v %v", f, err)
	}
	if _, err := ParseStopFailure([]byte(`[`), now, loc); err == nil {
		t.Fatal("garbage parsed")
	}
}

func TestParseResetTime(t *testing.T) {
	loc := time.FixedZone("X", -5*3600)
	now := time.Date(2026, 11, 18, 14, 0, 0, 0, loc) // a Wednesday
	at := func(d, h, m int) time.Time { return time.Date(2026, 11, d, h, m, 0, 0, loc) }
	berlin, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		t.Skip("no tzdata")
	}
	tests := []struct {
		msg  string
		want time.Time
	}{
		{"You've hit your session limit · resets 3:45pm", at(18, 15, 45)},
		{"limit · resets 3pm", at(18, 15, 0)},
		{"resets 2:00pm", at(19, 14, 0)}, // exactly now: next day
		{"resets 1:30pm", at(19, 13, 30)},
		{"resets 12:00am", at(19, 0, 0)},
		{"resets 12:30pm", at(19, 12, 30)},
		{"You've hit your weekly limit · resets Mon 12:00am", at(23, 0, 0)},
		{"resets Wed 3pm", at(18, 15, 0)},
		{"resets Wed 1pm", at(25, 13, 0)},
		{"resets Wednesday 1pm", at(25, 13, 0)},
		{"RESETS 9:05 PM", at(18, 21, 5)},
		{"resets at 4pm", at(18, 16, 0)},
		{"resets 3.45pm", time.Time{}},
		{"resets Nov 20, 5pm", at(20, 17, 0)},
		{"resets Nov 1 at 5pm", time.Date(2027, 11, 1, 17, 0, 0, 0, loc)},
		{"resets Feb 30, 5pm", time.Time{}},
		{"resets 3:45pm (Europe/Berlin)", time.Date(2026, 11, 18, 15, 45, 0, 0, berlin).AddDate(0, 0, 1)},
		{"resets 3:45pm (Not/AZone)", at(18, 15, 45)},
		{"resets 13pm", time.Time{}},
		{"resets 0am", time.Time{}},
		{"resets 3:75pm", time.Time{}},
		{"no time here", time.Time{}},
		{"", time.Time{}},
	}
	for _, tt := range tests {
		got, ok := ParseResetTime(tt.msg, now, loc)
		if ok != !tt.want.IsZero() || !got.Equal(tt.want) {
			t.Errorf("%q: got %v (%v), want %v", tt.msg, got, ok, tt.want)
		}
	}
	if _, ok := ParseResetTime("resets 3pm", now, nil); !ok {
		t.Error("nil location")
	}
}

func FuzzParseResetTime(f *testing.F) {
	for _, s := range []string{"resets 3:45pm", "resets Mon 12:00am", "resets Nov 20, 5pm (UTC)", "resets 99:99am", "resets"} {
		f.Add(s)
	}
	now := time.Date(2026, 11, 18, 14, 0, 0, 0, time.UTC)
	f.Fuzz(func(t *testing.T, msg string) {
		got, ok := ParseResetTime(msg, now, time.UTC)
		if !ok {
			if !got.IsZero() {
				t.Fatalf("%q: time without ok", msg)
			}
			return
		}
		if !got.After(now) || got.Sub(now) > 367*24*time.Hour {
			t.Fatalf("%q: %v is not the next occurrence after %v", msg, got, now)
		}
	})
}

func TestStatusLineSettings(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	if _, ok, err := StatusLineCommand(path); ok || err != nil {
		t.Fatalf("missing file: %v %v", ok, err)
	}
	orig := "{\n  \"model\": \"opus\",\n  \"statusLine\": {\"type\": \"command\", \"command\": \"~/sl.sh\", \"padding\": 2},\n  \"env\": {\"A\": \"<b>\"}\n}\n"
	if err := os.WriteFile(path, []byte(orig), 0o640); err != nil {
		t.Fatal(err)
	}
	if c, ok, err := StatusLineCommand(path); c != "~/sl.sh" || !ok || err != nil {
		t.Fatalf("command = %q %v %v", c, ok, err)
	}
	if err := SetStatusLineCommand(path, `x hook statusline --then '~/sl.sh'`); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	want := "{\n  \"model\": \"opus\",\n  \"statusLine\": {\n    \"type\": \"command\",\n    \"command\": \"x hook statusline --then '~/sl.sh'\",\n    \"padding\": 2\n  },\n  \"env\": {\n    \"A\": \"<b>\"\n  }\n}\n"
	if string(b) != want {
		t.Fatalf("settings =\n%s\nwant\n%s", b, want)
	}
	if fi, _ := os.Stat(path); runtime.GOOS != "windows" && fi.Mode().Perm() != 0o640 {
		t.Fatalf("mode = %v", fi.Mode())
	}
	if err := SetStatusLineCommand(path, ""); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := StatusLineCommand(path); ok {
		t.Fatal("status line not removed")
	}

	fresh := filepath.Join(dir, "new.json")
	if err := SetStatusLineCommand(fresh, "a"); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(fresh); !strings.Contains(string(b), `"type": "command"`) {
		t.Fatalf("new file: %s", b)
	}
	if err := SetStatusLineCommand(fresh, ""); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(fresh); string(b) != "{}\n" {
		t.Fatalf("emptied file: %q", b)
	}

	for _, bad := range []string{"[1]", "{", `{"a":1} x`, `{"a":}`} {
		if err := os.WriteFile(path, []byte(bad), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, _, err := StatusLineCommand(path); err == nil {
			t.Errorf("%q: no error", bad)
		}
		if err := SetStatusLineCommand(path, "x"); err == nil {
			t.Errorf("%q: written", bad)
		}
	}
	if err := os.WriteFile(path, []byte(`{"statusLine": "weird"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := StatusLineCommand(path); ok || err != nil {
		t.Fatalf("non-object status line: %v %v", ok, err)
	}
	if err := SetStatusLineCommand(path, "x"); err != nil {
		t.Fatal(err)
	}
	if c, _, _ := StatusLineCommand(path); c != "x" {
		t.Fatalf("replaced non-object: %q", c)
	}
}

func TestStatusLineThroughLink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks")
	}
	dir := t.TempDir()
	main := filepath.Join(dir, "main.json")
	if err := os.WriteFile(main, []byte(`{"a":1}`), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.json")
	if err := os.Symlink(main, link); err != nil {
		t.Fatal(err)
	}
	if err := SetStatusLineCommand(link, "x"); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Lstat(link); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatal("link replaced by a file")
	}
	if c, _, _ := StatusLineCommand(main); c != "x" {
		t.Fatalf("main not changed: %q", c)
	}
}
