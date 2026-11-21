package claude

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/squadrn/squadrn-cli/internal/notify"
)

// The fixtures follow the hook input documented in Claude Code's hooks reference.
func TestParseEvent(t *testing.T) {
	for file, want := range map[string]notify.Event{
		"notification-permission.json":  {Kind: notify.Input, Reason: "permission_prompt", Tool: "Bash", Message: "Claude needs your permission to use Bash"},
		"notification-idle.json":        {Kind: notify.Input, Reason: "idle_prompt", Message: "Claude is waiting for your input"},
		"notification-elicitation.json": {Kind: notify.Input, Reason: "elicitation_dialog", Message: "An MCP server asks for input"},
		"notification-auth.json":        {Kind: notify.Info, Reason: "auth_success", Message: "Logged in"},
		"notification-unknown.json":     {Reason: "brand_new_type", Message: "Something new"},
		"stop.json":                     {Kind: notify.Done, Message: "All tests pass."},
		"stop-active.json":              {Busy: true},
		"stop-background.json":          {Busy: true},
		"stop-crons.json":               {Busy: true},
		"prompt.json":                   {Busy: true},
		"post-tool.json":                {Busy: true},
		"stopfailure-rate-limit.json":   {Kind: notify.Failed, Reason: "rate_limit"},
		"stopfailure-auth.json":         {Kind: notify.Failed, Reason: "authentication_failed"},
		"stopfailure-server.json":       {Kind: notify.Failed, Reason: "server_error"},
		"session-start.json":            {},
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
			e.Session, e.Turn, e.Cwd = "", "", ""
			if e != want {
				t.Errorf("got %+v\nwant %+v", e, want)
			}
		})
	}
	if e, _ := (Events{}).ParseEvent([]byte(`{"session_id":"s","error":"rate_limit"}`)); e.Kind != notify.Failed || e.Reason != "rate_limit" {
		t.Errorf("bare StopFailure input = %+v", e)
	}
	if _, err := (Events{}).ParseEvent([]byte("{")); err == nil {
		t.Error("broken input accepted")
	}
}
