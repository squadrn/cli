//go:build !windows

package cli

import (
	"errors"
	"syscall"
)

// errNoInterrupt: the platform cannot ask a session to stop, only terminate it.
var errNoInterrupt = errors.New("cannot interrupt another console's process")

// stopProcess sends pid SIGINT, the signal of ctrl-c, which lets the session save and exit; force
// sends SIGTERM.
func stopProcess(pid int, force bool) error {
	sig := syscall.SIGINT
	if force {
		sig = syscall.SIGTERM
	}
	return syscall.Kill(pid, sig)
}
