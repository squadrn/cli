//go:build !windows

package fsx

import "os"

// ReplaceExecutable installs src at dst as an executable, replacing dst even while it runs: the
// copy is atomic and a running binary keeps its old inode.
func ReplaceExecutable(src, dst string) error {
	if err := CopyFile(src, dst, 0o755, false); err != nil {
		return err
	}
	return os.Chmod(dst, 0o755)
}

// CleanOld has nothing to do: a replaced binary leaves no file behind on Unix.
func CleanOld(string, []string) {}
