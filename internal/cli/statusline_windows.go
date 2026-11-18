//go:build windows

package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// statusLineWord quotes s as one word that Git Bash and PowerShell, whichever Claude Code uses,
// read the same way: a plain path (forward slashes, ~ for the home directory) or a single-quoted
// string without single quotes, which neither shell expands.
func statusLineWord(s, home string) (string, error) {
	if p := filepath.ToSlash(s); filepath.IsAbs(s) {
		if rel, err := filepath.Rel(home, s); err == nil && !strings.HasPrefix(rel, "..") {
			p = "~/" + filepath.ToSlash(rel)
		}
		if strings.Trim(p, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_./:~-") != "" {
			return "", fmt.Errorf("%s contains characters the status line cannot run on Windows", s)
		}
		return p, nil
	}
	if strings.Contains(s, "'") {
		return "", fmt.Errorf("cannot wrap %q: Git Bash and PowerShell quote ' differently; remove it from the status line command", s)
	}
	return "'" + s + "'", nil
}

// statusLineShell runs a status line command the way Claude Code does: Git Bash when Git for
// Windows provides it, PowerShell otherwise.
func statusLineShell(command string) (string, []string) {
	if git, err := exec.LookPath("git"); err == nil {
		root := filepath.Dir(filepath.Dir(git))
		for _, c := range []string{filepath.Join(root, "bin", "bash.exe"), filepath.Join(root, "usr", "bin", "bash.exe")} {
			if fi, err := os.Stat(c); err == nil && fi.Mode().IsRegular() {
				return c, []string{"-c", command}
			}
		}
	}
	return "powershell.exe", []string{"-NoProfile", "-Command", command}
}
