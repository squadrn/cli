//go:build windows

package cli

import (
	"errors"
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

// startDetached starts cmd without a console (no window flashes for it or the programs it runs),
// in its own process group (ctrl-c in the terminal does not reach it) and outside any job the
// terminal put sprawl in, so it survives sprawl's exit. Breaking away is not always allowed;
// then it runs inside the job.
func startDetached(cmd *exec.Cmd) error {
	flags := uint32(windows.CREATE_NO_WINDOW | windows.CREATE_NEW_PROCESS_GROUP)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: flags | windows.CREATE_BREAKAWAY_FROM_JOB}
	err := cmd.Start()
	if errors.Is(err, windows.ERROR_ACCESS_DENIED) {
		cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: flags}
		cmd.Process = nil
		return cmd.Start()
	}
	return err
}
