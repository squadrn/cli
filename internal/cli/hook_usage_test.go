package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	"sprawling-agents/internal/account"
)

// A legacy account (plugin installed, no marker) is recognised by hooks as by commands.
func TestHookSeesLegacyAccount(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, ".local", "state"))
	dir := filepath.Join(home, ".claude-old")
	if err := os.MkdirAll(filepath.Join(dir, "plugins"), 0o700); err != nil {
		t.Fatal(err)
	}
	reg := `{"plugins":{"` + account.PluginID + `":[]}}`
	if err := os.WriteFile(filepath.Join(dir, "plugins", "installed_plugins.json"), []byte(reg), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
	h := hookEnv{stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}, now: func() time.Time { return time.Unix(1000, 0) }}
	name, _ := h.recordStatus([]byte(`{"session_id":"s1","cwd":"/r","rate_limits":{"five_hour":{"used_percentage":5,"resets_at":4102444800}}}`))
	if name != "old" {
		t.Fatalf("recordStatus account = %q, want old", name)
	}
	if _, err := os.Stat(filepath.Join(dir, account.Marker)); err != nil {
		t.Fatalf("legacy account not marked: %v", err)
	}
	if _, err := os.Stat(filepath.Join(usageStore(home).Dir, "claude-old.json")); err != nil {
		t.Fatalf("usage not recorded: %v", err)
	}
}
