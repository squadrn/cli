//go:build !windows

package claude

import "syscall"

// run replaces the current process with bin: claude inherits the terminal, signals and exit
// status directly.
func run(bin string, argv, env []string) error {
	return syscall.Exec(bin, argv, env)
}
