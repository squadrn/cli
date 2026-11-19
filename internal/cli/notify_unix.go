//go:build !darwin && !windows

package cli

import "os/exec"

// notifyCommand uses notify-send where it is installed; text goes in as arguments.
func notifyCommand(title, body string) []string {
	if path, err := exec.LookPath("notify-send"); err == nil {
		return []string{path, "--app-name=Claude", title, body}
	}
	return nil
}
