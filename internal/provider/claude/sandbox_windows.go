//go:build windows

package claude

// sandboxAvailable: native Windows has no Claude Code sandbox, so strict cannot require one.
const sandboxAvailable = false
