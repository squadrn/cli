//go:build !windows

package guard

import (
	"os"
	"syscall"
)

// openLog opens the log for appending. O_NOFOLLOW: a symlink planted at the log path cannot
// redirect writes elsewhere.
func openLog(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY|syscall.O_NOFOLLOW, 0o600)
}
