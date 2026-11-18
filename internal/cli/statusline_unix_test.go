//go:build !windows

package cli

import (
	"os/exec"
	"strings"
	"testing"
)

func TestStatusLineWordRoundTrip(t *testing.T) {
	for _, s := range []string{
		"/usr/local/bin/sprawl",
		"/Users/a b/.local/share/sprawl/bin/sprawl",
		`echo "it's"; printf '%s' $HOME`,
		"",
		"a'b'c",
	} {
		q, err := statusLineWord(s, "/home")
		if err != nil {
			t.Fatal(err)
		}
		out, err := exec.Command("sh", "-c", "printf '%s' "+q).Output()
		if err != nil || string(out) != s {
			t.Errorf("%q: shell read %q (%v) from %s", s, out, err, q)
		}
		cmd := "/x/sprawl" + statusLineHook + " --then " + q
		if got, ours := unwrapStatusLine(cmd); s != "" && (!ours || got != s) {
			t.Errorf("%q: unwrap %s = %q, %v", s, cmd, got, ours)
		}
	}
	if sh, args := statusLineShell("true"); sh == "" || strings.Join(args, " ") != "-c true" {
		t.Errorf("shell = %s %v", sh, args)
	}
}
