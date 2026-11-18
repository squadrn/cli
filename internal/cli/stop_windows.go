//go:build windows

package cli

import (
	"errors"

	"golang.org/x/sys/windows"
)

// errNoInterrupt: the platform cannot ask a session to stop, only terminate it.
// GenerateConsoleCtrlEvent only reaches process groups attached to the caller's own console, so
// a session in another terminal cannot get ctrl-c; it is terminated (TerminateProcess) after the
// user agreed. A session sprawl started runs as sprawl's child; both are listed as holders.
var errNoInterrupt = errors.New("cannot interrupt another console's process")

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
