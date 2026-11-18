//go:build !windows

package proc

import (
	"errors"
	"os/exec"
	"syscall"
)

// setup starts cmd in its own process group; cancellation kills the group, so grandchildren
// holding our pipes die too.
func setup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		// Negative pid: signal the group.
		if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
			return cmd.Process.Kill()
		}
		return nil
	}
}
