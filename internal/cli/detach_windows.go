//go:build windows

package cli

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"syscall"

	"golang.org/x/sys/windows"
)

// startDetached starts cmd without a console (no window flashes for it or the programs it runs),
// in its own process group (ctrl-c in the terminal does not reach it) and outside any job the
// terminal put squadrn in, so it survives squadrn's exit. Breaking away is not always allowed;
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

// removeAfterExit deletes paths a few seconds after squadrn exits, from a hidden cmd.exe: Windows
// keeps running executables locked until their process ends.
func removeAfterExit(paths []string) error {
	var b strings.Builder
	b.WriteString("ping -n 4 127.0.0.1 >nul")
	for _, p := range paths {
		if strings.ContainsAny(p, `"%^&!`) {
			return fmt.Errorf("refusing to schedule removal of %q", p)
		}
		fmt.Fprintf(&b, ` & rmdir /s /q "%[1]s" 2>nul & del /f /q "%[1]s" 2>nul`, p)
	}
	cmd := exec.Command("cmd.exe")
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CmdLine:       `cmd.exe /d /c "` + b.String() + `"`,
		CreationFlags: windows.CREATE_NO_WINDOW | windows.CREATE_NEW_PROCESS_GROUP | windows.CREATE_BREAKAWAY_FROM_JOB,
	}
	if err := cmd.Start(); err != nil {
		if !errors.Is(err, windows.ERROR_ACCESS_DENIED) {
			return err
		}
		cmd = exec.Command("cmd.exe")
		cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: `cmd.exe /d /c "` + b.String() + `"`, CreationFlags: windows.CREATE_NO_WINDOW | windows.CREATE_NEW_PROCESS_GROUP}
		if err := cmd.Start(); err != nil {
			return err
		}
	}
	return cmd.Process.Release()
}
