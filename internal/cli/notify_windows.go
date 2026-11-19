//go:build windows

package cli

// notifyCommand: no desktop notifications on Windows yet.
func notifyCommand(string, string) []string { return nil }
