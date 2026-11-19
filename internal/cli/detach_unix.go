//go:build !windows

package cli

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

// startDetached starts cmd in a new session, without a controlling terminal.
func startDetached(cmd *exec.Cmd) error {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	return cmd.Start()
}

// removeAfterExit removes paths now: Unix lets a running program's files be deleted. deferred
// is false.
func removeAfterExit(paths []string) (deferred bool, err error) {
	var errs []error
	for _, p := range paths {
		errs = append(errs, os.RemoveAll(p))
	}
	return false, errors.Join(errs...)
}
