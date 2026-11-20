package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/charmbracelet/x/term"
	"github.com/spf13/cobra"

	"github.com/squadrn/squadrn/internal/account"
	"github.com/squadrn/squadrn/internal/fsx"
	"github.com/squadrn/squadrn/internal/grid"
	"github.com/squadrn/squadrn/internal/proc"
	"github.com/squadrn/squadrn/internal/worktree"
)

func (a *app) gridCmd() *cobra.Command {
	var spread bool
	cmd := &cobra.Command{
		Use:   "grid <repo> [<account>:]<name>...",
		Short: "Run agents side by side in a tmux grid, one worktree each",
		Long: `Run agents side by side in a tmux grid for <repo>: one pane per agent, each in its own
worktree. Running grid again for the same repository adds panes.`,
		Example: fmt.Sprintf(`  %[1]s grid ~/repo feat-a personal:fix-b          per-agent account (default: main or -a)
  %[1]s grid ~/repo feat-a claude/personal:fix-b   account as provider/name
  %[1]s grid ~/repo feat-1 feat-2 --spread         round-robin over logged-in accounts
  %[1]s grid ls | attach [repo] | stop [repo]`, a.cmd),
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) < 2 {
				return usagef("usage: %s", cmd.UseLine())
			}
			return nil
		},
		RunE: func(_ *cobra.Command, args []string) error {
			if spread && a.account != "" {
				return usagef("--spread picks the accounts itself; it cannot be combined with an account")
			}
			return a.grid(args[0], args[1:], a.account, spread)
		},
	}
	cmd.Flags().BoolVar(&spread, "spread", false, "spread unprefixed names over logged-in accounts")
	cmd.AddCommand(a.gridLsCmd(), a.gridAttachCmd(), a.gridStopCmd())
	return cmd
}

func (a *app) grid(repoArg string, specs []string, acctInput string, spread bool) error {
	if err := a.requireTmux("grid"); err != nil {
		return err
	}
	ctx := context.Background()
	var pool []string
	def := account.Main
	if spread {
		var err error
		if pool, err = a.loggedIn(); err != nil {
			return err
		}
	} else {
		acct, err := a.resolve(acctInput)
		if err != nil {
			return err
		}
		def = acct.Label()
	}
	agents, err := grid.Assign(specs, def, pool)
	if err != nil {
		return err
	}
	var repo worktree.Repo
	for i, ag := range agents {
		acct, err := a.store.Get(ag.Account)
		if err != nil {
			return err
		}
		if _, err := a.agent(acct.Spec); err != nil {
			return err
		}
		if !acct.Spec.Caps.Worktree {
			return fmt.Errorf("%s: %s cannot create worktrees", acct.Label(), acct.Spec.Name)
		}
		if repo.Root == "" {
			if repo, err = a.openRepo(ctx, repoArg, acct.Spec.WorktreeDir); err != nil {
				return err
			}
		}
		agents[i].Account = acct.Label()
	}

	// One grid run at a time per repository: another one could start the same worktree between
	// the check for running panes and the start.
	common, err := repo.CommonDir(ctx)
	if err != nil {
		return err
	}
	unlock, err := fsx.Lock(filepath.Join(common, "squadrn-grid.lock"), 30*time.Second)
	if err != nil {
		return fmt.Errorf("another %s grid is starting agents in this repository: %w", a.cmd, err)
	}
	session, err := a.startGrid(ctx, repo, agents)
	unlock()
	if err != nil {
		return err
	}
	if !a.interactive {
		return nil
	}
	return grid.Tmux{}.Attach(session)
}

// startGrid starts the agents that are not running yet in repo's grid and returns its session.
func (a *app) startGrid(ctx context.Context, repo worktree.Repo, agents []grid.Agent) (string, error) {
	t := grid.Tmux{}
	session, err := t.FindSession(ctx, repo.Root)
	if err != nil {
		return "", err
	}
	running := map[string]bool{}
	if session != "" {
		panes, err := t.Panes(ctx, session)
		if err != nil {
			return "", err
		}
		for _, p := range panes {
			running[p.Name] = true
		}
	}
	cols, rows := 200, 50
	if w, h, err := term.GetSize(os.Stdout.Fd()); err == nil {
		cols, rows = w, h
	}
	started := 0
	for _, ag := range agents {
		if running[ag.Name] {
			a.warnf("%q is already running in %s; skipped", ag.Name, session)
			continue
		}
		command := proc.ShellQuote(selfPath(), "-a", ag.Account, "wt", ag.Name)
		if session, err = t.Start(ctx, a.cmd, repo.Root, repo.Root, cols, rows, ag, command); err != nil {
			return "", err
		}
		started++
		a.out("  %-20s → %s\n", ag.Account, ag.Name)
	}
	if session == "" {
		return "", errors.New("no agents were started")
	}
	if started > 0 {
		a.out("grid %s (%s grid attach)\n", session, a.cmd)
	}
	return session, nil
}

// loggedIn returns the labels of accounts that are signed in, for --spread.
func (a *app) loggedIn() ([]string, error) {
	accts, err := a.store.List()
	if err != nil {
		return nil, err
	}
	var names []string
	for i, res := range a.statuses(accts, false) {
		if res.Err == nil && res.Status.LoggedIn {
			names = append(names, accts[i].Label())
		}
	}
	if len(names) == 0 {
		return nil, fmt.Errorf("--spread: no logged-in accounts (see: %s account ls)", a.cmd)
	}
	return names, nil
}

