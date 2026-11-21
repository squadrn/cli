package config

import (
	"errors"
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
	t.Setenv("SQUADRN_SHARED_MCP", "a  b")
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
	if err != nil || p != filepath.Join(cfg, "squadrn", "config.toml") {
		t.Fatalf("Path() = %q, %v", p, err)
	}
}

func TestEmptyEnvDoesNotOverride(t *testing.T) {
	t.Setenv("SQUADRN_SHARED_MCP", " ")
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
	for _, body := range []string{`command = "cd"`, `command = "Squadrn"`, `aliases = ["ok", "claude"]`, `default_provider = "nope"`} {
		if _, err := load(writeConfig(t, body)); err == nil {
			t.Errorf("accepted %s", body)
		}
	}
}

func TestValidateCommand(t *testing.T) {
	for _, ok := range []string{"squadrn", "ai", "x", "cc-2", "abcdefghijklmnop"} {
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
	path := filepath.Join(t.TempDir(), "squadrn", "config.toml")
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

func TestSetCommandPromotesAlias(t *testing.T) {
	c := Config{Command: "squadrn", Aliases: []string{"x", "srn"}}
	if err := c.Set("command", "srn"); err != nil {
		t.Fatal(err)
	}
	if c.Command != "srn" || !slices.Equal(c.Aliases, []string{"x", "squadrn"}) {
		t.Fatalf("after promoting srn: %+v", c)
	}
	aliases := []string{"srn"}
	c = Config{Command: "squadrn", Aliases: aliases}
	c.SetCommand("ai")
	if c.Command != "ai" || !slices.Equal(c.Aliases, []string{"srn"}) {
		t.Fatalf("a new name keeps the aliases: %+v", c)
	}
	c.SetCommand("srn")
	if !slices.Equal(c.Aliases, []string{"ai"}) || aliases[0] != "srn" {
		t.Fatalf("the swap must not modify the caller's slice: %+v %v", c, aliases)
	}
	c = Config{Aliases: []string{"srn"}}
	c.SetCommand("srn")
	if c.Command != "srn" || !slices.Equal(c.Aliases, []string{"srn"}) {
		t.Fatalf("without a command there is nothing to swap in: %+v", c)
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
	d := DirsFor(home, "squadrn")
	if d != (Dirs{Config: j(home, ".config", "squadrn"), Data: j(home, ".local", "share", "squadrn"), State: j(home, ".local", "state", "squadrn"), Cache: j(home, ".cache", "squadrn")}) {
		t.Fatalf("defaults: %+v", d)
	}
	cache := filepath.Join(t.TempDir(), "c")
	t.Setenv("XDG_CACHE_HOME", cache)
	t.Setenv("XDG_DATA_HOME", "relative")
	d = DirsFor(home, "squadrn")
	if d.Cache != j(cache, "squadrn") || d.Data != j(home, ".local", "share", "squadrn") {
		t.Fatalf("XDG: %+v", d)
	}
}

func TestEnvOverrides(t *testing.T) {
	t.Setenv("SQUADRN_SHARED_MCP", " ")
	if len(EnvOverrides()) != 0 {
		t.Fatal("a blank variable overrides nothing")
	}
	t.Setenv("SQUADRN_SHARED_MCP", "a b")
	if EnvOverrides()["shared_mcp"] != "SQUADRN_SHARED_MCP" {
		t.Fatal("shared_mcp override not reported")
	}
}

func TestReadOrEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if _, fresh, err := ReadOrEmpty(path); err != nil || !fresh {
		t.Fatalf("nothing: %v %v", fresh, err)
	}
	if err := os.WriteFile(path, []byte("command = \"sx\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if cfg, fresh, err := ReadOrEmpty(path); err != nil || fresh || cfg.Command != "sx" {
		t.Fatalf("existing: %+v %v %v", cfg, fresh, err)
	}
	if err := os.WriteFile(path, []byte("bogus = 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ReadOrEmpty(path); err == nil {
		t.Fatal("an invalid config must be an error")
	}
}

func TestDefaultProfile(t *testing.T) {
	var c Config
	if v, _ := c.Get("default_profile"); v != "standard" {
		t.Errorf("default = %q", v)
	}
	if err := c.Set("default_profile", "strict"); err != nil || c.ProfileName() != "strict" {
		t.Errorf("set strict: %v", err)
	}
	if err := c.Set("default_profile", "lax"); !errors.Is(err, ErrInvalidValue) {
		t.Errorf("set lax: %v", err)
	}
}

func TestNotifyKeys(t *testing.T) {
	var c Config
	for k, want := range map[string]string{"notify.enabled": "true", "notify.events": "input,failed,done", "notify.sound": "input", "notify.when": "unfocused", "notify.quiet_hours": "", "notify.preview": "false", "notify.method": "auto"} {
		if v, err := c.Get(k); err != nil || v != want {
			t.Errorf("Get(%s) = %q, %v; want %q", k, v, err, want)
		}
	}
	for k, v := range map[string]string{"notify.enabled": "false", "notify.events": "input, info", "notify.sound": "all", "notify.when": "always", "notify.quiet_hours": "22:00-08:00", "notify.preview": "true", "notify.method": "desktop"} {
		if err := c.Set(k, v); err != nil {
			t.Fatalf("Set(%s, %s): %v", k, v, err)
		}
	}
	if c.Notify.On() || !slices.Equal(c.Notify.Events, []string{"input", "info"}) || !c.Notify.Preview || c.Notify.QuietHours != "22:00-08:00" {
		t.Fatalf("after Set: %+v", c.Notify)
	}
	if v, _ := c.Value("notify.events"); !slices.Equal(v.([]string), []string{"input", "info"}) {
		t.Errorf("Value(notify.events) = %v", v)
	}
	for k, v := range map[string]string{"notify.enabled": "maybe", "notify.events": "everything", "notify.sound": "loud", "notify.when": "never", "notify.quiet_hours": "late", "notify.method": "pigeon"} {
		if err := c.Set(k, v); !errors.Is(err, ErrInvalidValue) {
			t.Errorf("Set(%s, %s) = %v", k, v, err)
		}
	}
	path := writeConfig(t, "[notify]\nenabled = false\nquiet_hours = \"23:00-07:00\"\n")
	got, err := Read(path)
	if err != nil || got.Notify.On() || got.Notify.QuietHours != "23:00-07:00" {
		t.Fatalf("read = %+v, %v", got.Notify, err)
	}
	if _, err := Read(writeConfig(t, "[notify]\nsound = \"loud\"\n")); !errors.Is(err, ErrInvalidValue) {
		t.Errorf("invalid [notify] read: %v", err)
	}
}

// Every value offered for completion is one Set accepts.
func TestValuesAreAccepted(t *testing.T) {
	for _, key := range Keys {
		values := Values(key)
		switch key {
		case "command", "aliases", "shared_mcp", "protected_branches", "notify.quiet_hours":
			if values != nil {
				t.Errorf("%s: free text, got %v", key, values)
			}
			continue
		}
		if len(values) == 0 {
			t.Errorf("%s: no values", key)
		}
		for _, v := range values {
			var c Config
			if err := c.Set(key, v); err != nil {
				t.Errorf("%s %s: %v", key, v, err)
			}
		}
	}
	if !ListKey("notify.events") || ListKey("notify.sound") {
		t.Error("ListKey")
	}
}
