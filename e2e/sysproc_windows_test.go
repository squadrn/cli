//go:build e2e

package e2e

import (
	"syscall"

	"golang.org/x/sys/windows"
)

// sysProcAttr detaches each command from the runner's console, so prompts that fall back to
// CONIN$ see no terminal, as on a Unix runner. A non-empty cmdLine replaces the command line.
func sysProcAttr(cmdLine string) *syscall.SysProcAttr {
	return &syscall.SysProcAttr{CmdLine: cmdLine, CreationFlags: windows.DETACHED_PROCESS}
}
