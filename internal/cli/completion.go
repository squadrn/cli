package cli

import (
	"bytes"
	"path/filepath"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/squadrn/squadrn-cli/internal/config"
)

// completionForEveryName makes `completion <shell>` print one script per command name (the
// command and every alias), and an unknown shell a usage error.
func (a *app) completionForEveryName() {
	var comp *cobra.Command
	for _, c := range a.root.Commands() {
		if c.Name() == "completion" {
			comp = c
		}
	}
	if comp == nil {
		return
	}
	var shells []string
	for _, sub := range comp.Commands() {
		shells = append(shells, sub.Name())
		gen := sub.RunE
		if gen == nil {
			continue
		}
		sub.RunE = func(cmd *cobra.Command, args []string) error {
			use := a.root.Use
			defer func() { a.root.Use = use }()
			for _, n := range a.names() {
				a.root.Use = n
				if err := gen(cmd, args); err != nil {
					return err
				}
			}
			return nil
		}
	}
	comp.AddCommand(a.completionInstallCmd(), a.completionUninstallCmd())
	comp.Args = cobra.ArbitraryArgs
	comp.RunE = func(cmd *cobra.Command, args []string) error {
		if len(args) > 0 {
			return usagef("unknown shell %q (supported: %s)", args[0], strings.Join(shells, ", "))
		}
		return cmd.Help()
	}
}

// names is the configured command followed by its aliases.
func (a *app) names() []string {
	out := []string{a.cmd}
	for _, n := range a.cfg.Aliases {
		if !slices.Contains(out, n) {
			out = append(out, n)
		}
	}
	return out
}

// powerShellCompletionPath holds the PowerShell completion script for every command name.
func (a *app) powerShellCompletionPath() string {
	return filepath.Join(a.layout.DataDir, "completion.ps1")
}

// generatedCompletion recognises a cobra completion script for name (ours, not the user's).
func generatedCompletion(b []byte, name string) bool {
	return bytes.Contains(b, []byte(name)) && bytes.Contains(b, []byte("__complete"))
}

func (a *app) completionInstallCmd() *cobra.Command {
	var shell string
	var yes bool
	cmd := &cobra.Command{
		Use:   "install",
		Short: "Set up tab completion for your shell (asks before editing its startup file)",
		Long: `Set up tab completion for your login shell, or --shell: write the completion scripts where
the shell loads them. When it loads none of its completion dirs (zsh without one on its fpath,
bash without bash-completion), squadrn's lines go into its startup file, after asking (--yes:
without asking). Turns config completion back on, so updates keep the scripts current.`,
		Args: noArgs,
		RunE: func(*cobra.Command, []string) error { return a.installCompletion(shell, yes) },
	}
	cmd.Flags().StringVar(&shell, "shell", "", "the shell to set up (default: your login shell)")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "edit the shell's startup file without asking")
	_ = cmd.RegisterFlagCompletionFunc("shell", cobra.FixedCompletions(completionShells, cobra.ShellCompDirectiveNoFileComp))
	return cmd
}

func (a *app) completionUninstallCmd() *cobra.Command {
	var shell string
	cmd := &cobra.Command{
		Use:   "uninstall",
		Short: "Remove tab completion and stop updates from writing it again",
		Args:  noArgs,
		RunE:  func(*cobra.Command, []string) error { return a.uninstallCompletion(shell) },
	}
	cmd.Flags().StringVar(&shell, "shell", "", "only this shell (default: every shell)")
	_ = cmd.RegisterFlagCompletionFunc("shell", cobra.FixedCompletions(completionShells, cobra.ShellCompDirectiveNoFileComp))
	return cmd
}

// setCompletion saves config completion (auto or off) when it changes.
func (a *app) setCompletion(value string) error {
	if a.cfg.Completion == value || (value == config.CompletionAuto && a.cfg.CompletionEnabled()) {
		return nil
	}
	path, err := config.Path()
	if err != nil {
		return err
	}
	cfg, err := config.Read(path)
	if err != nil {
		return err
	}
	if err := cfg.Set("completion", value); err != nil {
		return err
	}
	if err := config.Save(path, cfg); err != nil {
		return err
	}
	a.cfg.Completion = value
	return nil
}
