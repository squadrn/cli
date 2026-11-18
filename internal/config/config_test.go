package config

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadMissingFileUsesDefaults(t *testing.T) {
	c, err := load(filepath.Join(t.TempDir(), "nope.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(c.ProtectedBranches, DefaultProtectedBranches()) || len(c.SharedMCP) != 0 {
		t.Fatalf("got %+v", c)
	}
}

func TestLoadFile(t *testing.T) {
	c, err := load(writeConfig(t, `
shared_mcp = ["codegraph", "figwright"]
protected_branches = ["main", "production"]
`))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(c.SharedMCP, []string{"codegraph", "figwright"}) {
		t.Errorf("SharedMCP = %v", c.SharedMCP)
	}
	if !slices.Equal(c.ProtectedBranches, []string{"main", "production"}) {
		t.Errorf("ProtectedBranches = %v", c.ProtectedBranches)
	}
}

func TestLoadRejectsUnknownKey(t *testing.T) {
	_, err := load(writeConfig(t, `shared_mpc = ["x"]`))
	if err == nil || !strings.Contains(err.Error(), `unknown key "shared_mpc"`) {
		t.Fatalf("err = %v", err)
	}
}

func TestLoadRejectsInvalidTOML(t *testing.T) {
	if _, err := load(writeConfig(t, `shared_mcp = [`)); err == nil {
		t.Fatal("expected a parse error")
	}
}

func TestEnvOverridesSharedMCP(t *testing.T) {
	t.Setenv("SPRAWL_SHARED_MCP", "a  b")
	c, err := load(writeConfig(t, `shared_mcp = ["x"]`))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(c.SharedMCP, []string{"a", "b"}) {
		t.Fatalf("SharedMCP = %v", c.SharedMCP)
	}
}

func TestPathHonoursXDG(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "cfg")
	t.Setenv("XDG_CONFIG_HOME", cfg)
	p, err := Path()
	if err != nil || p != filepath.Join(cfg, "sprawl", "config.toml") {
		t.Fatalf("Path() = %q, %v", p, err)
	}
}

func TestEmptyEnvDoesNotOverride(t *testing.T) {
	t.Setenv("SPRAWL_SHARED_MCP", " ")
	c, err := load(writeConfig(t, `shared_mcp = ["x"]`))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(c.SharedMCP, []string{"x"}) {
		t.Fatalf("SharedMCP = %v", c.SharedMCP)
	}
}

func TestLoadNamesAndProvider(t *testing.T) {
	c, err := load(writeConfig(t, `
command = "ai"
aliases = ["sx", "cx"]
`))
	if err != nil {
		t.Fatal(err)
	}
	if c.CommandName() != "ai" || !slices.Equal(c.Aliases, []string{"sx", "cx"}) || c.DefaultProvider != "claude" {
		t.Fatalf("got %+v", c)
	}
	if (Config{}).CommandName() != DefaultCommand {
		t.Error("empty command must fall back to the default")
	}
}

func TestLoadRejectsBadNames(t *testing.T) {
	for _, body := range []string{`command = "cd"`, `command = "Sprawl"`, `aliases = ["ok", "claude"]`, `default_provider = "nope"`} {
		if _, err := load(writeConfig(t, body)); err == nil {
			t.Errorf("accepted %s", body)
		}
	}
}

func TestValidateCommand(t *testing.T) {
	for _, ok := range []string{"sprawl", "ai", "x", "cc-2", "abcdefghijklmnop"} {
		if err := ValidateCommand(ok); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "1ai", "A", "a_b", "abcdefghijklmnopq", "test", "time", "git", "codex"} {
		if err := ValidateCommand(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestSaveRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sprawl", "config.toml")
	in := Config{Command: "ai", Aliases: []string{"sx"}, SharedMCP: []string{"codegraph"}}
	if err := Save(path, in); err != nil {
		t.Fatal(err)
	}
	out, err := Read(path)
	if err != nil || out.Command != "ai" || !slices.Equal(out.Aliases, in.Aliases) || !slices.Equal(out.SharedMCP, in.SharedMCP) {
		t.Fatalf("Read = %+v, %v", out, err)
	}
	if b, _ := os.ReadFile(path); strings.Contains(string(b), "protected_branches") {
		t.Errorf("defaults must not be written out:\n%s", b)
	}
}

func TestLegacyPaths(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "cfg")
	t.Setenv("XDG_CONFIG_HOME", cfg)
	p, err := LegacyPaths()
	if err != nil || !slices.Equal(p, []string{filepath.Join(cfg, "squad", "config.toml")}) {
		t.Fatalf("LegacyPaths() = %q, %v", p, err)
	}
}

func TestGetSet(t *testing.T) {
	var c Config
	for _, k := range Keys {
		if _, err := c.Get(k); err != nil {
			t.Errorf("Get(%s): %v", k, err)
		}
	}
	if v, _ := c.Get("git_hooks"); v != "true" {
		t.Errorf("git_hooks default = %s", v)
	}
	for k, v := range map[string]string{"command": "ai", "aliases": "a1, a2", "default_provider": "claude", "shared_mcp": "x,y", "protected_branches": "main release/*", "git_hooks": "false", "update_check": "false", "update_channel": "beta"} {
		if err := c.Set(k, v); err != nil {
			t.Fatalf("Set(%s, %s): %v", k, v, err)
		}
	}
	if !slices.Equal(c.Aliases, []string{"a1", "a2"}) || !slices.Equal(c.ProtectedBranches, []string{"main", "release/*"}) || c.GitHooksEnabled() {
		t.Fatalf("after Set: %+v", c)
	}
	if c.UpdateCheckEnabled() || c.Channel() != "beta" {
		t.Fatalf("update settings: %+v", c)
	}
	if v, _ := c.Get("aliases"); v != "a1,a2" {
		t.Errorf("Get(aliases) = %q", v)
	}
	for k, v := range map[string]string{"colour": "x", "git_hooks": "maybe", "command": "cd", "update_channel": "nightly", "update_check": "?"} {
		if err := c.Set(k, v); err == nil {
			t.Errorf("Set(%s, %s) accepted", k, v)
		}
	}
	if _, err := c.Get("colour"); err == nil {
		t.Error("Get(colour) accepted")
	}
}

func TestSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.toml")
	if err := Save(path, Config{Command: "ai"}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	if !strings.Contains(string(b), "schema = 1") {
		t.Fatalf("schema not stamped:\n%s", b)
	}
	if _, err := Read(writeConfig(t, "schema = 99\n")); err == nil || !strings.Contains(err.Error(), "newer") {
		t.Fatalf("newer schema: %v", err)
	}
}

func TestDirsFor(t *testing.T) {
	for _, k := range []string{"XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME", "XDG_CACHE_HOME"} {
		t.Setenv(k, "")
	}
	home := filepath.Join(t.TempDir(), "h")
	j := filepath.Join
	d := DirsFor(home, "sprawl")
	if d != (Dirs{Config: j(home, ".config", "sprawl"), Data: j(home, ".local", "share", "sprawl"), State: j(home, ".local", "state", "sprawl"), Cache: j(home, ".cache", "sprawl")}) {
		t.Fatalf("defaults: %+v", d)
	}
	cache := filepath.Join(t.TempDir(), "c")
	t.Setenv("XDG_CACHE_HOME", cache)
	t.Setenv("XDG_DATA_HOME", "relative")
	d = DirsFor(home, "sprawl")
	if d.Cache != j(cache, "sprawl") || d.Data != j(home, ".local", "share", "sprawl") {
		t.Fatalf("XDG: %+v", d)
	}
}
