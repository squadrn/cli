//go:build !windows

package codex

import "testing"

func TestHookCommandUnix(t *testing.T) {
	cmd := hookCommand("/opt/squadrn", guardArgs)
	if cmd != `"/opt/squadrn/plugin/bin/squadrn-hook" guard-git --no-ask` {
		t.Fatalf("command %s", cmd)
	}
}
