//go:build !windows

package cli

import (
	"os/exec"
	"syscall"
)

// detach runs cmd in a new session, without a controlling terminal.
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
