package account

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"sprawling-agents/internal/config"
	"sprawling-agents/internal/provider"
)

const (
	MarketplaceName = "sprawl"
	PluginID        = "sprawl@sprawl"
)

// Provisioner prepares an account dir. It is idempotent: re-running repairs missing pieces.
type Provisioner struct {
	Store  Store
	Runner provider.Runner
	// SharedMCP is the allowlist of user-scope MCP servers copied from main.
	SharedMCP []string
	// Marketplace is a local dir holding .claude-plugin/marketplace.json; "" skips the plugin.
	Marketplace string
}

type Report struct {
	Linked          []string
	Unlinked        []string
	Conflicts       []string
	MCPAdded        []string
	MCPSkipped      []string
	MCPMissing      []string
	PluginInstalled bool
}

func (p Provisioner) Provision(ctx context.Context, a Account) (Report, error) {
	var r Report
	// Account dirs hold credentials and transcripts: private to the user.
	if err := os.MkdirAll(a.Dir, 0o700); err != nil {
		return r, err
	}
	if !a.IsMain() {
		if err := os.Chmod(a.Dir, 0o700); err != nil {
			return r, err
		}
	}
	if err := p.linkShared(a, &r); err != nil {
		return r, err
	}
	if err := p.copyMCP(ctx, a, &r); err != nil {
		return r, err
	}
	if !a.Spec.Caps.Plugins {
		return r, nil
	}
	installed, err := p.InstallPlugin(ctx, a.ConfigDir())
	if err != nil {
		return r, err
	}
	r.PluginInstalled = installed
	return r, nil
}

func (p Provisioner) linkShared(a Account, r *Report) error {
	main := p.Store.MainDir(a.Spec)
	for _, item := range a.Spec.Shared {
		if err := linkOne(filepath.Join(main, item), filepath.Join(a.Dir, item), r); err != nil {
			return err
		}
	}
	for _, dir := range slices.Sorted(maps.Keys(a.Spec.SharedPerEntry)) {
		entries, err := os.ReadDir(filepath.Join(main, dir))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		} else if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Join(a.Dir, dir), 0o755); err != nil {
			return err
		}
		for _, e := range entries {
			if slices.Contains(a.Spec.SharedPerEntry[dir], e.Name()) {
				continue
			}
			if err := linkOne(filepath.Join(main, dir, e.Name()), filepath.Join(a.Dir, dir, e.Name()), r); err != nil {
				return err
			}
		}
	}
	return nil
}

func linkOne(src, dst string, r *Report) error {
	if _, err := os.Stat(src); errors.Is(err, fs.ErrNotExist) {
		// main no longer has the item: drop the link we made to it rather than leave it dangling.
		if target, err := os.Readlink(dst); err == nil && target == src {
			if err := os.Remove(dst); err != nil {
				return err
			}
			r.Unlinked = append(r.Unlinked, dst)
		}
		return nil
	} else if err != nil {
		return err
	}
	if target, err := os.Readlink(dst); err == nil && target == src {
		return nil
	}
	if _, err := os.Lstat(dst); err == nil {
		r.Conflicts = append(r.Conflicts, dst)
		return nil
	}
	if err := os.Symlink(src, dst); err != nil {
		return fmt.Errorf("link %s: %w", dst, err)
	}
	r.Linked = append(r.Linked, dst)
	return nil
}

// copyMCP copies allowlisted servers from main. Entries carrying a non-empty env or headers are
// never copied, even when allowlisted, because that is where tokens live.
func (p Provisioner) copyMCP(ctx context.Context, a Account, r *Report) error {
	if len(p.SharedMCP) == 0 || !a.Spec.Caps.SharedMCP {
		return nil
	}
	servers, err := readMCPServers(p.Store.MCPConfig(a.Spec))
	if err != nil {
		return err
	}
	for _, name := range p.SharedMCP {
		if _, err := p.Runner.Output(ctx, a.ConfigDir(), "mcp", "get", name); err == nil {
			continue
		}
		raw, ok := servers[name]
		switch {
		case !ok:
			r.MCPMissing = append(r.MCPMissing, name)
		case carriesSecrets(raw):
			r.MCPSkipped = append(r.MCPSkipped, name)
		default:
			if _, err := p.Runner.Output(ctx, a.ConfigDir(), "mcp", "add-json", "-s", "user", name, string(raw)); err != nil {
				return err
			}
			r.MCPAdded = append(r.MCPAdded, name)
		}
	}
	return nil
}

