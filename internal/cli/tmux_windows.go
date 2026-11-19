//go:build windows

package cli

import "fmt"

func (a *app) requireTmux(what string) error {
	return fmt.Errorf("%s is not available on Windows: it runs agents in tmux panes; start each agent in its own terminal with: %s wt <name>", what, a.cmd)
}

// hasTmux: grids are not supported on Windows.
func hasTmux() bool { return false }
