//go:build !windows

package fsx

import "os"

// replaceExecutable copies src over dst atomically (a running binary keeps its old inode).
func replaceExecutable(src, dst string) error {
	if err := CopyFile(src, dst, 0o755, false); err != nil {
		return err
	}
	return os.Chmod(dst, 0o755)
}