func readMCPServers(path string) (map[string]json.RawMessage, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	var doc struct {
		MCPServers map[string]json.RawMessage `json:"mcpServers"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return doc.MCPServers, nil
}

func carriesSecrets(raw json.RawMessage) bool {
	var s struct {
		Env     map[string]any `json:"env"`
		Headers map[string]any `json:"headers"`
	}
	if json.Unmarshal(raw, &s) != nil {
		return true
	}
	return len(s.Env) > 0 || len(s.Headers) > 0
}

// InstallPlugin registers the local marketplace and installs the sprawl plugin for one config dir.
// It reports whether anything was installed; with no marketplace it does nothing.
func (p Provisioner) InstallPlugin(ctx context.Context, configDir string) (bool, error) {
	if p.Marketplace == "" {
		return false, nil
	}
	markets, err := listNames(ctx, p.Runner, configDir, "name", "plugin", "marketplace", "list", "--json")
	if err != nil {
		return false, err
	}
	if !markets[MarketplaceName] {
		if _, err := p.Runner.Output(ctx, configDir, "plugin", "marketplace", "add", p.Marketplace); err != nil {
			return false, err
		}
	}
	plugins, err := listNames(ctx, p.Runner, configDir, "id", "plugin", "list", "--json")
	if err != nil {
		return false, err
	}
	if plugins[PluginID] {
		return false, nil
	}
	if _, err := p.Runner.Output(ctx, configDir, "plugin", "install", PluginID, "--scope", "user"); err != nil {
		return false, err
	}
	return true, nil
}

// LegacyPluginIDs are the plugins previous product names installed (<name>@<name>).
func LegacyPluginIDs() []string {
	ids := make([]string, len(config.PreviousNames))
	for i, n := range config.PreviousNames {
		ids[i] = n + "@" + n
	}
	return ids
}

// RemoveLegacyPlugins uninstalls plugins and marketplaces of previous product names and
// returns the plugin ids it removed.
func (p Provisioner) RemoveLegacyPlugins(ctx context.Context, configDir string) ([]string, error) {
	plugins, err := listNames(ctx, p.Runner, configDir, "id", "plugin", "list", "--json")
	if err != nil {
		return nil, err
	}
	markets, err := listNames(ctx, p.Runner, configDir, "name", "plugin", "marketplace", "list", "--json")
	if err != nil {
		return nil, err
	}
	var removed []string
	for _, n := range config.PreviousNames {
		id := n + "@" + n
		if plugins[id] {
			if _, err := p.Runner.Output(ctx, configDir, "plugin", "uninstall", id); err != nil {
				return removed, err
			}
			removed = append(removed, id)
		}
		if markets[n] {
			if _, err := p.Runner.Output(ctx, configDir, "plugin", "marketplace", "remove", n); err != nil {
				return removed, err
			}
		}
	}
	return removed, nil
}

// UpdatePlugin refreshes the marketplace and plugin after the files on disk changed.
func (p Provisioner) UpdatePlugin(ctx context.Context, configDir string) error {
	if _, err := p.Runner.Output(ctx, configDir, "plugin", "marketplace", "update", MarketplaceName); err != nil {
		return err
	}
	_, err := p.Runner.Output(ctx, configDir, "plugin", "update", PluginID)
	return err
}

// listNames runs a claude `... --json` listing and returns the set of values of field.
func listNames(ctx context.Context, c provider.Runner, configDir, field string, args ...string) (map[string]bool, error) {
	out, err := c.Output(ctx, configDir, args...)
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
