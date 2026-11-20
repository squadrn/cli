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
	"github.com/squadrn/squadrn/internal/provider"
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

type fakeAgent struct {
	provider.Agent
	provider.GuardInstaller
	installed bool
	fail      string
	calls     []string
}

func (f *fakeAgent) step(name string) error {
	f.calls = append(f.calls, name)
	if f.fail == name {
		return errors.New("boom")
	}
	return nil
}

func (f *fakeAgent) InstallPlugin(_ context.Context, _ string, dir string) (bool, error) {
	if err := f.step("install " + dir); err != nil {
		return false, err
	}
	return !f.installed, nil
}

func (f *fakeAgent) UpdatePlugin(context.Context, string) error { return f.step("update") }

func (f *fakeAgent) RemovePlugin(context.Context, string) error { return f.step("remove") }

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

	f := &fakeAgent{}
	if res := Plugins(ctx, accts, f, "/m"); len(res) != 1 || res[0].Action != PluginInstalled || strings.Join(f.calls, ";") != "install /m" {
		t.Fatalf("install: %+v (%v)", res, f.calls)
	}
	f = &fakeAgent{installed: true}
	if res := Plugins(ctx, accts, f, "/m"); res[0].Action != PluginUpdated || res[0].Err != nil || strings.Join(f.calls, ";") != "install /m;update" {
		t.Fatalf("update: %+v (%v)", res, f.calls)
	}
	if res := Plugins(ctx, accts, &fakeAgent{installed: true, fail: "update"}, "/m"); res[0].Action != PluginFailed {
		t.Fatalf("update failure: %+v", res)
	}
	if res := Plugins(ctx, accts, &fakeAgent{fail: "install /m"}, "/m"); res[0].Action != PluginFailed {
		t.Fatalf("install failure: %+v", res)
	}
}

func TestRemovePlugins(t *testing.T) {
	home := t.TempDir()
	store := account.Store{Home: home}
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	accts, _ := store.List()
	if errs := RemovePlugins(context.Background(), accts, &fakeAgent{}); len(errs) != 1 || errs["claude/main"] != nil {
		t.Fatalf("RemovePlugins = %v", errs)
	}
	if errs := RemovePlugins(context.Background(), accts, &fakeAgent{fail: "remove"}); errs["claude/main"] == nil {
		t.Fatal("error not reported")
	}
}
