//go:build !windows

package doctor

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
)

// executable: the execute bit is set.
func executable(_ string, fi fs.FileInfo) bool { return fi.Mode().Perm()&0o111 != 0 }

// private: no permissions for group or others.
func private(fi fs.FileInfo) bool { return fi.Mode().Perm()&0o077 == 0 }

// PathFix is the command that puts dir on PATH (shown with the home directory as ~).
func PathFix(dir string) string { return `export PATH="` + dir + `:$PATH"` }

func (e Env) pathFix() string { return PathFix(e.tilde(e.Layout.BinDir)) }

// onPath lists distinct executables named name across PATH.
func onPath(name string) []string {
	var out []string
	seen := map[string]bool{}
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		p := filepath.Join(dir, name)
		if fi, err := os.Stat(p); err != nil || fi.IsDir() || !executable(p, fi) {
			continue
		}
		if real := resolve(p); !seen[real] {
			seen[real] = true
			out = append(out, p)
		}
	}
	return out
}

// platform has no extra checks on Unix.
func (Env) platform(context.Context) []Check { return nil }

// powerShellOnly: Unix agents always use the shell tool.
func powerShellOnly() bool { return false }