// gridSession finds the grid of the repository at dir ("" = the working directory).
func (a *app) gridSession(ctx context.Context, dir string) (string, error) {
	if dir == "" {
		dir = "."
	}
	repo, err := a.openRepo(ctx, dir, a.defaultSpec().WorktreeDir)
	if err != nil {
		return "", err
	}
	session, err := grid.Tmux{}.FindSession(ctx, repo.Root)
	if err != nil {
		return "", err
	}
	if session == "" {
		return "", fmt.Errorf("no grid is running for %s", a.tilde(repo.Root))
	}
	return session, nil
}

func optionalRepo(cmd *cobra.Command, args []string) error {
	if len(args) > 1 {
		return usagef("usage: %s", cmd.UseLine())
	}
	return nil
}

func firstOr(args []string) string {
	if len(args) > 0 {
		return args[0]
	}
	return ""
}

func (a *app) gridAttachCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "attach [repo]",
		Short: "Attach to the grid of a repository (default: this one)",
		Args:  optionalRepo,
		RunE: func(_ *cobra.Command, args []string) error {
			if err := a.requireTmux("grid attach"); err != nil {
				return err
			}
			session, err := a.gridSession(context.Background(), firstOr(args))
			if err != nil {
				return err
			}
			if !a.interactive {
				return fmt.Errorf("grid attach needs a terminal; run it in one: tmux attach -t %s", session)
			}
			return grid.Tmux{}.Attach(session)
		},
	}
}

func (a *app) gridStopCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "stop [repo]",
		Short: "Stop the grid of a repository and every agent in it (worktrees are kept)",
		Args:  optionalRepo,
		RunE: func(_ *cobra.Command, args []string) error {
			if err := a.requireTmux("grid stop"); err != nil {
				return err
			}
			ctx := context.Background()
			session, err := a.gridSession(ctx, firstOr(args))
			if err != nil {
				return err
			}
			panes, _ := grid.Tmux{}.Panes(ctx, session)
			if err := a.confirm(yes, "Stop grid %s and its %d agent(s)", session, len(panes)); err != nil {
				return err
			}
			if err := (grid.Tmux{}).Kill(ctx, session); err != nil {
				return err
			}
			a.out("stopped %s\n", session)
			return nil
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "do not ask for confirmation")
	return cmd
}

func (a *app) gridLsCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "ls",
		Short: "List running grids",
		Args:  noArgs,
		RunE: func(*cobra.Command, []string) error {
			if err := a.requireTmux("grid ls"); err != nil {
				return err
			}
			sessions, err := grid.Tmux{}.Sessions(context.Background())
			if err != nil {
				return err
			}
			if asJSON {
				if sessions == nil {
					sessions = []grid.Session{}
				}
				return a.printJSON(sessions)
			}
			if len(sessions) == 0 {
				a.out("no running grids\n")
				return nil
			}
			w := tabwriter.NewWriter(a.stdout, 0, 0, 2, ' ', 0)
			_, _ = fmt.Fprintln(w, "SESSION\tREPO\tPANES\tATTACHED")
			for _, s := range sessions {
				_, _ = fmt.Fprintf(w, "%s\t%s\t%d\t%s\n", s.Name, a.tilde(s.Repo), s.Panes, yesNo(s.Attached))
			}
			return w.Flush()
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print JSON")
	return cmd
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

func (a *app) psCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "ps",
		Short: "List running agents: account, worktree, state, where",
		Args:  noArgs,
		RunE:  func(*cobra.Command, []string) error { return a.ps(asJSON) },
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print JSON")
	return cmd
}

// panes lists agent panes of root's grid; none when tmux is missing or no grid runs.
func (a *app) panes(root string) []grid.Pane {
	if !hasTmux() {
		return nil
	}
	ctx := context.Background()
	t := grid.Tmux{}
	session, err := t.FindSession(ctx, root)
	if err != nil || session == "" {
		return nil
	}
	panes, _ := t.Panes(ctx, session)
	return panes
}

// paneIn returns the pane running worktree (or account label) key in the grid of dir's repository.
func (a *app) paneIn(dir, key string) string {
	repo, err := worktree.Open(context.Background(), dir, a.defaultSpec().WorktreeDir)
	if err != nil {
		return ""
	}
	for _, p := range a.panes(repo.Root) {
		if p.Name == key || p.Account == key {
			return p.ID
		}
	}
	return ""
}

// anyPane returns a pane of any grid running as account label.
func (a *app) anyPane(label string) string {
	if !hasTmux() {
		return ""
	}
	panes, _ := grid.Tmux{}.Panes(context.Background(), "")
	for _, p := range panes {
		if p.Account == label {
			return p.ID
		}
	}
	return ""
}

// selfPath is how panes re-invoke squadrn. It keeps the invoked name (not the symlink target),
// so an ~/.local/bin link keeps working after the binary it points to is replaced.
func selfPath() string {
	p := os.Args[0]
	if !strings.ContainsRune(p, filepath.Separator) {
		if found, err := exec.LookPath(p); err == nil {
			p = found
		}
	}
	if abs, err := filepath.Abs(p); err == nil {
		return abs
	}
	return p
}
