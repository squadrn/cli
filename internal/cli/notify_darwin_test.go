package cli

import (
	"slices"
	"testing"
)

func TestNotifyArgsDarwin(t *testing.T) {
	d := delivery{Title: `a" & do shell script "x`, Body: "Done", Kind: "done", Group: "s1", Activate: "com.googlecode.iterm2"}
	got := notifyArgs("terminal-notifier", d)
	want := []string{"-title", d.Title, "-message", "Done", "-group", "squadrn-s1", "-activate", "com.googlecode.iterm2"}
	if !slices.Equal(got, want) {
		t.Errorf("terminal-notifier = %q", got)
	}
	d.Sound = true
	if got := notifyArgs("terminal-notifier", d); got[len(got)-1] != "default" {
		t.Errorf("sound = %q", got)
	}
	got = notifyArgs("osascript", d)
	if got[len(got)-2] != d.Title || got[len(got)-1] != "Done" || !slices.Contains(got, `display notification (item 2 of argv) with title (item 1 of argv) sound name "Glass"`) {
		t.Errorf("osascript = %q", got)
	}
	d.Sound = false
	if got := notifyArgs("osascript", d); !slices.Contains(got, `display notification (item 2 of argv) with title (item 1 of argv)`) {
		t.Errorf("silent osascript = %q", got)
	}
}

func TestTerminalBundle(t *testing.T) {
	t.Setenv("__CFBundleIdentifier", "")
	t.Setenv("TERM_PROGRAM", "ghostty")
	if got := terminalBundle(); got != "com.mitchellh.ghostty" {
		t.Errorf("ghostty = %q", got)
	}
	t.Setenv("__CFBundleIdentifier", "com.example.term")
	if got := terminalBundle(); got != "com.example.term" {
		t.Errorf("bundle env = %q", got)
	}
}
