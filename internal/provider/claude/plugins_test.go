package claude

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// pluginCLI records claude plugin calls and keeps the marketplaces and plugins they change.
type pluginCLI struct {
	calls   []string
	markets string
	plugins string
	fail    string
}

func (f *pluginCLI) Output(_ context.Context, _ string, args ...string) ([]byte, error) {
	call := strings.Join(args, " ")
	f.calls = append(f.calls, call)
	if f.fail != "" && strings.HasPrefix(call, f.fail) {
		return nil, errors.New("boom")
	}
	switch {
	case call == "plugin marketplace list --json":
		return []byte(f.markets), nil
	case call == "plugin list --json":
		return []byte(f.plugins), nil
	case strings.HasPrefix(call, "plugin marketplace add"):
		f.markets = `[{"name":"squadrn"}]`
	case strings.HasPrefix(call, "plugin install"):
		f.plugins = `[{"id":"squadrn@squadrn","enabled":true}]`
	case strings.HasPrefix(call, "plugin marketplace remove"):
		f.markets = `[]`
	}
	return nil, nil
}

// registry writes a config dir's plugin registry files; "" leaves a file out.
func registry(t *testing.T, known, installed string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "plugins"), 0o700); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"known_marketplaces.json": known, "installed_plugins.json": installed} {
		if body == "" {
			continue
		}
		if err := os.WriteFile(filepath.Join(dir, "plugins", name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestInstallPlugin(t *testing.T) {
	ctx := context.Background()
	const (
		listM   = "plugin marketplace list --json"
		add     = "plugin marketplace add /m"
		listP   = "plugin list --json"
		install = "plugin install squadrn@squadrn --scope user"
	)
	for _, tc := range []struct {
		name, known, installed string
		cli                    *pluginCLI
		ok                     bool
		calls                  string
	}{
		// A new account has no registry: nothing to list.
		{"fresh", "", "", &pluginCLI{}, true, add + ";" + install},
		{"marketplace known", `{"squadrn":{"source":{"source":"directory"}}}`, `{"plugins":{}}`, &pluginCLI{}, true, install},
		{"other marketplace", `{"other":{}}`, `{"version":2,"plugins":{"other@x":[{}]}}`, &pluginCLI{}, true, add + ";" + install},
		{"installed", `{"squadrn":{}}`, `{"plugins":{"squadrn@squadrn":[{"scope":"user"}]}}`, &pluginCLI{}, false, ""},
		// An unreadable registry falls back to the CLI listings.
		{"bad marketplaces", `not json`, "", &pluginCLI{markets: `[{"name":"other"}]`, plugins: `[]`}, true, listM + ";" + add + ";" + listP + ";" + install},
		{"bad plugins", `{}`, `[]`, &pluginCLI{markets: `[{"name":"squadrn"}]`, plugins: `[{"id":"squadrn@squadrn"}]`}, false, listM + ";" + listP},
		// The add failing because the CLI knows the marketplace after all is fine.
		{"stale registry", "", "", &pluginCLI{fail: "plugin marketplace add", markets: `[{"name":"squadrn"}]`}, true, add + ";" + listM + ";" + install},
	} {
		dir := registry(t, tc.known, tc.installed)
		ok, err := installPlugin(ctx, tc.cli, dir, "/m")
		if err != nil || ok != tc.ok || strings.Join(tc.cli.calls, ";") != tc.calls {
			t.Errorf("%s: install = %v, %v; calls %q, want %q", tc.name, ok, err, strings.Join(tc.cli.calls, ";"), tc.calls)
		}
	}
	if _, err := installPlugin(ctx, &pluginCLI{fail: "plugin marketplace add", markets: `[]`}, t.TempDir(), "/m"); err == nil {
		t.Error("a failed marketplace add was dropped")
	}
	if _, err := installPlugin(ctx, &pluginCLI{markets: `not json`}, registry(t, "not json", ""), "/m"); err == nil {
		t.Error("unparsable listing accepted")
	}
	if _, err := installPlugin(ctx, &pluginCLI{fail: "plugin install"}, t.TempDir(), "/m"); err == nil {
		t.Error("a failed install was dropped")
	}
	f := &pluginCLI{plugins: `[{"id":"squadrn@squadrn","enabled":true}]`}
	if installed, enabled, err := pluginState(ctx, f, ""); !installed || !enabled || err != nil {
		t.Errorf("state = %v %v %v", installed, enabled, err)
	}
}

func TestUpdateAndRemovePlugin(t *testing.T) {
	ctx := context.Background()
	f := &pluginCLI{}
	if err := updatePlugin(ctx, f, ""); err != nil || strings.Join(f.calls, ";") != "plugin marketplace update squadrn;plugin update squadrn@squadrn" {
		t.Errorf("update = %v (%v)", err, f.calls)
	}
	f = &pluginCLI{fail: "plugin uninstall", markets: `[{"name":"squadrn"}]`}
	if err := removePlugin(ctx, f, ""); err == nil || strings.Join(f.calls, ";") != "plugin uninstall squadrn@squadrn;plugin marketplace list --json;plugin marketplace remove squadrn" {
		t.Errorf("remove = %v (%v)", err, f.calls)
	}
	f = &pluginCLI{markets: `[]`}
	if err := removePlugin(ctx, f, ""); err != nil || strings.Contains(strings.Join(f.calls, ";"), "marketplace remove") {
		t.Errorf("no registration: %v (%v)", err, f.calls)
	}
	f = &pluginCLI{fail: "plugin marketplace remove", markets: `[{"name":"squadrn"}]`}
	if err := removePlugin(ctx, f, ""); err == nil || !strings.Contains(err.Error(), "remove the squadrn marketplace") {
		t.Errorf("a failed removal must be reported: %v", err)
	}
}

func TestPluginState(t *testing.T) {
	ctx := context.Background()
	for plugins, want := range map[string][2]bool{
		`[]`: {false, false},
		`[{"id":"squadrn@squadrn","enabled":false}]`: {true, false},
		`[{"id":"other@x","enabled":true}]`:          {false, false},
	} {
		installed, enabled, err := pluginState(ctx, &pluginCLI{plugins: plugins}, "")
		if err != nil || [2]bool{installed, enabled} != want {
			t.Errorf("%s: %v %v %v", plugins, installed, enabled, err)
		}
	}
	if _, _, err := pluginState(ctx, &pluginCLI{fail: "plugin list"}, ""); err == nil {
		t.Error("listing error dropped")
	}
}

func TestMCP(t *testing.T) {
	ctx := context.Background()
	f := &pluginCLI{fail: "mcp get absent"}
	if !hasMCP(ctx, f, "", "here") || hasMCP(ctx, f, "", "absent") {
		t.Errorf("hasMCP wrong (%v)", f.calls)
	}
	f.calls = nil
	if err := addMCP(ctx, f, "", "cg", []byte(`{"command":"cg"}`)); err != nil || strings.Join(f.calls, ";") != `mcp add-json -s user cg {"command":"cg"}` {
		t.Errorf("addMCP = %v (%v)", err, f.calls)
	}
}

func TestFastPathsReadTheConfigDir(t *testing.T) {
	dir := t.TempDir()
	if pluginRegistered(dir) || mcpConfigured(dir, "x") {
		t.Fatal("found state in an empty dir")
	}
	if err := os.MkdirAll(filepath.Join(dir, "plugins"), 0o700); err != nil {
		t.Fatal(err)
	}
	for body, want := range map[string]bool{
		`{"plugins":{"squadrn@squadrn":[{"scope":"user"}]}}`: true,
		`{"plugins":{"squadrn@squadrn":[]}}`:                 false,
		`{"plugins":{"other@x":[{}]}}`:                       false,
		`not json`:                                           false,
	} {
		if err := os.WriteFile(filepath.Join(dir, "plugins", "installed_plugins.json"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if got := pluginRegistered(dir); got != want {
			t.Errorf("pluginRegistered(%s) = %v", body, got)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, ".claude.json"), []byte(`{"mcpServers":{"x":{}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if !mcpConfigured(dir, "x") || mcpConfigured(dir, "y") {
		t.Error("mcpConfigured")
	}
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)
	if d, ok := configDirOrMain(""); !ok || d != filepath.Join(dir, ".claude") {
		t.Errorf("configDirOrMain = %q", d)
	}
}
