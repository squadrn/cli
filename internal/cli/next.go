package cli

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/squadrn/squadrn/internal/grid"
	"github.com/squadrn/squadrn/internal/notify"
)

func (a *app) nextCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "next",
		Short: "Jump to the tmux pane of the agent that waits for you (then a failed one)",
		Long: `Jump to the first tmux pane whose agent needs input, else the first whose turn failed.
Inside tmux it switches this client; outside it attaches to the pane's session.`,
		Example: fmt.Sprintf("  %s next", a.cmd),
		Args:    noArgs,
		RunE:    func(*cobra.Command, []string) error { return a.next() },
	}
}

func (a *app) next() error {
	if err := a.requireTmux("next"); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	panes, err := grid.Tmux{}.StatePanes(ctx)
	if err != nil {
		return err
	}
	p, ok := waiting(panes)
	if !ok {
		a.out("no agent is waiting for you\n")
		return nil
	}
	what := a.describePane(p)
	if insideTmux() {
		if err := showPane(ctx, "", p.ID); err != nil {
			return err
		}
		a.out("→ %s\n", what)
		return nil
	}
	if !a.interactive {
		return fmt.Errorf("%s waits; next needs a terminal to attach: tmux attach -t %s", what, p.Session)
	}
	if err := showPane(ctx, "", p.ID); err != nil && !errors.Is(err, errNoClient) {
		return err
	}
	return grid.Tmux{}.Attach(p.Session)
}

// waiting picks the first pane that needs input, else the first that failed.
func waiting(panes []grid.Pane) (grid.Pane, bool) {
	for _, state := range []notify.Kind{notify.Input, notify.Failed} {
		for _, p := range panes {
			if p.State == string(state) {
				return p, true
			}
		}
	}
	return grid.Pane{}, false
}

func (a *app) focusCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "focus <session>",
		Short:  "Show the tmux pane and terminal of an agent session (a notification's click action)",
		Args:   exactArgs(1),
		Hidden: true,
		RunE:   func(_ *cobra.Command, args []string) error { return a.focus(args[0]) },
	}
}

// focus shows the pane the session's hooks last reported and brings its terminal app forward.
func (a *app) focus(session string) error {
	ag, ok := notifyStore(a.store.Home).Load().Agents[session]
	if !ok {
		return fmt.Errorf("no running agent session %q is known", session)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var err error
	if ag.Pane != "" {
		err = showPane(ctx, ag.Socket, ag.Pane)
	}
	if ag.Bundle != "" {
		_ = activateApp(ctx, ag.Bundle)
	}
	return err
}

// describePane names a pane's agent: "claude/work · feat-a (input)". A pane outside a grid is
// named from what its hooks recorded.
func (a *app) describePane(p grid.Pane) string {
	account, name := p.Account, p.Name
	if name == "" {
		var newest notify.Agent
		for _, ag := range notifyStore(a.store.Home).Load().Agents {
			if ag.Pane == p.ID && ag.At.After(newest.At) {
				newest = ag
			}
		}
		e := notify.Event{Cwd: newest.Cwd}
		e.Locate(worktreeDirs())
		account, name = newest.Account, e.Place()
	}
	if name == "" {
		return fmt.Sprintf("%s %s (%s)", p.Session, p.ID, p.State)
	}
	return fmt.Sprintf("%s · %s (%s)", orDash(account), name, p.State)
}
