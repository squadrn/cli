//go:build windows

package gemini

import "path/filepath"

// hookExe is squadrn.exe, relative to the marketplace dir (as the Claude Windows hooks start it).
// Gemini runs hook commands through PowerShell, so the command starts it with the call operator.
const hookExe = `bin\squadrn.exe`

// shell is the shell Gemini runs the agent's commands in (run_shell_command): PowerShell.
const shell = "powershell"

// hookCommand is the settings.json command running squadrn's hook args from marketplace.
func hookCommand(marketplace, args string) string {
	return `& "` + filepath.Join(marketplace, hookExe) + `" hook ` + args
}
