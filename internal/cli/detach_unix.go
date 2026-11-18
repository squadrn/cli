//go:build !windows

package cli

import (
	"os/exec"
	"syscall"
)

// startDetached starts cmd in a new session, without a controlling terminal.
func startDetached(cmd *exec.Cmd) error {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	return cmd.Start()
}
