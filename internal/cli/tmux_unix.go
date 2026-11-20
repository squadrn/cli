//go:build !windows

package cli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/squadrn/squadrn/internal/proc"
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

// tmuxPane reads the state of the pane a hook runs in ($TMUX_PANE); ok is false outside tmux.
func tmuxPane(ctx context.Context) (s paneState, ok bool) {
	pane := os.Getenv("TMUX_PANE")
	if pane == "" || os.Getenv("TMUX") == "" {
		return s, false
	}
	out, err := proc.Output(ctx, proc.Options{}, "tmux", "display-message", "-p", "-t", pane,
		"#{pane_active}#{window_active}\t#{session_attached}\t#{session_id}\t#{@squadrn_name}")
	if err != nil {
		return s, false
	}
	f := strings.SplitN(strings.TrimRight(string(out), "\n"), "\t", 4)
	if len(f) < 4 {
		return s, false
	}
	s.name = f[3]
	s.visible = f[0] == "11" && f[1] != "0"
	if s.visible {
		clients, _ := proc.Output(ctx, proc.Options{}, "tmux", "list-clients", "-t", f[2], "-F", "#{client_flags}")
		s.focused = strings.Contains(string(clients), "focused")
	}
	return s, true
}
