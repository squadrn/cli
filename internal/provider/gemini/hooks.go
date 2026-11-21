package gemini

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/squadrn/squadrn/internal/fsx"
	"github.com/squadrn/squadrn/internal/provider"
)

// Layer 1 of the push guard is a BeforeTool hook in the account's own settings.json (user
// settings: no trust needed, unlike an extension or project hooks), ADR 0013 §5. Gemini runs
// hooks synchronously and blocks a tool call on exit 2.

const guardName = "squadrn-guard"

// hookDef is one hook squadrn keeps in an account's settings.json.
type hookDef struct {
	event, matcher, name, args string
	timeout                    int // milliseconds
}

// notifyName names squadrn's notification hooks. Gemini runs hooks synchronously, so they have a
// short timeout, always exit 0 with nothing on stdout (a decision or exit 2 on AfterAgent would
// make Gemini retry the turn) and leave desktop delivery to a background `hook deliver`. There is
// no AfterTool hook: it would delay every tool call.
const (
	notifyName = "squadrn-notify"
	notifyArgs = "notify --agent gemini"
)

// hookDefs are squadrn's hooks, the guard first.
var hookDefs = []hookDef{
	{"BeforeTool", "run_shell_command", guardName, "guard-git --no-ask --agent gemini", 5000},
	{"BeforeAgent", "", notifyName, notifyArgs, 2000},
	{"AfterAgent", "", notifyName, notifyArgs, 2000},
	{"Notification", "", notifyName, notifyArgs, 2000},
}

// guardEntry is a guard hook entry: squadrn's entry point running guard-git.
func guardEntry(cmd string) bool {
	return provider.SquadrnCommand(cmd) && strings.Contains(cmd, "guard-git")
}

// errCommented: main's settings.json has comments squadrn would drop by rewriting it.
var errCommented = errors.New("has comments squadrn does not rewrite")

func (c CLI) settingsPath(configDir string) string {
	return filepath.Join(c.state(configDir), provider.Gemini.Settings)
}

// readDoc reads a settings file as a document; raw is its content (nil when missing).
func readDoc(path string) (map[string]any, []byte, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return map[string]any{}, nil, nil
	} else if err != nil {
		return nil, nil, err
	}
	doc, err := Settings{}.Decode(b)
	if err != nil {
		return nil, nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if doc == nil {
		doc = map[string]any{}
	}
	return doc, b, nil
}

// wantedHooks are squadrn's hook groups for marketplace, per event.
func wantedHooks(marketplace string) map[string][]any {
	out := map[string][]any{}
	for _, d := range hookDefs {
		group := map[string]any{"hooks": []any{map[string]any{
			"type": "command", "name": d.name, "command": hookCommand(marketplace, d.args), "timeout": d.timeout,
		}}}
		if d.matcher != "" {
			group["matcher"] = d.matcher
		}
		out[d.event] = append(out[d.event], group)
	}
	return out
}

// setHooks makes squadrn's hooks in the settings file at path those of marketplace ("" removes
// them), keeping everything else. A file with comments is written only when it is an account's
// own copy (main is false); main's is left alone and the error says what to add. It reports
// whether the file changed.
func setHooks(path, marketplace string, main bool) (bool, error) {
	doc, raw, err := readDoc(path)
	if err != nil {
		return false, err
	}
	before := canonical(doc)
	provider.SplitSquadrnHooks(doc)
	if marketplace != "" {
		provider.AddHooks(doc, wantedHooks(marketplace))
	}
	if canonical(doc) == before && raw != nil {
		return false, nil
	}
	if main && (Settings{}).HasComments(raw) {
		want, _ := json.Marshal(wantedHooks(marketplace))
		if marketplace == "" {
			return false, fmt.Errorf("%s %w: remove squadrn's hooks (commands running squadrn-hook) from it yourself", path, errCommented)
		}
		return false, fmt.Errorf("%s %w: add these entries to its \"hooks\" yourself: %s", path, errCommented, want)
	}
	b, err := Settings{}.Encode(doc)
	if err != nil {
		return false, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return false, err
	}
	return true, fsx.WriteBytes(path, b, 0o600)
}

func canonical(doc map[string]any) string {
	b, _ := json.Marshal(doc)
	return string(b)
}

// InstallPlugin writes squadrn's hooks from marketplace into the account's settings.json. It
// reports whether it changed anything.
func (c CLI) InstallPlugin(_ context.Context, configDir, marketplace string) (bool, error) {
	return setHooks(c.settingsPath(configDir), marketplace, configDir == "")
}

// UpdatePlugin has nothing to do: Gemini runs user hooks without a trust step, and
// InstallPlugin already rewrote changed commands.
func (CLI) UpdatePlugin(context.Context, string) error { return nil }

// RemovePlugin removes squadrn's hooks; other hooks are kept.
func (c CLI) RemovePlugin(_ context.Context, configDir string) error {
	path := c.settingsPath(configDir)
	if !fsx.IsFile(path) {
		return nil
	}
	_, err := setHooks(path, "", configDir == "")
	return err
}

// PluginState: installed is a guard hook in settings.json; enabled that Gemini runs it as
// squadrn wrote it (shell tool matcher, an entry point that exists, hooks not turned off).
func (c CLI) PluginState(_ context.Context, configDir string) (installed, enabled bool, err error) {
	doc, _, err := readDoc(c.settingsPath(configDir))
	if err != nil {
		return false, false, err
	}
	hooks, _ := doc["hooks"].(map[string]any)
	groups, _ := hooks["BeforeTool"].([]any)
	for _, g := range groups {
		group, _ := g.(map[string]any)
		list, _ := group["hooks"].([]any)
		for _, h := range list {
			m, _ := h.(map[string]any)
			cmd, _ := m["command"].(string)
			if !guardEntry(cmd) {
				continue
			}
			installed = true
			if group["matcher"] == "run_shell_command" && strings.HasSuffix(cmd, hookDefs[0].args) && entryExists(cmd) && !hooksOff(doc, m) {
				return true, true, nil
			}
		}
	}
	return installed, false, nil
}

// hooksOff reports whether settings turn hooks off, or the hook h by name or command.
func hooksOff(doc map[string]any, h map[string]any) bool {
	cfg, _ := doc["hooksConfig"].(map[string]any)
	if on, ok := cfg["enabled"].(bool); ok && !on {
		return true
	}
	disabled, _ := cfg["disabled"].([]any)
	return slices.Contains(disabled, h["name"]) || slices.Contains(disabled, h["command"])
}

// entryExists reports whether the quoted program a hook command starts is there.
func entryExists(cmd string) bool {
	_, rest, ok := strings.Cut(cmd, `"`)
	if !ok {
		return false
	}
	p, _, ok := strings.Cut(rest, `"`)
	return ok && fsx.IsFile(p)
}

// EnablePluginArgs is empty: re-running account add rewrites the hook.
func (CLI) EnablePluginArgs() []string { return nil }
