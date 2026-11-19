package cli

import (
	"bytes"
	"path/filepath"

	"github.com/squadrn/squadrn/internal/config"
)

// completionDirs are the per-user completion dirs of each shell; only existing ones are used.
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
