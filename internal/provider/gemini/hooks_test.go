package gemini

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/squadrn/squadrn/internal/provider"
)

func TestParseTool(t *testing.T) {
	g := Guard{}
	call, err := g.ParseTool([]byte(`{"session_id":"s","transcript_path":"/t","cwd":"/repo","hook_event_name":"BeforeTool","timestamp":"x","tool_name":"run_shell_command","tool_input":{"command":"git push","description":"d","dir_path":"sub"}}`))
	if err != nil || call.Command != "git push" || call.Dir != filepath.Join("/repo", "sub") || call.Shell != shell || call.Tool != shellTool {
		t.Fatalf("relative dir: %+v, %v", call, err)
	}
	abs := filepath.Join(t.TempDir(), "x")
	call, _ = g.ParseTool([]byte(`{"cwd":"/repo","tool_name":"run_shell_command","tool_input":{"command":"ls","dir_path":` + quote(abs) + `}}`))
	if call.Dir != abs {
		t.Errorf("absolute dir: %+v", call)
	}
	call, _ = g.ParseTool([]byte(`{"cwd":"/repo","tool_name":"run_shell_command","tool_input":{"command":"ls"}}`))
	if call.Dir != "/repo" {
		t.Errorf("no dir: %+v", call)
	}
	call, _ = g.ParseTool([]byte(`{"tool_name":"run_shell_command","tool_input":{"command":"ls","dir_path":"d"}}`))
	if call.Dir != "d" {
		t.Errorf("no cwd: %+v", call)
	}
	call, _ = g.ParseTool([]byte(`{"cwd":"/repo","tool_name":"read_file","tool_input":{"file_path":"x"}}`))
	if call.Shell != "" || call.Tool != "read_file" {
		t.Errorf("other tool: %+v", call)
	}
	if _, err := g.ParseTool([]byte(`{`)); err == nil {
		t.Error("bad input accepted")
	}
	if out, errOut, code := g.Block("no"); out != "" || errOut != "no\n" || code != 2 {
		t.Errorf("Block = %q %q %d", out, errOut, code)
	}
}

