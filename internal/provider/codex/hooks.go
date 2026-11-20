package codex

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/squadrn/squadrn/internal/fsx"
)

// The push guard's layer 1 is a PreToolUse hook in $CODEX_HOME/hooks.json running the squadrn
// plugin's hook entry point. Codex runs a user hook only once its exact definition is trusted:
// squadrn records that trust the way Codex's /hooks review does, through the app server
// (hooks/list gives the hook's key and current hash, config/batchWrite stores
// hooks.state.<key>.trusted_hash in config.toml), so the hash is always Codex's own.

const (
	hooksFile = "hooks.json"
	// guardArgs: Codex hooks cannot ask (permissionDecision "ask" is not supported and lets the
	// call through), so the guard denies what it would ask about.
	guardArgs    = "guard-git --no-ask"
	hookTimeout  = 10
	trustTimeout = 15 * time.Second
)

// ours reports whether a hooks.json command is squadrn's guard, from any install location and
// in either platform's form.
func ours(cmd string) bool {
	return (strings.Contains(cmd, "squadrn-hook") || strings.Contains(cmd, "squadrn.exe")) && strings.Contains(cmd, "guard-git")
}

// hookDoc is hooks.json; unknown fields are kept as they are.
type hookDoc map[string]json.RawMessage

func readHooks(path string) (hookDoc, map[string][]json.RawMessage, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return hookDoc{}, map[string][]json.RawMessage{}, nil
	} else if err != nil {
		return nil, nil, err
	}
	doc := hookDoc{}
	if err := json.Unmarshal(b, &doc); err != nil {
		return nil, nil, fmt.Errorf("%s is not valid JSON: %w", path, err)
	}
	events := map[string][]json.RawMessage{}
	if raw, ok := doc["hooks"]; ok {
		if err := json.Unmarshal(raw, &events); err != nil {
			return nil, nil, fmt.Errorf("%s: hooks is not an object of event lists: %w", path, err)
		}
	}
	return doc, events, nil
}

// groupCommands lists the commands of a matcher group.
func groupCommands(raw json.RawMessage) []string {
	var g struct {
		Hooks []struct {
			Command string `json:"command"`
		} `json:"hooks"`
	}
	_ = json.Unmarshal(raw, &g)
	var out []string
	for _, h := range g.Hooks {
		out = append(out, h.Command)
	}
	return out
}

// installedCommand is squadrn's guard command in hooks.json at path; "" when there is none.
func installedCommand(path string) (string, error) {
	_, events, err := readHooks(path)
	if err != nil {
		return "", err
	}
	for _, g := range events["PreToolUse"] {
		for _, c := range groupCommands(g) {
			if ours(c) {
				return c, nil
			}
		}
	}
	return "", nil
}

// setGuardHook makes cmd the only squadrn guard hook in hooks.json at path ("" removes it),
// keeping every other hook. It reports whether the file changed.
func setGuardHook(path, cmd string) (bool, error) {
	doc, events, err := readHooks(path)
	if err != nil {
		return false, err
	}
	var groups []json.RawMessage
	found := false
	for _, g := range events["PreToolUse"] {
		cmds := groupCommands(g)
		if len(cmds) == 1 && ours(cmds[0]) {
			if cmd != "" && cmds[0] == cmd && !found {
				found = true
				groups = append(groups, g)
			}
			continue
		}
		groups = append(groups, g)
	}
	if cmd != "" && !found {
		g, err := json.Marshal(map[string]any{
			"matcher": "Bash",
			"hooks":   []map[string]any{{"type": "command", "command": cmd, "timeout": hookTimeout}},
		})
		if err != nil {
			return false, err
		}
		groups = append(groups, g)
	}
	if len(groups) == 0 {
		delete(events, "PreToolUse")
	} else {
		events["PreToolUse"] = groups
	}
	raw, err := json.Marshal(events)
	if err != nil {
		return false, err
	}
	doc["hooks"] = raw
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(doc); err != nil {
		return false, err
	}
	old, _ := os.ReadFile(path)
	if canonical(old) == canonical(buf.Bytes()) {
		return false, nil
	}
	if err := fsx.WriteBytes(path, buf.Bytes(), 0o600); err != nil {
		return false, err
	}
	return true, nil
}

// canonical is b re-encoded without formatting; "" when it does not parse.
func canonical(b []byte) string {
	var v any
	if json.Unmarshal(b, &v) != nil {
		return ""
	}
	out, _ := json.Marshal(v)
	return string(out)
}

// listedHook is a hook as the app server's hooks/list reports it.
type listedHook struct {
	Key         string `json:"key"`
	Command     string `json:"command"`
	SourcePath  string `json:"sourcePath"`
	Enabled     bool   `json:"enabled"`
	CurrentHash string `json:"currentHash"`
	TrustStatus string `json:"trustStatus"`
}

