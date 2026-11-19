package claude

import (
	"context"
	"errors"
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

func TestInstallPlugin(t *testing.T) {
	ctx := context.Background()
	f := &pluginCLI{markets: `[{"name":"other"}]`, plugins: `[]`}
	if ok, err := installPlugin(ctx, f, "", "/m"); !ok || err != nil {
		t.Fatalf("install = %v, %v (%v)", ok, err, f.calls)
	}
	want := "plugin marketplace list --json;plugin marketplace add /m;plugin list --json;plugin install squadrn@squadrn --scope user"
	if got := strings.Join(f.calls, ";"); got != want {
		t.Errorf("calls = %s", got)
	}
	f.calls = nil
	if ok, err := installPlugin(ctx, f, "", "/m"); ok || err != nil || strings.Join(f.calls, ";") != "plugin marketplace list --json;plugin list --json" {
		t.Errorf("second install = %v, %v (%v)", ok, err, f.calls)
	}
	if installed, enabled, err := pluginState(ctx, f, ""); !installed || !enabled || err != nil {
		t.Errorf("state = %v %v %v", installed, enabled, err)
	}
	if _, err := installPlugin(ctx, &pluginCLI{markets: `not json`}, "", "/m"); err == nil {
		t.Error("unparsable listing accepted")
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