func quote(s string) string { return `"` + strings.ReplaceAll(s, `\`, `\\`) + `"` }

// marketplace is a fake install: the hook entry point exists.
func marketplace(t *testing.T) string {
	t.Helper()
	mp := t.TempDir()
	write(t, filepath.Join(mp, "plugin", "bin", "squadrn-hook"), "#!/bin/sh\n")
	write(t, filepath.Join(mp, "bin", "squadrn.exe"), "")
	return mp
}

func TestGuardInstaller(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	c := CLI{Home: home}
	acct := filepath.Join(home, ".gemini-work")
	path := filepath.Join(acct, ".gemini", "settings.json")
	mp := marketplace(t)

	if inst, en, err := c.PluginState(ctx, acct); inst || en || err != nil {
		t.Fatalf("no settings: %v %v %v", inst, en, err)
	}
	write(t, path, `{"ui":{"theme":"x"},"hooks":{"BeforeTool":[{"matcher":"write_file","hooks":[{"type":"command","command":"lint"}]},{"matcher":"run_shell_command","hooks":[{"type":"command","command":"\"/old/plugin/bin/squadrn-hook\" guard-git --no-ask"}]}]}}`)
	if inst, en, _ := c.PluginState(ctx, acct); !inst || en {
		t.Fatalf("an old hook: %v %v", inst, en)
	}
	changed, err := c.InstallPlugin(ctx, acct, mp)
	if err != nil || !changed {
		t.Fatalf("install: %v %v", changed, err)
	}
	b, _ := os.ReadFile(path)
	if strings.Count(string(b), hookDefs[0].args) != 1 || strings.Contains(string(b), "/old/") || !strings.Contains(string(b), `"lint"`) || !strings.Contains(string(b), `"theme": "x"`) {
		t.Fatalf("settings = %s", b)
	}
	if inst, en, err := c.PluginState(ctx, acct); !inst || !en || err != nil {
		t.Fatalf("installed: %v %v %v", inst, en, err)
	}
	if changed, err := c.InstallPlugin(ctx, acct, mp); err != nil || changed {
		t.Fatalf("again: %v %v", changed, err)
	}
	if err := c.UpdatePlugin(ctx, acct); err != nil || c.EnablePluginArgs() != nil {
		t.Fatal("update")
	}
	// Turned off by name, or all hooks off.
	for _, off := range []string{`{"disabled":["squadrn-guard"]}`, `{"enabled":false}`} {
		doc, _, _ := readDoc(path)
		cfg := map[string]any{}
		if off == `{"enabled":false}` {
			cfg["enabled"] = false
		} else {
			cfg["disabled"] = []any{guardName}
		}
		doc["hooksConfig"] = cfg
		b, _ := Settings{}.Encode(doc)
		write(t, path, string(b))
		if inst, en, _ := c.PluginState(ctx, acct); !inst || en {
			t.Errorf("%s: %v %v", off, inst, en)
		}
	}
	// The entry point is gone.
	if err := os.RemoveAll(mp); err != nil {
		t.Fatal(err)
	}
	doc, _, _ := readDoc(path)
	delete(doc, "hooksConfig")
	b, _ = Settings{}.Encode(doc)
	write(t, path, string(b))
	if inst, en, _ := c.PluginState(ctx, acct); !inst || en {
		t.Errorf("missing entry point: %v %v", inst, en)
	}
	if err := c.RemovePlugin(ctx, acct); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(path)
	if strings.Contains(string(b), "squadrn") || !strings.Contains(string(b), `"lint"`) {
		t.Fatalf("after remove: %s", b)
	}
	if err := c.RemovePlugin(ctx, filepath.Join(home, ".gemini-none")); err != nil {
		t.Fatal(err)
	}
	write(t, path, `{`)
	if _, err := c.InstallPlugin(ctx, acct, marketplace(t)); err == nil {
		t.Fatal("a broken file was rewritten")
	}
	if _, _, err := c.PluginState(ctx, acct); err == nil {
		t.Fatal("a broken file was read")
	}
}

func TestGuardInstallerLeavesCommentedMainAlone(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	c := CLI{Home: home}
	mainPath := filepath.Join(home, ".gemini", "settings.json")
	orig := "// mine\n{\"ui\": {}}\n"
	write(t, mainPath, orig)
	if _, err := c.InstallPlugin(ctx, "", marketplace(t)); !errors.Is(err, errCommented) || !strings.Contains(err.Error(), `"BeforeTool"`) {
		t.Fatalf("install = %v", err)
	}
	if err := c.RemovePlugin(ctx, ""); err != nil {
		t.Fatalf("nothing to remove: %v", err)
	}
	if b, _ := os.ReadFile(mainPath); string(b) != orig {
		t.Fatalf("main rewritten: %q", b)
	}
	write(t, mainPath, "// mine\n{\"hooks\":{\"BeforeTool\":[{\"hooks\":[{\"command\":\"squadrn-hook guard-git\"}]}]}}\n")
	if err := c.RemovePlugin(ctx, ""); !errors.Is(err, errCommented) {
		t.Fatalf("remove = %v", err)
	}
	// Without comments main is written like an account's copy.
	write(t, mainPath, `{"ui":{}}`)
	if changed, err := c.InstallPlugin(ctx, "", marketplace(t)); err != nil || !changed {
		t.Fatalf("plain main: %v %v", changed, err)
	}
	// An account's own copy is rewritten even with comments (they are dropped, ADR 0013 §3).
	acct := filepath.Join(home, ".gemini-work")
	write(t, filepath.Join(acct, ".gemini", "settings.json"), "// x\n{}\n")
	if changed, err := c.InstallPlugin(ctx, acct, marketplace(t)); err != nil || !changed {
		t.Fatalf("account with comments: %v %v", changed, err)
	}
}

func TestHookCommandRuns(t *testing.T) {
	mp := marketplace(t)
	cmd := hookCommand(mp, hookDefs[0].args)
	if !guardEntry(cmd) || !entryExists(cmd) || !strings.HasSuffix(cmd, "guard-git --no-ask --agent gemini") {
		t.Fatalf("command = %s", cmd)
	}
	if entryExists("no quotes") || entryExists(`"open`) {
		t.Fatal("entryExists")
	}
	if !provider.Gemini.Caps.Plugins || !provider.Gemini.NoGitHooks {
		t.Fatal("caps")
	}
}
