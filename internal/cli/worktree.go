package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"sprawling-agents/internal/gitver"
	"sprawling-agents/internal/provider"
	"sprawling-agents/internal/worktree"
)

// defaultSpec is the provider for commands that name no account.
func (a *app) defaultSpec() provider.Spec {
	if s, ok := provider.Get(a.store.DefaultProvider); ok {
		return s
	}
	return provider.Claude
}

// repoHere opens the repository of the working directory with spec's worktree layout.
func (a *app) repoHere(ctx context.Context, spec provider.Spec) (worktree.Repo, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return worktree.Repo{}, err
	}
	return a.openRepo(ctx, cwd, spec.WorktreeDir)
}

// openRepo opens the repository at dir after checking, once per run, that Git is new enough:
// an old Git gets an upgrade command instead of a raw git error.
func (a *app) openRepo(ctx context.Context, dir, subdir string) (worktree.Repo, error) {
	if !a.gitChecked {
		if err := gitver.Require(ctx, runtime.GOOS); err != nil {
			return worktree.Repo{}, err
		}
		a.gitChecked = true
	}
	return worktree.Open(ctx, dir, subdir)
}

func (a *app) wtCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "wt <name>",
		Short: "Run the agent in worktree <name> of this repository (Claude: .claude/worktrees/<name>)",
		Example: fmt.Sprintf(`  %[1]s wt feat-login             as main
  %[1]s personal wt fix-otp        as claude/personal
  %[1]s wt ls                      list worktrees
  %[1]s wt rm feat-login           remove one`, a.cmd),
		Args:              oneArg,
		ValidArgsFunction: a.completeWorktrees,
		RunE: func(_ *cobra.Command, args []string) error {
			if err := worktree.ValidateName(args[0]); err != nil {
				return err
			}
			acct, err := a.resolve(a.account)
			if err != nil {
				return err
			}
			if !acct.Spec.Caps.Worktree {
				return fmt.Errorf("%s: %s cannot create worktrees", acct.Label(), acct.Spec.Name)
			}
			repo, err := a.repoHere(context.Background(), acct.Spec)
			if err != nil {
				return err
			}
			ag, err := a.agent(acct.Spec)
			if err != nil {
				return err
			}
			if err := os.Chdir(repo.Root); err != nil {
				return err
			}
			a.banner(acct)
			return a.session(ag, acct, ag.WorktreeArgs(args[0]))
		},
	}
	cmd.AddCommand(a.wtLsCmd(), a.wtRmCmd())
	return cmd
}

type worktreeRow struct {
	Name    string `json:"name"`
	Branch  string `json:"branch"`
	Path    string `json:"path"`
	Dirty   bool   `json:"dirty"`
	Account string `json:"account,omitempty"` // running in a grid pane as this account
}

func (a *app) wtLsCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "ls",
		Short: "List the agent worktrees of this repository",
		Args:  noArgs,
		RunE: func(*cobra.Command, []string) error {
			ctx := context.Background()
			repo, err := a.repoHere(ctx, a.defaultSpec())
			if err != nil {
				return err
			}
			wts, err := repo.Agents(ctx)
			if err != nil {
				return err
			}
			running := map[string]string{}
			for _, p := range a.panes(repo.Root) {
				running[p.Name] = p.Account
			}
			rows := make([]worktreeRow, 0, len(wts))
			for _, w := range wts {
				name, _ := repo.Name(w)
				dirty, _ := worktree.Dirty(ctx, w.Path)
				rows = append(rows, worktreeRow{Name: name, Branch: w.Branch, Path: w.Path, Dirty: dirty, Account: running[name]})
			}
			if asJSON {
				return a.printJSON(rows)
			}
			if len(rows) == 0 {
				a.out("no worktrees under %s\n", a.tilde(repo.Base()))
				return nil
			}
			w := tabwriter.NewWriter(a.stdout, 0, 0, 2, ' ', 0)
			_, _ = fmt.Fprintln(w, "NAME\tBRANCH\tSTATE\tRUNNING AS")
			for _, r := range rows {
				state, as := "clean", r.Account
				if r.Dirty {
					state = "modified"
				}
				if as == "" {
					as = "-"
				}
				_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", r.Name, r.Branch, state, as)
			}
			return w.Flush()
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print JSON")
	return cmd
}

func (a *app) wtRmCmd() *cobra.Command {
	var force, yes bool
	cmd := &cobra.Command{
		Use:               "rm <name>",
		Short:             "Remove a worktree and its branch if merged (--force: even if modified or unmerged)",
		Args:              oneArg,
		ValidArgsFunction: a.completeWorktrees,
		RunE: func(_ *cobra.Command, args []string) error {
			if err := worktree.ValidateName(args[0]); err != nil {
				return err
			}
			ctx := context.Background()
			repo, err := a.repoHere(ctx, a.defaultSpec())
			if err != nil {
				return err
			}
			for _, p := range a.panes(repo.Root) {
				if p.Name == args[0] {
					return fmt.Errorf("%s is running in tmux pane %s; stop it first", args[0], p.ID)
				}
			}
			if force {
				if err := a.confirm(yes, "Remove %s with its uncommitted changes and unmerged commits", a.tilde(repo.Dir(args[0]))); err != nil {
					return err
				}
			}
			err = repo.RemoveOne(ctx, args[0], force)
			if errors.Is(err, worktree.ErrUnmerged) {
				a.out("removed worktree %s\n", args[0])
				a.warnf("%v; delete it with --force once you no longer need it", err)
				return nil
			}
			if err != nil {
				return err
			}
			a.out("removed worktree %s\n", args[0])
			return nil
		},
	}
	cmd.Flags().BoolVarP(&force, "force", "f", false, "also remove modified worktrees and unmerged branches")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "do not ask for confirmation with --force")
	return cmd
}

