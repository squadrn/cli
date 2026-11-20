package claude

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/squadrn/squadrn/internal/provider"
)

// marketplace is the name the squadrn marketplace registers under (.claude-plugin/marketplace.json).
const marketplace = "squadrn"

func (c CLI) InstallPlugin(ctx context.Context, configDir, dir string) (bool, error) {
	if pluginRegistered(configDir) {
		return false, nil // account add re-runs stay fast: no CLI call when it is installed
	}
	return installPlugin(ctx, c, configDir, dir)
}

func (c CLI) UpdatePlugin(ctx context.Context, configDir string) error {
	return updatePlugin(ctx, c, configDir)
}

func (c CLI) RemovePlugin(ctx context.Context, configDir string) error {
	return removePlugin(ctx, c, configDir)
}

func (c CLI) PluginState(ctx context.Context, configDir string) (installed, enabled bool, err error) {
	return pluginState(ctx, c, configDir)
}

func (CLI) EnablePluginArgs() []string { return []string{"plugin", "enable", provider.Claude.Plugin} }

func (c CLI) HasMCP(ctx context.Context, configDir, name string) bool {
	return mcpConfigured(configDir, name) || hasMCP(ctx, c, configDir, name)
}

func (c CLI) AddMCP(ctx context.Context, configDir, name string, config []byte) error {
	return addMCP(ctx, c, configDir, name, config)
}

func hasMCP(ctx context.Context, r provider.Runner, configDir, name string) bool {
	_, err := r.Output(ctx, configDir, "mcp", "get", name)
	return err == nil
}

func addMCP(ctx context.Context, r provider.Runner, configDir, name string, config []byte) error {
	_, err := r.Output(ctx, configDir, "mcp", "add-json", "-s", "user", name, string(config))
	return err
}

// installPlugin registers the marketplace in dir unless it is known and installs the plugin
// unless it is installed. Both are read from the config dir's plugin registry first (a new
// account has none: nothing to list); the CLI is asked only when the registry is unreadable.
func installPlugin(ctx context.Context, r provider.Runner, configDir, dir string) (bool, error) {
	markets, plugins, ok := readRegistry(configDir)
	var err error
	if !ok {
		if markets, err = listNames(ctx, r, configDir, "name", "plugin", "marketplace", "list", "--json"); err != nil {
			return false, err
		}
	}
	if !markets[marketplace] {
		if _, err := r.Output(ctx, configDir, "plugin", "marketplace", "add", dir); err != nil {
			// The registry may have been stale or elsewhere: the add failing because the
			// marketplace is known is fine.
			if known, lerr := listNames(ctx, r, configDir, "name", "plugin", "marketplace", "list", "--json"); lerr != nil || !known[marketplace] {
				return false, err
			}
		}
	}
	if !ok {
		if plugins, err = listNames(ctx, r, configDir, "id", "plugin", "list", "--json"); err != nil {
			return false, err
		}
	}
	if plugins[provider.Claude.Plugin] {
		return false, nil
	}
	if _, err := r.Output(ctx, configDir, "plugin", "install", provider.Claude.Plugin, "--scope", "user"); err != nil {
		return false, err
	}
	return true, nil
}

// updatePlugin refreshes the marketplace and plugin after the files on disk changed.
func updatePlugin(ctx context.Context, r provider.Runner, configDir string) error {
	if _, err := r.Output(ctx, configDir, "plugin", "marketplace", "update", marketplace); err != nil {
		return err
	}
	_, err := r.Output(ctx, configDir, "plugin", "update", provider.Claude.Plugin)
	return err
}

// removePlugin uninstalls the plugin, then drops the marketplace whatever happened.
// removePlugin uninstalls the plugin and removes the marketplace registration, checking that it
// is gone: its files are deleted with the installation, so a registration left behind would
// point at nothing.
func removePlugin(ctx context.Context, r provider.Runner, configDir string) error {
	_, err := r.Output(ctx, configDir, "plugin", "uninstall", provider.Claude.Plugin)
	markets, lerr := listNames(ctx, r, configDir, "name", "plugin", "marketplace", "list", "--json")
	if lerr == nil && !markets[marketplace] {
		return err
	}
	if _, merr := r.Output(ctx, configDir, "plugin", "marketplace", "remove", marketplace); merr != nil {
		return errors.Join(err, fmt.Errorf("remove the %s marketplace: %w", marketplace, merr))
	}
	return err
}

