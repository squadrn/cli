//go:build !windows

package cli

import (
	"syscall"
)

// stopProcess sends pid SIGINT, the signal of ctrl-c, which lets the session save and exit; force
// sends SIGTERM.
func stopProcess(pid int, force bool) error {
	sig := syscall.SIGINT
	if force {
		sig = syscall.SIGTERM
	}
	return syscall.Kill(pid, sig)
}
