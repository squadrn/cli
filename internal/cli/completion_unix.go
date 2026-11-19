//go:build !windows

package cli

import (
	"bytes"
	"os"
	"path/filepath"

	"github.com/squadrn/squadrn/internal/fsx"
)

// writeCompletions generates completion scripts for every name into the shells' completion dirs
// that exist, and removes the ones squadrn generated for names that are gone.
func (a *app) writeCompletions(names, removedLinks []string) {
	use := a.root.Use
	defer func() { a.root.Use = use }()
	for shell, dir := range a.completionDirs() {
		if !fsx.IsDir(dir) {
			continue
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
		a.out("  %s completion → %s\n", shell, a.tilde(dir))
	}
}
