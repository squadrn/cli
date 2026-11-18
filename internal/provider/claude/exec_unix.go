//go:build !windows

package claude

import (
	"os/exec"
	"syscall"
)

func lookPath() (string, error) {
	bin, err := exec.LookPath("claude")
	if err != nil {
		return "", errNotInstalled
	}
	return bin, nil
}

// run replaces the current process with bin: claude inherits the terminal, signals and exit
// status directly.
func run(bin string, argv, env []string) error {
	return syscall.Exec(bin, argv, env)
}
