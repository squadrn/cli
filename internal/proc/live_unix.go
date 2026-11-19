//go:build !windows

package proc

import (
	"context"
	"errors"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// running reports whether pid exists (EPERM: it runs as another user).
func running(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

// StartTime is the process start time from ps(1), the same on macOS and Linux and unchanged by
// exec. "" when ps is unavailable.
func StartTime(pid int) string {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	env := append(os.Environ(), "LC_ALL=C", "TZ=UTC")
	out, err := Output(ctx, Options{Env: env}, "ps", "-o", "lstart=", "-p", strconv.Itoa(pid))
	if err != nil {
		return ""
	}
	return strings.Join(strings.Fields(string(out)), " ")
}
