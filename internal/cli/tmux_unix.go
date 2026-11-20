//go:build !windows

package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"

	"github.com/squadrn/squadrn/internal/grid"
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
	pane, tmux := os.Getenv("TMUX_PANE"), os.Getenv("TMUX")
	if pane == "" || tmux == "" {
		return s, false
	}
	out, err := proc.Output(ctx, proc.Options{}, "tmux", "display-message", "-p", "-t", pane,
		"#{pane_active}#{window_active}\t#{session_attached}\t#{session_id}\t#{pane_tty}\t#{@squadrn_name}")
	if err != nil {
		return s, false
	}
	f := strings.SplitN(strings.TrimRight(string(out), "\n"), "\t", 5)
	if len(f) < 5 {
		return s, false
	}
	s.id, s.tty, s.name = pane, f[3], f[4]
	s.socket, _, _ = strings.Cut(tmux, ",")
	s.visible = f[0] == "11" && f[1] != "0"
	if s.visible {
		clients, _ := proc.Output(ctx, proc.Options{}, "tmux", "list-clients", "-t", f[2], "-F", "#{client_flags}")
		s.focused = strings.Contains(string(clients), "focused")
	}
	return s, true
}

// setPaneState shows the agent's state on its pane (the grid's border colours it); best effort.
func setPaneState(ctx context.Context, pane, state string) {
	_, _ = proc.Output(ctx, proc.Options{}, "tmux", "set-option", "-p", "-t", pane, grid.OptState, state)
}

// ringBell rings the bell in the pane: tmux flags its window and a client can alert.
func ringBell(_ context.Context, tty string) error {
	if tty == "" {
		return errors.New("no pane tty")
	}
	f, err := os.OpenFile(tty, os.O_WRONLY|syscall.O_NOCTTY, 0)
	if err != nil {
		return err
	}
	_, err = f.WriteString("\a")
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

// showPane makes pane the current pane of its window and session and switches the most
// recently active client to it (or, inside tmux, this client). socket "" is the default server.
func showPane(ctx context.Context, socket, pane string) error {
	tmux := func(args ...string) ([]byte, error) {
		if socket != "" {
			args = append([]string{"-S", socket}, args...)
		}
		return proc.Output(ctx, proc.Options{}, "tmux", args...)
	}
	if _, err := tmux("select-window", "-t", pane, ";", "select-pane", "-t", pane); err != nil {
		return err
	}
	if os.Getenv("TMUX") != "" && socket == "" {
		_, err := tmux("switch-client", "-t", pane)
		return err
	}
	out, err := tmux("list-clients", "-F", "#{client_activity} #{client_name}")
	if err != nil {
		return err
	}
	client, newest := "", -1
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		at, name, ok := strings.Cut(line, " ")
		if n, err := strconv.Atoi(at); ok && err == nil && n > newest {
			client, newest = name, n
		}
	}
	if client == "" {
		return errNoClient
	}
	_, err = tmux("switch-client", "-c", client, "-t", pane)
	return err
}

// insideTmux reports whether squadrn runs in a tmux client.
func insideTmux() bool { return os.Getenv("TMUX") != "" }
