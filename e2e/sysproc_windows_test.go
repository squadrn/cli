//go:build e2e

package e2e

import (
	"syscall"

	"golang.org/x/sys/windows"
)

// sysProcAttr detaches the installed commands from the runner's console, so prompts that fall
// back to CONIN$ see no terminal, as on a Unix runner. Other programs keep it: Windows
// PowerShell prints nothing without one. A non-empty cmdLine replaces the command line.
func sysProcAttr(cmdLine string, installed bool) *syscall.SysProcAttr {
	a := &syscall.SysProcAttr{CmdLine: cmdLine}
	if installed {
		a.CreationFlags = windows.DETACHED_PROCESS
	}
	return a
}
