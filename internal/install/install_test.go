package install

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/squadrn/squadrn/internal/account"
	"github.com/squadrn/squadrn/internal/config"
	"github.com/squadrn/squadrn/internal/release"
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
	if err := n.Check(config.Config{Command: "squadrn", Aliases: []string{"mine"}}); err != nil {
		t.Fatal(err)
	}
	if err := n.Check(config.Config{Command: "squadrn", Aliases: []string{"sx"}}); !errors.Is(err, ErrNameTaken) {
		t.Fatalf("clash: %v", err)
	}
	if err := n.Check(config.Config{Command: "squadrn", Aliases: []string{"squadrn"}}); !errors.Is(err, ErrDuplicateName) {
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
		return []byte(`[{"name":"squadrn"}]`), nil
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

	r := &fakeRunner{plugins: `[]`}
	res := Plugins(ctx, accts, store, r, "/m")
	if len(res) != 1 || res[0].Action != PluginInstalled {
		t.Fatalf("install: %+v (%v)", res, r.calls)
	}
	r = &fakeRunner{plugins: `[{"id":"squadrn@squadrn"}]`}
	if res := Plugins(ctx, accts, store, r, "/m"); res[0].Action != PluginUpdated || res[0].Err != nil {
		t.Fatalf("update: %+v", res)
	}
	r = &fakeRunner{plugins: `[{"id":"squadrn@squadrn"}]`, fail: "plugin update"}
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
	if errs["claude/main"] != nil || strings.Join(r.calls, ";") != "plugin uninstall squadrn@squadrn;plugin marketplace remove squadrn" {
		t.Fatalf("RemovePlugins = %v, calls %v", errs, r.calls)
	}
	r = &fakeRunner{fail: "plugin uninstall"}
	if errs := RemovePlugins(context.Background(), accts, r); errs["claude/main"] == nil {
		t.Fatal("error not reported")
	}
}
