package codex

import (
	"bytes"

	"github.com/BurntSushi/toml"
)

// Settings is Codex's config.toml as a generic document, so each account's copy can be merged
// from main's (ADR 0011).
type Settings struct{}

func (Settings) Decode(b []byte) (map[string]any, error) {
	doc := map[string]any{}
	if len(bytes.TrimSpace(b)) == 0 {
		return doc, nil
	}
	if _, err := toml.Decode(string(b), &doc); err != nil {
		return nil, err
	}
	return doc, nil
}

func (Settings) Encode(doc map[string]any) ([]byte, error) {
	var buf bytes.Buffer
	enc := toml.NewEncoder(&buf)
	enc.Indent = ""
	if err := enc.Encode(doc); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Deep: tables whose entries come from several sources, merged entry by entry.
func (Settings) Deep() []string {
	return []string{
		"analytics", "features", "feedback", "model_providers", "profiles", "projects",
		"sandbox_workspace_write", "shell_environment_policy", "tools", "tui",
	}
}

// Private are never taken from main: MCP servers (they may carry tokens; shared_mcp copies the
// allowlisted ones with `codex mcp add`), hook definitions and trust (keyed by each account's
// own hooks.json) and the credentials store, which squadrn sets itself.
func (Settings) Private() []string {
	return []string{"mcp_servers", "hooks", storeKey}
}
