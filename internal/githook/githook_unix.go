//go:build !windows

package githook

import "io/fs"

// isRunnable mirrors git: a hook runs when it is an executable regular file.
func isRunnable(_ string, fi fs.FileInfo) bool {
	return !fi.IsDir() && fi.Mode().Perm()&0o111 != 0
}

// HookCommand runs the hook file directly; its #! line picks the interpreter.
func HookCommand(path string, args []string) (string, []string) {
	return path, args
}
