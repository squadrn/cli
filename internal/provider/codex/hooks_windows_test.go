//go:build windows

package codex

import "testing"

func TestHookCommandWindows(t *testing.T) {
	cmd := hookCommand(`C:\Users\me\.local\share\squadrn`, guardArgs)
	if cmd != `"C:\Users\me\.local\share\squadrn\bin\squadrn.exe" hook guard-git --no-ask` {
		t.Fatalf("command %s", cmd)
	}
	// The Unix shim form an older squadrn wrote is still recognised (and replaced).
	if !ours(`"C:/x/plugin/bin/squadrn-hook" guard-git --no-ask`) || !ours(cmd) {
		t.Fatal("guard command not recognised")
	}
}
