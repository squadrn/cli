//go:build !windows

package codex

import "path/filepath"

// hookEntry is the squadrn plugin's hook entry point, relative to the marketplace dir
// (.claude-plugin/marketplace.json: source ./plugin). Codex runs hook commands with $SHELL -lc.
const hookEntry = "plugin/bin/squadrn-hook"

// hookCommand is the hooks.json command running the guard from marketplace.
func hookCommand(marketplace string) string {
	return `"` + filepath.ToSlash(filepath.Join(marketplace, hookEntry)) + `" ` + guardArgs
}
