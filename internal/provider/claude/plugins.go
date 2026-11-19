package claude

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/squadrn/squadrn/internal/provider"
)

// marketplace is the name the squadrn marketplace registers under (.claude-plugin/marketplace.json).
const marketplace = "squadrn"

func (c CLI) InstallPlugin(ctx context.Context, configDir, dir string) (bool, error) {
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
	return hasMCP(ctx, c, configDir, name)
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
// unless it is installed.
func installPlugin(ctx context.Context, r provider.Runner, configDir, dir string) (bool, error) {
	markets, err := listNames(ctx, r, configDir, "name", "plugin", "marketplace", "list", "--json")
	if err != nil {
		return false, err
	}
	if !markets[marketplace] {
		if _, err := r.Output(ctx, configDir, "plugin", "marketplace", "add", dir); err != nil {
			return false, err
		}
	}
	plugins, err := listNames(ctx, r, configDir, "id", "plugin", "list", "--json")
	if err != nil {
		return false, err
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
func removePlugin(ctx context.Context, r provider.Runner, configDir string) error {
	_, err := r.Output(ctx, configDir, "plugin", "uninstall", provider.Claude.Plugin)
	_, _ = r.Output(ctx, configDir, "plugin", "marketplace", "remove", marketplace)
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
