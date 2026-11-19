package install

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/squadrn/cli/internal/account"
	"github.com/squadrn/cli/internal/config"
	"github.com/squadrn/cli/internal/release"
)

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sprawl.toml")
	legacy := filepath.Join(dir, "squad.toml")

	if _, from, fresh, err := LoadConfig(path, []string{legacy}); err != nil || !fresh || from != "" {
		t.Fatalf("nothing: %q %v %v", from, fresh, err)
	}
	write(t, legacy, "command = \"squad\"\nshared_mcp = [\"codegraph\"]\n")
	cfg, from, fresh, err := LoadConfig(path, []string{legacy})
	if err != nil || !fresh || from != legacy || cfg.Command != "" || len(cfg.SharedMCP) != 1 {
		t.Fatalf("legacy: %+v %q %v %v", cfg, from, fresh, err)
	}
	write(t, path, "command = \"sx\"\n")
	if cfg, _, fresh, err := LoadConfig(path, []string{legacy}); err != nil || fresh || cfg.Command != "sx" {
		t.Fatalf("existing: %+v %v %v", cfg, fresh, err)
	}
	write(t, path, "bogus = 1\n")
	if _, _, _, err := LoadConfig(path, nil); err == nil {
		t.Fatal("invalid config accepted")
	}
}

func TestNameCheck(t *testing.T) {
	dir := t.TempDir()
	layout := release.Layout{DataDir: filepath.Join(dir, "data"), BinDir: filepath.Join(dir, "bin")}
	other := filepath.Join(dir, "usr", "sx")
	write(t, other, "")
	look := func(name string) (string, error) {
		switch name {
		case "sx":
			return other, nil
		case "mine":
			return filepath.Join(layout.BinDir, "mine"), nil
		}
		return "", errors.New("not found")
	}
	n := NameCheck{Layout: layout, Accepted: map[string]bool{}, LookPath: look}
	if err := n.Check(config.Config{Command: "sprawl", Aliases: []string{"mine"}}); err != nil {
		t.Fatal(err)
	}
	if err := n.Check(config.Config{Command: "sprawl", Aliases: []string{"sx"}}); !errors.Is(err, ErrNameTaken) {
		t.Fatalf("clash: %v", err)
	}
	if err := n.Check(config.Config{Command: "sprawl", Aliases: []string{"sprawl"}}); !errors.Is(err, ErrDuplicateName) {
		t.Fatalf("duplicate: %v", err)
	}
	if err := n.Check(config.Config{Command: "con"}); !errors.Is(err, config.ErrInvalidCommand) {
		t.Fatal("a Windows device name was accepted")
	}
	if n.Clash("sx") == "" || n.Clash("mine") != "" {
		t.Fatal("Clash")
	}
	accepted := NameCheck{Layout: layout, Accepted: map[string]bool{"gc": true}, LookPath: look}
	if err := accepted.Check(config.Config{Command: "gc"}); err != nil {
		t.Fatalf("a name accepted before must not be re-checked for portability: %v", err)
	}
	if err := n.Check(config.Config{Command: "git"}); !errors.Is(err, config.ErrInvalidCommand) {
		t.Fatalf("invalid: %v", err)
	}
	n.Accepted["sx"] = true
	if err := n.Check(config.Config{Command: "sx"}); err != nil {
		t.Fatalf("accepted name re-checked: %v", err)
	}
	n = NameCheck{Layout: layout, LookPath: look, Self: other}
	if err := n.Check(config.Config{Command: "sx"}); err != nil {
		t.Fatalf("self is no clash: %v", err)
	}
	n = NameCheck{Layout: layout, LookPath: look, Force: true}
	if err := n.Check(config.Config{Command: "sx"}); err != nil {
		t.Fatalf("force: %v", err)
	}
}

