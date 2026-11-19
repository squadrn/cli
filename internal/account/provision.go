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

	"github.com/squadrn/squadrn/internal/fsx"
	"github.com/squadrn/squadrn/internal/provider"
)

// Provisioner prepares an account dir. It is idempotent: re-running repairs missing pieces.
type Provisioner struct {
	Store Store
	Agent provider.Agent
	// SharedMCP is the allowlist of user-scope MCP servers copied from main.
	SharedMCP []string
	// Marketplace is the local dir holding the squadrn plugin marketplace; "" skips the plugin.
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
		if err := Mark(a); err != nil {
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
	if p.Marketplace == "" {
		return r, nil
	}
	installed, err := p.Agent.InstallPlugin(ctx, a.ConfigDir(), p.Marketplace)
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
		if err := linkEntries(filepath.Join(main, dir), filepath.Join(a.Dir, dir), a.Spec.SharedPerEntry[dir], r); err != nil {
			return err
		}
	}
	return nil
}

// linkEntries links every entry of main's dir src into dst, except skip, and removes the links
// it made to entries main no longer has (a deleted skill).
func linkEntries(src, dst string, skip []string, r *Report) error {
	entries, err := os.ReadDir(src)
	if errors.Is(err, fs.ErrNotExist) {
		entries = nil
	} else if err != nil {
		return err
	}
	if len(entries) > 0 {
		if err := os.MkdirAll(dst, 0o755); err != nil {
			return err
		}
	}
	for _, e := range entries {
		if slices.Contains(skip, e.Name()) {
			continue
		}
		if err := linkOne(filepath.Join(src, e.Name()), filepath.Join(dst, e.Name()), r); err != nil {
			return err
		}
	}
	own, err := os.ReadDir(dst)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	for _, e := range own {
		p := filepath.Join(dst, e.Name())
		target, ok := linkTarget(p)
		if !ok || !fsx.Within(target, src) {
			continue // not a link squadrn made
		}
		if _, err := os.Stat(target); !errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err := os.Remove(p); err != nil {
			return err
		}
		r.Unlinked = append(r.Unlinked, p)
	}
	return nil
}

func linkOne(src, dst string, r *Report) error {
	if _, err := os.Stat(src); errors.Is(err, fs.ErrNotExist) {
		// main no longer has the item: drop the link we made to it rather than leave it dangling.
		if linksTo(dst, src) {
			if err := os.Remove(dst); err != nil {
				return err
			}
			r.Unlinked = append(r.Unlinked, dst)
		}
		return nil
	} else if err != nil {
		return err
	}
	if linksTo(dst, src) {
		return nil
	}
	if _, err := os.Lstat(dst); err == nil {
		r.Conflicts = append(r.Conflicts, dst)
		return nil
	}
	if err := link(src, dst); err != nil {
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
	servers, err := readMCPServers(p.Store.mcpConfig(a.Spec))
	if err != nil {
		return err
	}
	for _, name := range p.SharedMCP {
		if p.Agent.HasMCP(ctx, a.ConfigDir(), name) {
			continue
		}
		raw, ok := servers[name]
		switch {
		case !ok:
			r.MCPMissing = append(r.MCPMissing, name)
		case carriesSecrets(raw):
			r.MCPSkipped = append(r.MCPSkipped, name)
		default:
			if err := p.Agent.AddMCP(ctx, a.ConfigDir(), name, raw); err != nil {
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

// Refresh is what RefreshShared found that the user should know about.
type Refresh struct {
	// Conflicts are copies of main's files (Windows without links) edited in the account.
	Conflicts []string
	// Replaced are shared items the account has its own file or dir for instead of the link.
	Replaced []string
}

// RefreshShared keeps account a following main before a session: per-entry links (skills) are
// added for new entries and removed for deleted ones, copies of main's files (where links are
// not available) are brought up to date. Items it must not touch are reported.
func (s Store) RefreshShared(a Account) Refresh {
	var out Refresh
	if a.IsMain() || !fsx.IsDir(a.Dir) {
		return out
	}
	main := s.MainDir(a.Spec)
	sources := map[string]string{}
	for _, item := range a.Spec.Shared {
		sources[item] = filepath.Join(main, item)
	}
	out.Conflicts = refreshCopies(a.Dir, sources)
	for _, dir := range slices.Sorted(maps.Keys(a.Spec.SharedPerEntry)) {
		var r Report
		src, dst := filepath.Join(main, dir), filepath.Join(a.Dir, dir)
		_ = linkEntries(src, dst, a.Spec.SharedPerEntry[dir], &r)
		entries, _ := os.ReadDir(src)
		perEntry := map[string]string{}
		for _, e := range entries {
			perEntry[e.Name()] = filepath.Join(src, e.Name())
		}
		out.Conflicts = append(out.Conflicts, refreshCopies(dst, perEntry)...)
	}
	for _, item := range a.Spec.Shared {
		dst, src := filepath.Join(a.Dir, item), sources[item]
		fi, err := os.Lstat(dst)
		if err != nil || (!fi.Mode().IsRegular() && !fi.IsDir()) || slices.Contains(out.Conflicts, dst) {
			continue
		}
		if _, err := os.Stat(src); err == nil && !linksTo(dst, src) {
			out.Replaced = append(out.Replaced, dst)
		}
	}
	return out
}
