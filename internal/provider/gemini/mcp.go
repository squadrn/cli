package gemini

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/squadrn/squadrn-cli/internal/fsx"
)

// User-scope MCP servers are mcpServers in settings.json. Gemini's server definitions use the
// same env and headers keys as Claude Code's (where tokens live, so shared_mcp skips those), and
// squadrn copies them between Gemini accounts as they are.

func (c CLI) mcpServers(configDir string) (map[string]json.RawMessage, error) {
	doc, _, err := readDoc(c.settingsPath(configDir))
	if err != nil {
		return nil, err
	}
	out := map[string]json.RawMessage{}
	servers, _ := doc["mcpServers"].(map[string]any)
	for name, v := range servers {
		b, err := json.Marshal(v)
		if err != nil {
			return nil, err
		}
		out[name] = b
	}
	return out, nil
}

// ListMCP lists an account's servers ("" is main), each as its JSON definition.
func (c CLI) ListMCP(_ context.Context, configDir string) (map[string]json.RawMessage, error) {
	return c.mcpServers(configDir)
}

func (c CLI) HasMCP(_ context.Context, configDir, name string) bool {
	servers, err := c.mcpServers(configDir)
	_, ok := servers[name]
	return err == nil && ok
}

// AddMCP adds a server to an account's own settings.json (never main's, which may hold comments).
func (c CLI) AddMCP(_ context.Context, configDir, name string, config []byte) error {
	if configDir == "" {
		return fmt.Errorf("add MCP server %s: squadrn does not edit main's settings.json", name)
	}
	var def any
	if err := json.Unmarshal(config, &def); err != nil {
		return fmt.Errorf("MCP server %s: %w", name, err)
	}
	path := c.settingsPath(configDir)
	doc, _, err := readDoc(path)
	if err != nil {
		return err
	}
	servers, _ := doc["mcpServers"].(map[string]any)
	if servers == nil {
		servers = map[string]any{}
		doc["mcpServers"] = servers
	}
	servers[name] = def
	b, err := Settings{}.Encode(doc)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return fsx.WriteBytes(path, b, 0o600)
}