type gcRow struct {
	Name   string          `json:"name"`
	Branch string          `json:"branch"`
	Action worktree.Action `json:"action"`
	Reason string          `json:"reason"`
}

func (a *app) gcCmd() *cobra.Command {
	var yes, asJSON bool
	cmd := &cobra.Command{
		Use:   "gc",
		Short: "Remove clean worktrees that are merged or empty (dry run without --yes)",
		Args:  noArgs,
		RunE: func(*cobra.Command, []string) error {
			ctx := context.Background()
			repo, err := a.repoHere(ctx, a.defaultSpec())
			if err != nil {
				return err
			}
			if err := repo.Fetch(ctx); err != nil {
				a.warnf("fetch failed, using local refs: %v", err)
			}
			base, err := repo.DefaultBase(ctx)
			if err != nil {
				return err
			}
			cands, err := repo.Classify(ctx, base, repo.GitHubSquashMerged(ctx))
			if err != nil {
				return err
			}
			if asJSON && !yes {
				rows := make([]gcRow, len(cands))
				for i, c := range cands {
					name, _ := repo.Name(c.Worktree)
					rows[i] = gcRow{Name: name, Branch: c.Branch, Action: c.Action, Reason: c.Reason}
				}
				return a.printJSON(map[string]any{"base": base, "worktrees": rows})
			}
			if asJSON {
				removed := []string{}
				errs := []string{}
				_, rerrs := repo.RemoveAll(ctx, cands)
				for _, c := range cands {
					if _, err := os.Stat(c.Path); c.Action != worktree.Keep && errors.Is(err, os.ErrNotExist) {
						name, _ := repo.Name(c.Worktree)
						removed = append(removed, name)
					}
				}
				for _, e := range rerrs {
					errs = append(errs, e.Error())
				}
				if err := a.printJSON(map[string]any{"base": base, "removed": removed, "errors": errs}); err != nil {
					return err
				}
				if len(rerrs) > 0 {
					return errQuiet
				}
				return nil
			}
			a.out("base: %s\n", base)
			for _, c := range cands {
				name, _ := repo.Name(c.Worktree)
				a.out("  %-16s %-32s %s\n", c.Action, name, c.Reason)
			}
			if !yes {
				a.out("dry run; re-run with --yes to remove\n")
				return nil
			}
			removed, errs := repo.RemoveAll(ctx, cands)
			for _, e := range errs {
				a.errf("%s: %v\n", a.cmd, e)
			}
			a.out("removed %d worktree(s)\n", removed)
			if len(errs) > 0 {
				return fmt.Errorf("%d worktree(s) could not be removed", len(errs))
			}
			return nil
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "remove instead of only listing")
	cmd.Flags().BoolVar(&asJSON, "json", false, "print the plan as JSON")
	return cmd
}

func (a *app) depsCmd() *cobra.Command {
	return &cobra.Command{
		Use:               "deps <name>",
		Short:             "Install dependencies in a worktree (package manager from its lockfile)",
		Args:              oneArg,
		ValidArgsFunction: a.completeWorktrees,
		RunE: func(_ *cobra.Command, args []string) error {
			if err := worktree.ValidateName(args[0]); err != nil {
				return err
			}
			repo, err := a.repoHere(context.Background(), a.defaultSpec())
			if err != nil {
				return err
			}
			dir := repo.Dir(args[0])
			if _, err := os.Stat(dir); err != nil {
				return fmt.Errorf("worktree not found: %s", a.tilde(dir))
			}
			argv, err := worktree.InstallCommand(dir)
			if err != nil {
				return err
			}
			bin, err := exec.LookPath(argv[0])
			if err != nil {
				return fmt.Errorf("%s is required for this worktree but is not installed", argv[0])
			}
			cmd := exec.Command(bin, argv[1:]...)
			cmd.Dir, cmd.Stdin, cmd.Stdout, cmd.Stderr = dir, a.stdin, a.stdout, a.stderr
			if err := cmd.Run(); err != nil {
				var ee *exec.ExitError
				if errors.As(err, &ee) && ee.ExitCode() > 0 {
					return childExit{ee.ExitCode()}
				}
				return err
			}
			return nil
		},
	}
}

func (a *app) completeWorktrees(_ *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	ctx := context.Background()
	repo, err := a.repoHere(ctx, a.defaultSpec())
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	wts, _ := repo.Agents(ctx)
	var names []string
	for _, w := range wts {
		if n, ok := repo.Name(w); ok {
			names = append(names, n)
		}
	}
	return names, cobra.ShellCompDirectiveNoFileComp
}