func TestMigrateLegacy(t *testing.T) {
	home := t.TempDir()
	for _, k := range []string{"XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME", "XDG_CACHE_HOME"} {
		t.Setenv(k, "")
	}
	old := config.DirsFor(home, "squad")
	write(t, filepath.Join(old.Data, "bin", "squad"), "")
	write(t, filepath.Join(old.State, "guard.log"), "line\n")
	write(t, filepath.Join(old.Config, "config.toml"), "")
	write(t, filepath.Join(old.Cache, "auth.json"), "{}")
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	moved, errs := MigrateLegacy(home, now)
	if len(errs) != 0 || len(moved) != 1 || moved[0].Name != "squad" {
		t.Fatalf("MigrateLegacy = %+v, %v", moved, errs)
	}
	state := config.DirsFor(home, config.Slug).State
	if b, _ := os.ReadFile(filepath.Join(state, "legacy-logs", "squad-guard.log")); string(b) != "line\n" {
		t.Error("guard log not kept")
	}
	for _, gone := range []string{old.Data, old.State, old.Config, old.Cache} {
		if _, err := os.Stat(gone); !os.IsNotExist(err) {
			t.Errorf("%s still exists", gone)
		}
	}
	entries, _ := os.ReadDir(moved[0].Backup)
	if len(entries) != 3 || !strings.HasSuffix(moved[0].Backup, "legacy-squad-20260102-030405") {
		t.Errorf("backup %s holds %d entries", moved[0].Backup, len(entries))
	}
	if again, _ := MigrateLegacy(home, now); len(again) != 0 {
		t.Error("second migration moved something")
	}
}

type fakeRunner struct {
	plugins string
	fail    string
	calls   []string
}

func (f *fakeRunner) Output(_ context.Context, _ string, args ...string) ([]byte, error) {
	cmd := strings.Join(args, " ")
	f.calls = append(f.calls, cmd)
	if f.fail != "" && strings.HasPrefix(cmd, f.fail) {
		return nil, errors.New("boom")
	}
	switch cmd {
	case "plugin list --json":
		return []byte(f.plugins), nil
	case "plugin marketplace list --json":
		return []byte(`[{"name":"sprawl"},{"name":"squad"}]`), nil
	}
	return nil, nil
}

func TestPlugins(t *testing.T) {
	home := t.TempDir()
	store := account.Store{Home: home}
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	accts, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	r := &fakeRunner{plugins: `[{"id":"squad@squad"}]`}
	res := Plugins(ctx, accts, store, r, "/m")
	if len(res) != 1 || res[0].Action != PluginInstalled || len(res[0].Removed) != 1 {
		t.Fatalf("install: %+v (%v)", res, r.calls)
	}
	r = &fakeRunner{plugins: `[{"id":"sprawl@sprawl"}]`}
	if res := Plugins(ctx, accts, store, r, "/m"); res[0].Action != PluginUpdated || res[0].Err != nil {
		t.Fatalf("update: %+v", res)
	}
	r = &fakeRunner{plugins: `[{"id":"sprawl@sprawl"}]`, fail: "plugin update"}
	if res := Plugins(ctx, accts, store, r, "/m"); res[0].Action != PluginFailed {
		t.Fatalf("update failure: %+v", res)
	}
	r = &fakeRunner{fail: "plugin list"}
	if res := Plugins(ctx, accts, store, r, "/m"); res[0].Action != PluginFailed {
		t.Fatalf("list failure: %+v", res)
	}
}

func TestRemovePlugins(t *testing.T) {
	home := t.TempDir()
	store := account.Store{Home: home}
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	accts, _ := store.List()
	r := &fakeRunner{}
	errs := RemovePlugins(context.Background(), accts, r)
	if errs["claude/main"] != nil || strings.Join(r.calls, ";") != "plugin uninstall sprawl@sprawl;plugin marketplace remove sprawl" {
		t.Fatalf("RemovePlugins = %v, calls %v", errs, r.calls)
	}
	r = &fakeRunner{fail: "plugin uninstall"}
	if errs := RemovePlugins(context.Background(), accts, r); errs["claude/main"] == nil {
		t.Fatal("error not reported")
	}
}
