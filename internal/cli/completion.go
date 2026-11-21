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

// completionDirs are the per-user completion dirs of each shell; existing ones are used, and the
// login shell's.
func (a *app) completionDirs() map[string]string {
	dirs := config.DirsFor(a.store.Home, "")
	return map[string]string{
		"zsh":  filepath.Join(a.store.Home, ".zsh", "completions"),
		"bash": filepath.Join(dirs.Data, "bash-completion", "completions"),
		"fish": filepath.Join(dirs.Config, "fish", "completions"),
	}
}

func completionFile(shell, dir, name string) string {
	switch shell {
	case "zsh":
		return filepath.Join(dir, "_"+name)
	case "fish":
		return filepath.Join(dir, name+".fish")
	}
	return filepath.Join(dir, name)
}

// powerShellCompletionPath holds the PowerShell completion script for every command name.
func (a *app) powerShellCompletionPath() string {
	return filepath.Join(a.layout.DataDir, "completion.ps1")
}

// generatedCompletion recognises a cobra completion script for name (ours, not the user's).
func generatedCompletion(b []byte, name string) bool {
	return bytes.Contains(b, []byte(name)) && bytes.Contains(b, []byte("__complete"))
}
