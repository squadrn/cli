//go:build !windows

package fsx

import (
	"os"
	"time"
)

// replaceExecutable copies src over dst atomically (a running binary keeps its old inode).
func replaceExecutable(src, dst string) error {
	if err := CopyFile(src, dst, 0o755, false); err != nil {
		return err
	}
	return os.Chmod(dst, 0o755)
}

// cleanOld has nothing to do: a replaced binary leaves no file behind on Unix.
func cleanOld(string, []string, time.Time) {}
