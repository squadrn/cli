package cli

import (
	"context"
	"os"
	"os/exec"
	"regexp"
	"strings"

	"github.com/squadrn/squadrn/internal/proc"
)

const osascriptNote = "notifications appear as Script Editor and need its permission (System Settings → Notifications); clicking does nothing. brew install terminal-notifier for grouping and click-to-focus"

// findNotifier prefers terminal-notifier (grouping, click action) over osascript.
func findNotifier() (notifier, bool) {
	if p, err := exec.LookPath("terminal-notifier"); err == nil {
		return notifier{Tool: "terminal-notifier", Path: p}, true
	}
	if p, err := exec.LookPath("osascript"); err == nil {
		return notifier{Tool: "osascript", Path: p, Note: osascriptNote}, true
	}
	return notifier{Note: "neither terminal-notifier nor osascript is on PATH"}, false
}

func (n notifier) send(ctx context.Context, d delivery, _ string) (string, error) {
	argv := notifyArgs(n.Tool, d)
	_, err := proc.Output(ctx, proc.Options{}, n.Path, argv...)
	return "", err
}

// notifyArgs passes text as arguments, never as script source, so titles cannot inject code.
func notifyArgs(tool string, d delivery) []string {
	if tool == "terminal-notifier" {
		args := []string{"-title", d.Title, "-message", d.Body}
		if d.Group != "" {
			args = append(args, "-group", "squadrn-"+d.Group)
		}
		if d.Activate != "" {
			args = append(args, "-activate", d.Activate)
		}
		if d.Execute != "" {
			args = append(args, "-execute", d.Execute)
		}
		if d.Sound {
			args = append(args, "-sound", "default")
		}
		return args
	}
	script := `display notification (item 2 of argv) with title (item 1 of argv)`
	if d.Sound {
		script += ` sound name "Glass"`
	}
	return []string{"-e", "on run argv", "-e", script, "-e", "end run", d.Title, d.Body}
}

// terminalApps maps TERM_PROGRAM to the app's bundle id, for terminals that do not export
// __CFBundleIdentifier.
var terminalApps = map[string]string{
	"Apple_Terminal": "com.apple.Terminal",
	"iTerm.app":      "com.googlecode.iterm2",
	"WezTerm":        "com.github.wez.wezterm",
	"ghostty":        "com.mitchellh.ghostty",
	"WarpTerminal":   "dev.warp.Warp-Stable",
	"vscode":         "com.microsoft.VSCode",
}

// terminalBundle is the bundle id of the terminal app the session runs in ("" when unknown).
func terminalBundle() string {
	if b := os.Getenv("__CFBundleIdentifier"); b != "" {
		return b
	}
	return terminalApps[os.Getenv("TERM_PROGRAM")]
}

var bundleLine = regexp.MustCompile(`"CFBundleIdentifier"="([^"]+)"`)

// terminalFrontmost compares the frontmost app (lsappinfo) with the session's terminal.
func terminalFrontmost(ctx context.Context) (front, known bool) {
	bundle := terminalBundle()
	if bundle == "" {
		return false, false
	}
	asn, err := proc.Output(ctx, proc.Options{}, "lsappinfo", "front")
	if err != nil || strings.TrimSpace(string(asn)) == "" {
		return false, false
	}
	out, err := proc.Output(ctx, proc.Options{}, "lsappinfo", "info", "-only", "bundleid", strings.TrimSpace(string(asn)))
	m := bundleLine.FindSubmatch(out)
	if err != nil || m == nil {
		return false, false
	}
	return strings.EqualFold(string(m[1]), bundle), true
}

func focusSources() string {
	return "tmux pane in view (inside tmux) · frontmost terminal app (lsappinfo)"
}

// activateApp brings the app with bundle id forward.
func activateApp(ctx context.Context, bundle string) error {
	_, err := proc.Output(ctx, proc.Options{}, "open", "-b", bundle)
	return err
}

// registerToastApp and unregisterToastApp: only Windows registers an app id for notifications.
func registerToastApp() error   { return nil }
func unregisterToastApp() error { return nil }
