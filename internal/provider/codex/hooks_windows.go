//go:build windows

package codex

import "path/filepath"

// hookExe is squadrn.exe next to the plugin, relative to the marketplace dir (as the Claude
// Windows hooks start it). Codex 0.171 runs a hook command as %COMSPEC% /C "<command>" and cmd.exe
// drops the outer quotes, so the quoted path survives; the sh shim cannot run there.
const hookExe = `bin\squadrn.exe`

// hookCommand is the hooks.json command running the guard from marketplace.
func hookCommand(marketplace string) string {
	return `"` + filepath.Join(marketplace, hookExe) + `" hook ` + guardArgs
}
