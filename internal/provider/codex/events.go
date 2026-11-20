package codex

import (
	"encoding/json"

	"github.com/squadrn/squadrn/internal/notify"
)

// Events maps Codex's hook input (PermissionRequest, UserPromptSubmit, PostToolUse, Stop) and
// the argument of its notify program (agent-turn-complete) to notification events (Codex hooks
// and config reference, checked against Codex CLI 0.171.0; ADR 0012).
type Events struct{}

type hookEvent struct {
	SessionID      string  `json:"session_id"`
	TurnID         string  `json:"turn_id"`
	Cwd            string  `json:"cwd"`
	HookEventName  string  `json:"hook_event_name"`
	ToolName       string  `json:"tool_name"`
	StopHookActive bool    `json:"stop_hook_active"`
	LastAssistant  *string `json:"last_assistant_message"`

	// The notify program's argument.
	Type              string `json:"type"`
	ThreadID          string `json:"thread-id"`
	NotifyTurnID      string `json:"turn-id"`
	LastAssistantText string `json:"last-assistant-message"`
}

func (Events) ParseEvent(raw []byte) (notify.Event, error) {
	var in hookEvent
	if err := json.Unmarshal(raw, &in); err != nil {
		return notify.Event{}, err
	}
	if in.Type != "" {
		e := notify.Event{Session: in.ThreadID, Turn: in.NotifyTurnID, Cwd: in.Cwd}
		if in.Type == "agent-turn-complete" {
			e.Kind, e.Message = notify.Done, in.LastAssistantText
		}
		return e, nil
	}
	e := notify.Event{Session: in.SessionID, Turn: in.TurnID, Cwd: in.Cwd}
	switch in.HookEventName {
	case "PermissionRequest":
		e.Kind, e.Reason, e.Tool = notify.Input, notify.ReasonPermission, in.ToolName
	case "UserPromptSubmit", "PostToolUse":
		e.Busy = true
	case "Stop":
		// A Stop hook that continued the turn keeps it going.
		if in.StopHookActive {
			e.Busy = true
			break
		}
		e.Kind = notify.Done
		if in.LastAssistant != nil {
			e.Message = *in.LastAssistant
		}
	}
	return e, nil
}

// HookReply is what a hook prints: Codex reads a Stop hook's stdout as JSON and rejects plain
// text or nothing, the other hooks print nothing.
func (Events) HookReply(raw []byte) string {
	var in struct {
		HookEventName string `json:"hook_event_name"`
	}
	if json.Unmarshal(raw, &in) == nil && in.HookEventName == "Stop" {
		return "{}"
	}
	return ""
}
