//go:build !windows

package cli

import (
	"bytes"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestQuiesceRefusesWhenStillRunning(t *testing.T) {
	cmd := exec.Command("sh", "-c", `trap "" INT TERM; while :; do sleep 1; done`)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	time.Sleep(100 * time.Millisecond) // let the trap be set
	old := stopWait
	stopWait = 300 * time.Millisecond
	defer func() { stopWait = old }()
	var errb bytes.Buffer
	a := &app{cmd: "squadrn", stderr: &errb}
	err := a.quiesce([]holder{{pid: cmd.Process.Pid, desc: "claude/x session"}}, true)
	if err == nil || !strings.Contains(err.Error(), "is still running; stop it, then swap again") {
		t.Fatalf("quiesce = %v", err)
	}
	if err := a.quiesce([]holder{{pid: 999999999, desc: "gone"}}, false); err != nil {
		t.Fatalf("a holder that is gone: %v", err)
	}
}
