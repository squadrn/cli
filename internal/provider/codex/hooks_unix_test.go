//go:build !windows

package codex

import "testing"

func TestHookCommandUnix(t *testing.T) {
	if cmd := hookCommand("/opt/squadrn"); cmd != `"/opt/squadrn/plugin/bin/squadrn-hook" guard-git --no-ask` {
		t.Fatalf("command %s", cmd)
	}
}
