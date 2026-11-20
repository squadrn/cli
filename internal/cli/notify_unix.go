//go:build !darwin && !windows

package cli

import (
	"context"
	"errors"
	"os/exec"
	"regexp"
	"strings"

	"github.com/squadrn/squadrn/internal/proc"
)

// findNotifier uses notify-send (libnotify), else gdbus talking to the notification server.
func findNotifier() (notifier, bool) {
	if p, err := exec.LookPath("notify-send"); err == nil {
		return notifier{Tool: "notify-send", Path: p}, true
	}
	if p, err := exec.LookPath("gdbus"); err == nil {
		return notifier{Tool: "gdbus", Path: p}, true
	}
	return notifier{Note: "install notify-send (libnotify-bin / libnotify)"}, false
}

func (n notifier) send(ctx context.Context, d delivery, replaceID string) (string, error) {
	if n.Tool == "gdbus" {
		out, err := proc.Output(ctx, proc.Options{}, n.Path, gdbusArgs(d, replaceID)...)
		if m := gdbusID.FindSubmatch(out); err == nil && m != nil {
			return string(m[1]), nil
		}
		return "", err
	}
	out, err := proc.Output(ctx, proc.Options{}, n.Path, notifySendArgs(d, replaceID, true)...)
	if err != nil {
		// libnotify before 0.7.9 knows neither --print-id nor --replace-id.
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			_, err = proc.Output(ctx, proc.Options{}, n.Path, notifySendArgs(d, "", false)...)
		}
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// notifySendArgs: urgency critical for input (it stays until dismissed), text after "--".
func notifySendArgs(d delivery, replaceID string, ids bool) []string {
	urgency := "normal"
	if d.Kind == "input" {
		urgency = "critical"
	}
	args := []string{"--app-name=Squadrn", "--urgency=" + urgency}
	if ids {
		args = append(args, "--print-id")
		if replaceID != "" {
			args = append(args, "--replace-id="+replaceID)
		}
	}
	if d.Sound {
		args = append(args, "--hint=string:sound-name:message-new-instant")
	}
	return append(args, "--", d.Title, d.Body)
}

var gdbusID = regexp.MustCompile(`uint32 (\d+)`)

// gdbusArgs calls org.freedesktop.Notifications.Notify; text goes in as GVariant string
// literals, escaped, so it cannot change the call.
func gdbusArgs(d delivery, replaceID string) []string {
	if replaceID == "" {
		replaceID = "0"
	}
	urgency := "1"
	if d.Kind == "input" {
		urgency = "2"
	}
	return []string{
		"call", "--session", "--dest", "org.freedesktop.Notifications",
		"--object-path", "/org/freedesktop/Notifications", "--method", "org.freedesktop.Notifications.Notify",
		"Squadrn", replaceID, "", gvariantString(d.Title), gvariantString(d.Body), "[]",
		"{'urgency': <byte " + urgency + ">}", "-1",
	}
}

func gvariantString(s string) string {
	return "'" + strings.NewReplacer(`\`, `\\`, `'`, `\'`).Replace(s) + "'"
}

func terminalBundle() string { return "" }

// terminalFrontmost: Linux desktops have no common way to ask; tmux decides alone.
func terminalFrontmost(context.Context) (front, known bool) { return false, false }

func focusSources() string { return "tmux pane in view (inside tmux); unknown outside tmux" }

// registerToastApp and unregisterToastApp: only Windows registers an app id for notifications.
func registerToastApp() error   { return nil }
func unregisterToastApp() error { return nil }

// activateApp: only macOS brings a terminal app forward.
func activateApp(context.Context, string) error { return nil }
