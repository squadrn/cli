package codex

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/squadrn/squadrn/internal/notify"
)

// The fixtures follow the hook input and the notify argument documented by Codex.
func TestParseEvent(t *testing.T) {
	for file, want := range map[string]notify.Event{
		"permission.json":    {Kind: notify.Input, Reason: notify.ReasonPermission, Tool: "Bash"},
		"prompt.json":        {Busy: true},
		"post-tool.json":     {Busy: true},
		"stop.json":          {Kind: notify.Done, Message: "All tests pass."},
		"stop-active.json":   {Busy: true},
		"turn-complete.json": {Kind: notify.Done, Message: "All tests pass."},
		"session-start.json": {},
	} {
		t.Run(file, func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join("testdata", "hooks", file))
			if err != nil {
				t.Fatal(err)
			}
			e, err := Events{}.ParseEvent(raw)
			if err != nil {
				t.Fatal(err)
			}
			if e.Session != "abc123" || e.Cwd != "/Users/me/api" {
				t.Errorf("session %q cwd %q", e.Session, e.Cwd)
			}
			if file != "session-start.json" && e.Turn != "t1" {
				t.Errorf("turn %q", e.Turn)
			}
			e.Session, e.Turn, e.Cwd = "", "", ""
			if e != want {
				t.Errorf("got %+v\nwant %+v", e, want)
			}
		})
	}
	if e, _ := (Events{}).ParseEvent([]byte(`{"type":"something-else","thread-id":"s"}`)); e.Kind != "" || e.Busy {
		t.Errorf("unknown notify type = %+v", e)
	}
	if _, err := (Events{}).ParseEvent([]byte("{")); err == nil {
		t.Error("broken input accepted")
	}
	for raw, want := range map[string]string{
		`{"hook_event_name":"Stop"}`:              "{}",
		`{"hook_event_name":"PermissionRequest"}`: "",
		`{"type":"agent-turn-complete"}`:          "",
		`not json`:                                "",
	} {
		if got := (Events{}).HookReply([]byte(raw)); got != want {
			t.Errorf("HookReply(%s) = %q", raw, got)
		}
	}
}
