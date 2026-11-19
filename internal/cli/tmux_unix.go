//go:build !windows

package cli

import (
	"fmt"
	"os/exec"
)

func (a *app) requireTmux(what string) error {
	if !hasTmux() {
		return fmt.Errorf("tmux is required for %s", what)
	}
	return nil
}

// hasTmux reports whether tmux is on PATH.
func hasTmux() bool {
	_, err := exec.LookPath("tmux")
	return err == nil
}
