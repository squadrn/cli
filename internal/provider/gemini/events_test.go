package gemini

import (
	"testing"

	"github.com/squadrn/squadrn/internal/notify"
	"github.com/squadrn/squadrn/internal/provider"
)

// The payloads follow the hook input ADR 0013's probe recorded (BeforeAgent, AfterAgent) and
// the hooks reference (Notification).
func TestParseEvent(t *testing.T) {
	common := `"session_id":"g1","transcript_path":"/t/chats/g1.json","cwd":"/src/api","timestamp":"2026-11-21T10:00:00.000Z"`
	for name, tt := range map[string]struct {
		raw  string
		want notify.Event
	}{
		"before agent": {`{` + common + `,"hook_event_name":"BeforeAgent","prompt":"fix it"}`, notify.Event{Busy: true}},
		"after agent":  {`{` + common + `,"hook_event_name":"AfterAgent","prompt":"fix it","prompt_response":"Fixed.","stop_hook_active":false}`, notify.Event{Kind: notify.Done, Message: "Fixed."}},
		"retried turn": {`{` + common + `,"hook_event_name":"AfterAgent","prompt":"p","prompt_response":"r","stop_hook_active":true}`, notify.Event{Busy: true}},
		"permission": {
			`{` + common + `,"hook_event_name":"Notification","notification_type":"ToolPermission","message":"Allow?","details":{"toolName":"run_shell_command"}}`,
			notify.Event{Kind: notify.Input, Reason: notify.ReasonPermission, Tool: "run_shell_command"},
		},
		"permission without details": {
			`{` + common + `,"hook_event_name":"Notification","notification_type":"ToolPermission","message":"Allow?"}`,
			notify.Event{Kind: notify.Input, Reason: notify.ReasonPermission},
		},
		"other notification": {`{` + common + `,"hook_event_name":"Notification","notification_type":"Other"}`, notify.Event{}},
		"session start":      {`{` + common + `,"hook_event_name":"SessionStart","source":"startup"}`, notify.Event{}},
	} {
		t.Run(name, func(t *testing.T) {
			e, err := Events{}.ParseEvent([]byte(tt.raw))
			if err != nil {
				t.Fatal(err)
			}
			if e.Session != "g1" || e.Cwd != "/src/api" {
				t.Errorf("session %q cwd %q", e.Session, e.Cwd)
			}
			e.Session, e.Cwd = "", ""
			if e != tt.want {
				t.Errorf("got %+v\nwant %+v", e, tt.want)
			}
		})
	}
	if _, err := (Events{}).ParseEvent([]byte(`{`)); err == nil {
		t.Error("bad input accepted")
	}
}

func TestProfiles(t *testing.T) {
	p := Profiles{}
	doc, err := p.Profile(provider.ProfileStandard, []string{"privacy"}, provider.ProfileContext{Notify: true})
	if err != nil || doc["general"].(map[string]any)["enableNotifications"] != false {
		t.Fatalf("notify: %v, %v", doc, err)
	}
	if doc, _ := p.Profile(provider.ProfileMinimal, nil, provider.ProfileContext{}); doc["general"] != nil {
		t.Fatalf("no notify: %v", doc)
	}
	if _, err := p.Profile("nope", nil, provider.ProfileContext{}); err == nil {
		t.Fatal("unknown profile")
	}
	if _, err := p.Profile(provider.ProfileStrict, []string{"nope"}, provider.ProfileContext{}); err == nil {
		t.Fatal("unknown option")
	}
}
