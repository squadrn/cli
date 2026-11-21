package gemini

import (
	"encoding/json"

	"github.com/squadrn/squadrn-cli/internal/notify"
)

// Events maps Gemini CLI's hook input to notification events (ADR 0013 §6, hooks reference of
// Gemini CLI 0.70.0): BeforeAgent starts a turn, AfterAgent ends it, Notification of type
// ToolPermission asks for an approval. Gemini sends no turn id.
type Events struct{}

type hookEvent struct {
	SessionID        string         `json:"session_id"`
	Cwd              string         `json:"cwd"`
	HookEventName    string         `json:"hook_event_name"`
	PromptResponse   *string        `json:"prompt_response"`
	StopHookActive   bool           `json:"stop_hook_active"`
	NotificationType string         `json:"notification_type"`
	Details          map[string]any `json:"details"`
}

func (Events) ParseEvent(raw []byte) (notify.Event, error) {
	var in hookEvent
	if err := json.Unmarshal(raw, &in); err != nil {
		return notify.Event{}, err
	}
	e := notify.Event{Session: in.SessionID, Cwd: in.Cwd}
	switch in.HookEventName {
	case "BeforeAgent":
		e.Busy = true
	case "AfterAgent":
		// A hook that made Gemini retry the turn keeps it going.
		if in.StopHookActive {
			e.Busy = true
			break
		}
		e.Kind = notify.Done
		if in.PromptResponse != nil {
			e.Message = *in.PromptResponse
		}
	case "Notification":
		if in.NotificationType == "ToolPermission" {
			e.Kind, e.Reason, e.Tool = notify.Input, notify.ReasonPermission, toolName(in.Details)
		}
	}
	return e, nil
}

// toolName is the tool an approval is for, from the notification's details when they name it.
func toolName(d map[string]any) string {
	for _, k := range []string{"toolName", "tool_name", "name"} {
		if s, ok := d[k].(string); ok && s != "" {
			return s
		}
	}
	return ""
}
