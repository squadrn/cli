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

// removeAfterExit removes paths now: Unix lets a running program's files be deleted.
func removeAfterExit(paths []string) error {
	var errs []error
	for _, p := range paths {
		errs = append(errs, os.RemoveAll(p))
	}
	return errors.Join(errs...)
}
