//go:build e2e

package e2e

import (
	"os/exec"
	"syscall"
)

func setCmdLine(cmd *exec.Cmd, line string) { cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: line} }
