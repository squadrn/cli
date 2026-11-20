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
// plugin's hook entry point; PermissionRequest, UserPromptSubmit, PostToolUse and Stop hooks
// report the agent's state and approvals for notifications (ADR 0012). Codex runs a user hook
// only once its exact definition is trusted: squadrn records that trust the way Codex's /hooks
// review does, through the app server (hooks/list gives each hook's key and current hash,
// config/batchWrite stores hooks.state.<key>.trusted_hash in config.toml), so the hash is always
// Codex's own.

const (
	hooksFile = "hooks.json"
	// guardArgs: Codex hooks cannot ask (permissionDecision "ask" is not supported and lets the
	// call through), so the guard denies what it would ask about.
	guardArgs    = "guard-git --no-ask"
	notifyArgs   = "notify --agent codex"
	hookTimeout  = 10
	trustTimeout = 15 * time.Second
)

// hookDef is one hook squadrn keeps in hooks.json.
type hookDef struct{ event, matcher, args string }

// hookDefs are squadrn's hooks, the guard first.
var hookDefs = []hookDef{
	{"PreToolUse", "Bash", guardArgs},
	{"PermissionRequest", "", notifyArgs},
	{"UserPromptSubmit", "", notifyArgs},
	{"PostToolUse", "", notifyArgs},
	{"Stop", "", notifyArgs},
}

// squadrnCommand reports whether a hooks.json command runs squadrn's hook entry point, from any
// install location and in either platform's form.
func squadrnCommand(cmd string) bool {
	return strings.Contains(cmd, "squadrn-hook") || strings.Contains(cmd, "squadrn.exe")
}

// ours reports whether a hooks.json command is squadrn's guard.
func ours(cmd string) bool {
	return squadrnCommand(cmd) && strings.Contains(cmd, "guard-git")
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

// setHooks makes squadrn's hooks those of marketplace in hooks.json at path ("" removes them),
// keeping every other hook. It reports whether the file changed.
func setHooks(path, marketplace string) (bool, error) {
	doc, events, err := readHooks(path)
	if err != nil {
		return false, err
	}
	for _, d := range hookDefs {
		cmd := ""
		if marketplace != "" {
			cmd = hookCommand(marketplace, d.args)
		}
		var groups []json.RawMessage
		found := false
		for _, g := range events[d.event] {
			cmds := groupCommands(g)
			if len(cmds) == 1 && squadrnCommand(cmds[0]) {
				if cmd != "" && cmds[0] == cmd && !found {
					found = true
					groups = append(groups, g)
				}
				continue
			}
			groups = append(groups, g)
		}
		if cmd != "" && !found {
			group := map[string]any{"hooks": []map[string]any{{"type": "command", "command": cmd, "timeout": hookTimeout}}}
			if d.matcher != "" {
				group["matcher"] = d.matcher
			}
			g, err := json.Marshal(group)
			if err != nil {
				return false, err
			}
			groups = append(groups, g)
		}
		if len(groups) == 0 {
			delete(events, d.event)
		} else {
			events[d.event] = groups
		}
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

// squadrnHooks lists squadrn's hooks from hooks.json at path in a hooks/list result.
func squadrnHooks(raw json.RawMessage, path string) []listedHook {
	var r struct {
		Data []struct {
			Hooks []listedHook `json:"hooks"`
		} `json:"data"`
	}
	if json.Unmarshal(raw, &r) != nil {
		return nil
	}
	var out []listedHook
	for _, e := range r.Data {
		for _, h := range e.Hooks {
			if squadrnCommand(h.Command) && (fsx.SameFile(h.SourcePath, path) || fsx.SamePath(h.SourcePath, path)) {
				out = append(out, h)
			}
		}
	}
	return out
}

// guardHook finds squadrn's guard hook from hooks.json at path in a hooks/list result.
func guardHook(raw json.RawMessage, path string) (listedHook, bool) {
	for _, h := range squadrnHooks(raw, path) {
		if ours(h.Command) {
			return h, true
		}
	}
	return listedHook{}, false
}

func trusted(h listedHook) bool {
	return h.Enabled && (h.TrustStatus == "trusted" || h.TrustStatus == "managed")
}

// listHooks asks the app server for the state of squadrn's hooks in configDir.
func (c CLI) listHooks(ctx context.Context, configDir string) (json.RawMessage, string, error) {
	dir := c.dir(configDir)
	r, err := c.appServer(ctx, configDir, request{Method: "hooks/list", Params: map[string]any{"cwds": []string{dir}}})
	if err != nil {
		return nil, "", err
	}
	if r[0].Err != nil {
		return nil, "", fmt.Errorf("codex hooks/list: %w", r[0].Err)
	}
	return r[0].Result, filepath.Join(dir, hooksFile), nil
}

// listGuard asks the app server for the state of squadrn's guard hook in configDir.
func (c CLI) listGuard(ctx context.Context, configDir string) (listedHook, bool, error) {
	raw, path, err := c.listHooks(ctx, configDir)
	if err != nil {
		return listedHook{}, false, err
	}
	h, ok := guardHook(raw, path)
	return h, ok, nil
}

// trustHooks records trust in squadrn's hooks, as Codex's /hooks review would. The guard must
// be listed; it reports whether it changed anything.
func (c CLI) trustHooks(ctx context.Context, configDir string) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, trustTimeout)
	defer cancel()
	raw, path, err := c.listHooks(ctx, configDir)
	if err != nil {
		return false, err
	}
	if _, ok := guardHook(raw, path); !ok {
		return false, errors.New("codex does not list squadrn's guard hook from " + hooksFile)
	}
	trust := map[string]any{}
	for _, h := range squadrnHooks(raw, path) {
		if trusted(h) {
			continue
		}
		if h.Key == "" || h.CurrentHash == "" {
			return false, errors.New("codex lists a squadrn hook without a key or hash")
		}
		trust[h.Key] = map[string]any{"trusted_hash": h.CurrentHash, "enabled": true}
	}
	if len(trust) == 0 {
		return false, nil
	}
	r, err := c.appServer(ctx, configDir, request{Method: "config/batchWrite", Params: map[string]any{
		"edits": []map[string]any{{
			"keyPath":       "hooks.state",
			"mergeStrategy": "upsert",
			"value":         trust,
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

// InstallPlugin installs squadrn's hooks from marketplace into configDir's hooks.json and trusts
// them. It reports whether it changed anything.
func (c CLI) InstallPlugin(ctx context.Context, configDir, marketplace string) (bool, error) {
	if err := c.prepare(configDir); err != nil {
		return false, err
	}
	changed, err := setHooks(filepath.Join(c.dir(configDir), hooksFile), marketplace)
	if err != nil {
		return false, err
	}
	t, err := c.trustHooks(ctx, configDir)
	return changed || t, err
}

// UpdatePlugin re-trusts squadrn's installed hooks (their files changed with an update).
func (c CLI) UpdatePlugin(ctx context.Context, configDir string) error {
	cmd, err := installedCommand(filepath.Join(c.dir(configDir), hooksFile))
	if err != nil || cmd == "" {
		return err
	}
	_, err = c.trustHooks(ctx, configDir)
	return err
}

// RemovePlugin removes squadrn's hooks; other hooks are kept.
func (c CLI) RemovePlugin(_ context.Context, configDir string) error {
	path := filepath.Join(c.dir(configDir), hooksFile)
	if !fsx.IsFile(path) {
		return nil
	}
	_, err := setHooks(path, "")
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
