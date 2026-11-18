//go:build !windows

package cli

import (
	"os"
	"strings"
)

// statusLineWord quotes s as one word for the shell Claude Code runs status line commands in.
func statusLineWord(s, _ string) (string, error) {
	if s != "" && strings.Trim(s, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_./:@%+=,-") == "" {
		return s, nil
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'", nil
}

// statusLineShell runs a status line command the way Claude Code does: through the user's shell.
func statusLineShell(command string) (string, []string) {
	sh := os.Getenv("SHELL")
	if sh == "" {
		sh = "/bin/sh"
	}
	return sh, []string{"-c", command}
}
