package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/squadrn/squadrn/internal/gitver"
	"github.com/squadrn/squadrn/internal/provider"
	"github.com/squadrn/squadrn/internal/worktree"
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
		if err := gitver.Require(ctx); err != nil {
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
		Example: fmt.Sprintf(`  %[1]s wt feat-login           as main
  %[1]s personal wt fix-otp      as claude/personal
  %[1]s wt ls                    list worktrees
  %[1]s wt rm feat-login         remove one`, a.cmd),
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
			return a.session(ag, acct, ag.WorktreeArgs(args[0]), args[0])
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
	var force, forceBranch, yes bool
	cmd := &cobra.Command{
		Use:   "rm <name>",
		Short: "Remove a worktree and its branch if merged",
		Long: `Remove a worktree and its branch if merged. --force also removes a modified worktree,
--force-branch also deletes an unmerged branch; both ask first (-y skips).`,
		Args:              oneArg,
		ValidArgsFunction: a.completeWorktrees,
		RunE: func(_ *cobra.Command, args []string) error {
			name := args[0]
			if err := worktree.ValidateName(name); err != nil {
				return err
			}
			ctx := context.Background()
			repo, err := a.repoHere(ctx, a.defaultSpec())
			if err != nil {
				return err
			}
			w, err := repo.Find(ctx, name)
			if errors.Is(err, worktree.ErrNotFound) && repo.BranchExists(ctx, worktree.AgentBranch(name)) {
				return a.rmOrphanBranch(ctx, repo, name, forceBranch, yes)
			} else if err != nil {
				return err
			}
			for _, p := range a.panes(repo.Root) {
				if p.Name == name {
					return fmt.Errorf("%s is running in tmux pane %s; stop it first", name, p.ID)
				}
			}
			// Decide before removing anything, so a refusal leaves the worktree and branch as they were.
			var risks []string
			if used := repo.InUse(w); len(used) > 0 {
				if !force {
					return fmt.Errorf("%s is in use by %s; stop it first (or use --force)", name, a.sessionDesc(used[0]))
				}
				risks = append(risks, "in use by "+a.sessionDesc(used[0]))
			}
			if dirty, err := worktree.Dirty(ctx, w.Path); err != nil || dirty {
				if !force {
					return fmt.Errorf("%s %w (or its status is unavailable); commit or stash them, or remove it anyway with --force", name, worktree.ErrDirty)
				}
				risks = append(risks, "uncommitted changes")
			}
			if w.Branch == worktree.AgentBranch(name) {
				unmerged, err := repo.Unmerged(ctx, w.Branch)
				if err != nil {
					return err
				}
				if unmerged && !forceBranch {
					return fmt.Errorf("%s has commits that are not merged (branch %s); merge them first, or remove both with --force-branch", name, w.Branch)
				}
				if unmerged {
					risks = append(risks, "unmerged commits on "+w.Branch)
				}
			}
			if len(risks) > 0 {
				if err := a.confirm(yes, "Remove %s (%s)", name, strings.Join(risks, ", ")); err != nil {
					return err
				}
			}
			ignored, _ := worktree.Ignored(ctx, w.Path)
			deleted, err := repo.Remove(ctx, w, force, forceBranch)
			if err != nil && !errors.Is(err, worktree.ErrUnmerged) {
				return err
			}
			a.out("removed worktree %s%s\n", name, ignoredNote(ignored))
			if deleted != "" {
				a.out("deleted branch %s\n", deleted)
			}
			if err != nil {
				a.warnf("%v; delete it with --force-branch once you no longer need it", err)
			}
			return nil
		},
	}
	cmd.Flags().BoolVarP(&force, "force", "f", false, "also remove a modified worktree (or one containing submodules, or in use)")
	cmd.Flags().BoolVar(&forceBranch, "force-branch", false, "also delete the branch when it is not merged")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "do not ask for confirmation with --force or --force-branch")
	return cmd
}

// rmOrphanBranch handles `wt rm <name>` when the worktree is gone but its agent branch is left.
func (a *app) rmOrphanBranch(ctx context.Context, repo worktree.Repo, name string, forceBranch, yes bool) error {
	branch := worktree.AgentBranch(name)
	if !forceBranch {
		return fmt.Errorf("worktree %s is gone, but its branch %s is left; delete it with --force-branch", name, branch)
	}
	if err := a.confirm(yes, "Delete branch %s even if unmerged", branch); err != nil {
		return err
	}
	if err := repo.DeleteBranch(ctx, name); err != nil {
		return err
	}
	a.out("deleted branch %s\n", branch)
	return nil
}

func (a *app) gcJSON(ctx context.Context, repo worktree.Repo, base string, cands []worktree.Candidate, yes, force bool) error {
	rep := gcReport{Base: base, Worktrees: make([]gcRow, len(cands)), Errors: []string{}}
	var removed []string
	var errs []error
	if yes {
		removed, errs = repo.RemoveAll(ctx, cands, force)
	}
	for i, c := range cands {
		name, _ := repo.Name(c.Worktree)
		rep.Worktrees[i] = gcRow{Name: name, Branch: c.Branch, Action: c.Action, Reason: a.gcReason(c), Ignored: c.Ignored, Removed: slices.Contains(removed, name)}
	}
	for _, e := range errs {
		rep.Errors = append(rep.Errors, e.Error())
	}
	if err := a.printJSON(rep); err != nil {
		return err
	}
	if len(errs) > 0 {
		return errQuiet
	}
	return nil
}

// gcReason says why gc keeps or removes a worktree.
func (a *app) gcReason(c worktree.Candidate) string {
	if len(c.InUse) > 0 {
		return "in use by " + a.sessionDesc(c.InUse[0])
	}
	return c.Reason
}

// ignoredNote tells that ignored files (build output, dependencies) went with a worktree.
func ignoredNote(n int) string {
	if n == 0 {
		return ""
	}
	return fmt.Sprintf(" (with %d ignored path(s))", n)
}

type gcRow struct {
	Name    string          `json:"name"`
	Branch  string          `json:"branch"`
	Action  worktree.Action `json:"action"`
	Reason  string          `json:"reason"`
	Ignored int             `json:"ignored"`
	Removed bool            `json:"removed"`
}

// gcReport is gc --json: the plan, and with --yes what was removed and what failed.
type gcReport struct {
	Base      string   `json:"base"`
	Worktrees []gcRow  `json:"worktrees"`
	Errors    []string `json:"errors"`
}

func (a *app) gcCmd() *cobra.Command {
	var yes, asJSON, force bool
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
			if asJSON {
				return a.gcJSON(ctx, repo, base, cands, yes, force)
			}
			a.out("base: %s\n", base)
			for _, c := range cands {
				name, _ := repo.Name(c.Worktree)
				a.out("  %-16s %-32s %s%s\n", c.Action, name, a.gcReason(c), ignoredNote(c.Ignored))
			}
			if !yes {
				a.out("dry run; re-run with --yes to remove\n")
				return nil
			}
			removed, errs := repo.RemoveAll(ctx, cands, force)
			for _, e := range errs {
				a.errf("%s: %v\n", a.cmd, e)
			}
			a.out("removed %d worktree(s)\n", len(removed))
			if len(errs) > 0 {
				return fmt.Errorf("%d worktree(s) cannot be removed", len(errs))
			}
			return nil
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "remove instead of only listing")
	cmd.Flags().BoolVar(&asJSON, "json", false, "print JSON (with --yes: also what was removed)")
	cmd.Flags().BoolVarP(&force, "force", "f", false, "let git remove clean worktrees it refuses (those containing submodules)")
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
