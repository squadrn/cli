//go:build !windows

package cli

import (
	"bytes"
	"os"
	"path/filepath"

	"github.com/squadrn/squadrn-cli/internal/fsx"
)

// writeCompletions generates completion scripts for every name into the shells' completion dirs
// that exist, and the login shell's even when it has to be created, and removes the ones squadrn
// generated for names that are gone.
func (a *app) writeCompletions(names, removedLinks []string) {
	use := a.root.Use
	defer func() { a.root.Use = use }()
	login := filepath.Base(os.Getenv("SHELL"))
	for shell, dir := range a.completionDirs() {
		created := false
		if !fsx.IsDir(dir) {
			if shell != login || os.MkdirAll(dir, 0o755) != nil {
				continue
			}
			created = true
		}
		for _, link := range removedLinks {
			file := completionFile(shell, dir, filepath.Base(link))
			if b, err := os.ReadFile(file); err == nil && generatedCompletion(b, filepath.Base(link)) {
				_ = os.Remove(file)
			}
		}
		for _, n := range names {
			a.root.Use = n
			var buf bytes.Buffer
			var err error
			switch shell {
			case "zsh":
				err = a.root.GenZshCompletion(&buf)
			case "bash":
				err = a.root.GenBashCompletionV2(&buf, true)
			case "fish":
				err = a.root.GenFishCompletion(&buf, true)
			}
			if err == nil {
				_ = fsx.WriteBytes(completionFile(shell, dir, n), buf.Bytes(), 0o644)
			}
		}
		hint := ""
		if created && shell == "zsh" {
			// zsh only reads completion dirs on its fpath; squadrn does not edit ~/.zshrc.
			hint = " (new: add to ~/.zshrc: fpath=(" + a.tilde(dir) + " $fpath); autoload -Uz compinit && compinit)"
		}
		a.out("  %s completion → %s%s\n", shell, a.tilde(dir), hint)
	}
}
