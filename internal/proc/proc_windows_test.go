//go:build windows

package proc

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"
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
	for running(pid) {
		if time.Now().After(deadline) {
			t.Fatalf("grandchild %d still running after the command returned", pid)
		}
		time.Sleep(50 * time.Millisecond)
	}
}
