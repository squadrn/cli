//go:build !windows

package gemini

import "path/filepath"

// hookEntry is the squadrn plugin's hook entry point, relative to the marketplace dir. Gemini
// runs hook commands through sh -c on Unix.
const hookEntry = "plugin/bin/squadrn-hook"

// shell is the shell Gemini runs the agent's commands in (run_shell_command).
const shell = "sh"

// hookCommand is the settings.json command running squadrn's hook args from marketplace.
func hookCommand(marketplace, args string) string {
	return `"` + filepath.ToSlash(filepath.Join(marketplace, hookEntry)) + `" ` + args
}