func pluginState(ctx context.Context, r provider.Runner, configDir string) (installed, enabled bool, err error) {
	b, err := r.Output(ctx, configDir, "plugin", "list", "--json")
	if err != nil {
		return false, false, err
	}
	var plugins []struct {
		ID      string `json:"id"`
		Enabled bool   `json:"enabled"`
	}
	_ = json.Unmarshal(b, &plugins)
	for _, p := range plugins {
		if p.ID == provider.Claude.Plugin {
			return true, p.Enabled, nil
		}
	}
	return false, false, nil
}

// listNames runs a `... --json` listing and returns the set of values of field.
func listNames(ctx context.Context, r provider.Runner, configDir, field string, args ...string) (map[string]bool, error) {
	out, err := r.Output(ctx, configDir, args...)
	if err != nil {
		return nil, err
	}
	var items []map[string]any
	if err := json.Unmarshal(out, &items); err != nil {
		return nil, fmt.Errorf("parse claude %s: %w", strings.Join(args, " "), err)
	}
	set := make(map[string]bool, len(items))
	for _, it := range items {
		if v, ok := it[field].(string); ok {
			set[v] = true
		}
	}
	return set, nil
}

// configDirOrMain is the dir the CLI uses for configDir ("" is ~/.claude).
func configDirOrMain(configDir string) (string, bool) {
	if configDir != "" {
		return configDir, true
	}
	home, err := os.UserHomeDir()
	return filepath.Join(home, provider.Claude.MainDir), err == nil
}

// pluginRegistered reads Claude Code's plugin registry (internal: plugins/installed_plugins.json,
// {"plugins": {"<id>": [...]}}) for the squadrn plugin. Anything unexpected means "ask the CLI".
func pluginRegistered(configDir string) bool {
	dir, ok := configDirOrMain(configDir)
	if !ok {
		return false
	}
	b, err := os.ReadFile(filepath.Join(dir, "plugins", "installed_plugins.json"))
	if err != nil {
		return false
	}
	var reg struct {
		Plugins map[string]json.RawMessage `json:"plugins"`
	}
	if json.Unmarshal(b, &reg) != nil {
		return false
	}
	entry, ok := reg.Plugins[provider.Claude.Plugin]
	var installs []json.RawMessage
	return ok && json.Unmarshal(entry, &installs) == nil && len(installs) > 0
}

// readRegistry reads Claude Code's plugin registry in the config dir (internal:
// plugins/known_marketplaces.json, {"<name>": {...}}, and plugins/installed_plugins.json,
// {"plugins": {"<id>": [...]}}). A missing file is an empty set; ok is false when a file cannot
// be read or parsed, and the caller asks the CLI.
func readRegistry(configDir string) (markets, plugins map[string]bool, ok bool) {
	dir, ok := configDirOrMain(configDir)
	if !ok {
		return nil, nil, false
	}
	var known map[string]json.RawMessage
	if !readJSON(filepath.Join(dir, "plugins", "known_marketplaces.json"), &known) {
		return nil, nil, false
	}
	var reg struct {
		Plugins map[string][]json.RawMessage `json:"plugins"`
	}
	if !readJSON(filepath.Join(dir, "plugins", "installed_plugins.json"), &reg) {
		return nil, nil, false
	}
	markets = make(map[string]bool, len(known))
	for name := range known {
		markets[name] = true
	}
	plugins = make(map[string]bool, len(reg.Plugins))
	for id, installs := range reg.Plugins {
		plugins[id] = len(installs) > 0
	}
	return markets, plugins, true
}

// readJSON decodes path into v; a missing file leaves v empty and counts as read.
func readJSON(path string, v any) bool {
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return true
	}
	return err == nil && json.Unmarshal(b, v) == nil
}

// mcpConfigured reads the user-scope MCP servers in the account's .claude.json (internal; the
// main account's is ~/.claude.json). A miss falls back to the CLI.
func mcpConfigured(configDir, name string) bool {
	path := ""
	if configDir != "" {
		path = filepath.Join(configDir, provider.Claude.MCPConfig)
	} else if home, err := os.UserHomeDir(); err == nil {
		path = filepath.Join(home, provider.Claude.MCPConfig)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var doc struct {
		MCPServers map[string]json.RawMessage `json:"mcpServers"`
	}
	if json.Unmarshal(b, &doc) != nil {
		return false
	}
	_, ok := doc.MCPServers[name]
	return ok
}
