//go:build !darwin && !windows

package cli

import (
	"slices"
	"testing"
)

func TestNotifySendArgs(t *testing.T) {
	d := delivery{Title: "-t", Body: "Done", Kind: "done"}
	if got, want := notifySendArgs(d, "", false), []string{"--app-name=Squadrn", "--urgency=normal", "--", "-t", "Done"}; !slices.Equal(got, want) {
		t.Errorf("plain = %q", got)
	}
	d.Kind, d.Sound = "input", true
	want := []string{"--app-name=Squadrn", "--urgency=critical", "--print-id", "--replace-id=7", "--hint=string:sound-name:message-new-instant", "--", "-t", "Done"}
	if got := notifySendArgs(d, "7", true); !slices.Equal(got, want) {
		t.Errorf("input = %q", got)
	}
}

func TestGdbusArgs(t *testing.T) {
	got := gdbusArgs(delivery{Title: `it's \ here`, Body: "b", Kind: "input"}, "")
	if !slices.Contains(got, `'it\'s \\ here'`) || !slices.Contains(got, "0") || !slices.Contains(got, "{'urgency': <byte 2>}") {
		t.Errorf("gdbus = %q", got)
	}
	if m := gdbusID.FindStringSubmatch("(uint32 12,)\n"); m == nil || m[1] != "12" {
		t.Errorf("id = %v", m)
	}
}
