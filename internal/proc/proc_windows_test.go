//go:build windows

package proc

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// On Windows the job object also kills the orphaned grandchild once the command returns.
func TestJobKillsOrphanedGrandchild(t *testing.T) {
	bin, opts := helper(t, "orphan")
	out, _ := Output(context.Background(), opts, bin)
	pid, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		t.Fatalf("grandchild pid %q: %v", out, err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for alive(pid) {
		if time.Now().After(deadline) {
			t.Fatalf("grandchild %d still running after the command returned", pid)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func alive(pid int) bool {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	defer func() { _ = windows.CloseHandle(h) }()
	var code uint32
	return windows.GetExitCodeProcess(h, &code) == nil && code == 259 // STILL_ACTIVE
}
