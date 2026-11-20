package claude

import (
	"encoding/json"

	"github.com/squadrn/squadrn/internal/notify"
)

// Events maps Claude Code's Notification, Stop and StopFailure hook input to notification
// events, and UserPromptSubmit and PostToolUse to a working agent (hooks reference, checked against Claude Code 2.1.329; ADR 0012).
type Events struct{}

type hookEvent struct {
	SessionID        string            `json:"session_id"`
	PromptID         string            `json:"prompt_id"`
	Cwd              string            `json:"cwd"`
	HookEventName    string            `json:"hook_event_name"`
	Message          string            `json:"message"`
	NotificationType string            `json:"notification_type"`
	StopHookActive   bool              `json:"stop_hook_active"`
	LastAssistant    string            `json:"last_assistant_message"`
	BackgroundTasks  []json.RawMessage `json:"background_tasks"`
	SessionCrons     []json.RawMessage `json:"session_crons"`
	Error            string            `json:"error"`
}

// notificationKinds lists the notification types squadrn notifies about; the plugin's matcher
// names the same ones. Permission, idle and elicitation dialogs already fire only when the user
// appears to be away.
var notificationKinds = map[string]notify.Kind{
	"permission_prompt":          notify.Input,
	"idle_prompt":                notify.Input,
	"elicitation_dialog":         notify.Input,
	"elicitation_url_dialog":     notify.Input,
	"auth_success":               notify.Info,
	"quota_auto_resume_fired":    notify.Info,
	"quota_auto_resume_stale":    notify.Info,
	"quota_auto_resume_disabled": notify.Info,
	"plugin_notification":        notify.Info,
}

func (Events) ParseEvent(raw []byte) (notify.Event, error) {
	var in hookEvent
	if err := json.Unmarshal(raw, &in); err != nil {
		return notify.Event{}, err
	}
	e := notify.Event{Session: in.SessionID, Turn: in.PromptID, Cwd: in.Cwd}
	if in.HookEventName == "" && in.Error != "" {
		// The rc.3 plugin's `hook limit` input is a StopFailure; input without the event name is one too.
		in.HookEventName = "StopFailure"
	}
	switch in.HookEventName {
	case "Notification":
		e.Kind, e.Reason = notificationKinds[in.NotificationType], in.NotificationType
		e.Message = in.Message
		if in.NotificationType == notify.ReasonPermission {
			e.Tool = notify.ToolFromMessage(in.Message)
		}
	case "Stop":
		// A turn that keeps going (a Stop hook continued it) or leaves background work behind
		// is not done yet.
		if !in.StopHookActive && len(in.BackgroundTasks) == 0 && len(in.SessionCrons) == 0 {
			e.Kind, e.Message = notify.Done, in.LastAssistant
		} else {
			e.Busy = true
		}
	case "UserPromptSubmit", "PostToolUse":
		e.Busy = true
	case "StopFailure":
		e.Kind, e.Reason = notify.Failed, in.Error
	}
	return e, nil
}
