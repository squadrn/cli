//go:build windows

package cli

import (
	"golang.org/x/sys/windows"
)

// stopProcess: GenerateConsoleCtrlEvent only reaches process groups attached to the caller's own
// console, so a session in another terminal cannot get ctrl-c; it is terminated
// (TerminateProcess) after the user agreed. A session squadrn started runs as squadrn's child;
// both are listed as holders.
func stopProcess(pid int, force bool) error {
	if !force {
		return errNoInterrupt
	}
	h, err := windows.OpenProcess(windows.PROCESS_TERMINATE, false, uint32(pid))
	if err != nil {
		return err
	}
	defer func() { _ = windows.CloseHandle(h) }()
	return windows.TerminateProcess(h, 1)
}