// guardHook finds squadrn's guard hook from hooks.json at path in a hooks/list result.
func guardHook(raw json.RawMessage, path string) (listedHook, bool) {
	var r struct {
		Data []struct {
			Hooks []listedHook `json:"hooks"`
		} `json:"data"`
	}
	if json.Unmarshal(raw, &r) != nil {
		return listedHook{}, false
	}
	for _, e := range r.Data {
		for _, h := range e.Hooks {
			if ours(h.Command) && (fsx.SameFile(h.SourcePath, path) || fsx.SamePath(h.SourcePath, path)) {
				return h, true
			}
		}
	}
	return listedHook{}, false
}

func trusted(h listedHook) bool {
	return h.Enabled && (h.TrustStatus == "trusted" || h.TrustStatus == "managed")
}

// listGuard asks the app server for the state of squadrn's guard hook in configDir.
func (c CLI) listGuard(ctx context.Context, configDir string) (listedHook, bool, error) {
	dir := c.dir(configDir)
	r, err := c.appServer(ctx, configDir, request{Method: "hooks/list", Params: map[string]any{"cwds": []string{dir}}})
	if err != nil {
		return listedHook{}, false, err
	}
	if r[0].Err != nil {
		return listedHook{}, false, fmt.Errorf("codex hooks/list: %w", r[0].Err)
	}
	h, ok := guardHook(r[0].Result, filepath.Join(dir, hooksFile))
	return h, ok, nil
}

// trustGuard records trust in squadrn's guard hook, as Codex's /hooks review would. It reports
// whether it changed anything.
func (c CLI) trustGuard(ctx context.Context, configDir string) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, trustTimeout)
	defer cancel()
	h, ok, err := c.listGuard(ctx, configDir)
	if err != nil {
		return false, err
	}
	if !ok {
		return false, errors.New("codex does not list squadrn's guard hook from " + hooksFile)
	}
	if trusted(h) {
		return false, nil
	}
	if h.Key == "" || h.CurrentHash == "" {
		return false, errors.New("codex lists squadrn's guard hook without a key or hash")
	}
	r, err := c.appServer(ctx, configDir, request{Method: "config/batchWrite", Params: map[string]any{
		"edits": []map[string]any{{
			"keyPath":       "hooks.state",
			"mergeStrategy": "upsert",
			"value":         map[string]any{h.Key: map[string]any{"trusted_hash": h.CurrentHash, "enabled": true}},
		}},
	}})
	if err != nil {
		return false, err
	}
	if r[0].Err != nil {
		return false, fmt.Errorf("codex config/batchWrite: %w", r[0].Err)
	}
	return true, nil
}

// InstallPlugin installs the guard hook from marketplace into configDir's hooks.json and trusts
// it. It reports whether it changed anything.
func (c CLI) InstallPlugin(ctx context.Context, configDir, marketplace string) (bool, error) {
	if err := c.prepare(configDir); err != nil {
		return false, err
	}
	changed, err := setGuardHook(filepath.Join(c.dir(configDir), hooksFile), hookCommand(marketplace))
	if err != nil {
		return false, err
	}
	t, err := c.trustGuard(ctx, configDir)
	return changed || t, err
}

// UpdatePlugin re-trusts the installed guard hook (its files changed with an update).
func (c CLI) UpdatePlugin(ctx context.Context, configDir string) error {
	cmd, err := installedCommand(filepath.Join(c.dir(configDir), hooksFile))
	if err != nil || cmd == "" {
		return err
	}
	_, err = c.trustGuard(ctx, configDir)
	return err
}

// RemovePlugin removes the guard hook; other hooks are kept.
func (c CLI) RemovePlugin(_ context.Context, configDir string) error {
	path := filepath.Join(c.dir(configDir), hooksFile)
	if !fsx.IsFile(path) {
		return nil
	}
	_, err := setGuardHook(path, "")
	return err
}

// PluginState: installed is the guard hook in hooks.json, enabled that Codex trusts and runs it.
func (c CLI) PluginState(ctx context.Context, configDir string) (installed, enabled bool, err error) {
	cmd, err := installedCommand(filepath.Join(c.dir(configDir), hooksFile))
	if err != nil || cmd == "" {
		return false, false, err
	}
	h, ok, err := c.listGuard(ctx, configDir)
	if err != nil {
		return true, false, err
	}
	return true, ok && trusted(h), nil
}

// EnablePluginArgs is empty: trust is recorded by re-running account add, not a codex command.
func (CLI) EnablePluginArgs() []string { return nil }
