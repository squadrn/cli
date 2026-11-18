package doctor

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"sprawling-agents/internal/account"
	"sprawling-agents/internal/config"
	"sprawling-agents/internal/githook"
	"sprawling-agents/internal/gitver"
	"sprawling-agents/internal/provider"
	"sprawling-agents/internal/release"
)

type fakeAgent struct {
	loggedIn bool
	authErr  error
	plugins  string
}

func (f fakeAgent) Output(_ context.Context, _ string, args ...string) ([]byte, error) {
	if strings.Join(args, " ") == "plugin list --json" {
		if f.plugins == "" {
			return nil, errors.New("no plugins")
		}
		return []byte(f.plugins), nil
	}
	return nil, nil
}

func (f fakeAgent) Auth(context.Context, string) (provider.AuthStatus, error) {
	return provider.AuthStatus{LoggedIn: f.loggedIn, Email: "me@x"}, f.authErr
}
func (fakeAgent) Exec(string, []string) error  { return nil }
func (fakeAgent) LoginArgs() []string          { return nil }
func (fakeAgent) LogoutArgs() []string         { return nil }
func (fakeAgent) WorktreeArgs(string) []string { return nil }
func (fakeAgent) ResumeArgs(string) []string   { return nil }

func find(checks []Check, substr string) (Check, bool) {
	for _, c := range checks {
		if strings.Contains(c.Name, substr) {
			return c, true
		}
	}
	return Check{}, false
}

func env(t *testing.T, ag provider.Agent, agentErr error) Env {
	t.Helper()
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	return Env{
		Store:    account.Store{Home: home},
		Agent:    func(provider.Spec) (provider.Agent, error) { return ag, agentErr },
		Layout:   release.Layout{DataDir: filepath.Join(home, ".local/share/sprawl"), BinDir: filepath.Join(home, ".local/bin")},
		LookPath: func(string) (string, error) { return "", errors.New("missing") },
		Names:    []string{"sprawl"},
		Command:  "sprawl",
		Timeout:  5 * time.Second,
	}
}

func TestGuardLayers(t *testing.T) {
	e := env(t, fakeAgent{}, nil)
	checks := e.guard(context.Background())
	if c, _ := find(checks, "layer 1"); c.Status != OK {
		t.Errorf("layer 1: %+v", c)
	}
	if c, _ := find(checks, "layer 2"); c.Status != Warn || c.Fix != "re-run install.sh" {
		t.Errorf("layer 2 missing: %+v", c)
	}
	if err := githook.Install(e.Layout.GitHooks(), e.Layout.Binary()); err != nil {
		t.Fatal(err)
	}
	if c, _ := find(e.guard(context.Background()), "layer 2"); c.Status != OK {
		t.Errorf("layer 2 installed: %+v", c)
	}
	off := false
	e.Config = config.Config{GitHooks: &off}
	if c, _ := find(e.guard(context.Background()), "layer 2"); c.Status != Warn || !strings.Contains(c.Fix, "git_hooks true") {
		t.Errorf("layer 2 off: %+v", c)
	}
}

func TestBranchProtection(t *testing.T) {
	e := env(t, fakeAgent{}, nil)
	e.Repo = "/r"
	reply := func(view string, protErr error) GH {
		return func(_ context.Context, args ...string) ([]byte, error) {
			if args[0] == "repo" {
				return []byte(view), nil
			}
			return nil, protErr
		}
	}
	for name, tt := range map[string]struct {
		gh     GH
		status Status
		want   string
	}{
		"protected":   {reply("me/app main\n", nil), OK, "layer 3 (server) protects main on me/app"},
		"unprotected": {reply("me/app main", errors.New("gh: Branch not protected (HTTP 404)")), Warn, "main on me/app is not protected"},
		"forbidden":   {reply("me/app main", errors.New("HTTP 403")), Warn, "needs admin"},
		"other":       {reply("me/app main", errors.New("network down")), Warn, "unknown: network down"},
	} {
		e.GH = tt.gh
		checks := e.branchProtection(context.Background())
		if len(checks) != 1 || checks[0].Status != tt.status || !strings.Contains(checks[0].Name, tt.want) {
			t.Errorf("%s: %+v", name, checks)
		}
	}
	e.GH = func(context.Context, ...string) ([]byte, error) { return nil, errors.New("not github") }
	if checks := e.branchProtection(context.Background()); checks != nil {
		t.Errorf("not GitHub: %+v", checks)
	}
	e.GH = reply("garbage", nil)
	if checks := e.branchProtection(context.Background()); checks != nil {
		t.Errorf("garbage: %+v", checks)
	}
	e.Repo = ""
	if checks := e.branchProtection(context.Background()); checks != nil {
		t.Errorf("outside a repo: %+v", checks)
	}
}

func TestDenyRules(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("XDG_CONFIG_HOME", "")
	e := env(t, fakeAgent{}, nil)
	e.Layout.DataDir = config.DirsFor(e.Store.Home, config.Slug).Data
	settings := filepath.Join(e.Store.Home, ".claude", "settings.json")
	if c, _ := find(e.settings(), "edit sprawl"); c.Status != Warn || !strings.Contains(c.Fix, `"Edit(~/.local/share/sprawl/**)"`) {
		t.Fatalf("no settings: %+v", c)
	}
	body := `{"permissions":{"deny":["Edit(~/.local/share/sprawl/**)","Write(~/.local/share/sprawl/**)","Edit(~/.config/sprawl/**)","Write(~/.config/sprawl/**)"]}}`
	if err := os.WriteFile(settings, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if c, _ := find(e.settings(), "cannot edit sprawl"); c.Status != OK {
		t.Fatalf("with rules: %+v", c)
	}
	if err := os.WriteFile(settings, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if c, _ := find(e.settings(), "not valid JSON"); c.Status != Fail {
		t.Fatalf("invalid JSON: %+v", c)
	}
}

func TestAccountChecks(t *testing.T) {
	e := env(t, fakeAgent{loggedIn: true, plugins: `[{"id":"sprawl@sprawl","enabled":false}]`}, nil)
	dir := filepath.Join(e.Store.Home, ".claude-work")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	checks := e.accounts(context.Background())
	if c, _ := find(checks, "plugin disabled"); c.Status != Fail || c.Fix != "claude plugin enable sprawl@sprawl" {
		t.Errorf("disabled plugin: %+v", c)
	}
	if c, _ := find(checks, "readable by other users"); runtime.GOOS != "windows" && (c.Status != Warn || c.Fix != "chmod 700 ~/.claude-work") {
		t.Errorf("permissions: %+v", c)
	}

	e = env(t, fakeAgent{authErr: errors.New("timeout")}, nil)
	if c, _ := find(e.accounts(context.Background()), "login status unavailable"); c.Status != Fail {
		t.Errorf("auth error: %+v", c)
	}
	if c, _ := find(e.accounts(context.Background()), "cannot list plugins"); c.Status != Warn {
		t.Errorf("plugin list error: %+v", c)
	}
	e = env(t, nil, errors.New("claude is not installed"))
	if c, _ := find(e.accounts(context.Background()), "not installed"); c.Status != Fail || c.Fix != provider.Claude.InstallHint {
		t.Errorf("no agent: %+v", c)
	}
}

func TestStatusText(t *testing.T) {
	for s, want := range map[Status]string{OK: "ok", Warn: "warn", Fail: "fail"} {
		if b, _ := s.MarshalText(); string(b) != want {
			t.Errorf("%d = %s", s, b)
		}
	}
}

func TestRunWithEverythingMissing(t *testing.T) {
	e := env(t, fakeAgent{loggedIn: true, plugins: "[]"}, nil)
	e.Signed = true
	if _, ok := find(Run(context.Background(), e), "release signatures verified"); !ok {
		t.Error("signed build not reported")
	}
	e.Signed = false
	var checks []Check
	e.Latest, e.Version = "v1.2.0", "1.0.0"
	checks = Run(context.Background(), e)
	for _, want := range []string{"sprawl v1.2.0 is available (you have v1.0.0)", "cannot verify release signatures", "claude not on PATH", "sprawl not on PATH", "git missing", "plugin files not installed", "no settings.json", "logged in as me@x", "sprawl plugin not installed"} {
		if _, ok := find(checks, want); !ok {
			t.Errorf("missing check %q", want)
		}
	}
}

func TestGitCheck(t *testing.T) {
	e := env(t, fakeAgent{}, nil)
	if c := e.gitCheck(context.Background()); c.Status != OK || c.Name != "git" {
		t.Errorf("no detector: %+v", c)
	}
	e.GitHint = "brew install git"
	e.Git = func(context.Context) (gitver.Version, error) {
		return gitver.Version{Major: 2, Minor: 30, Patch: 2}, nil
	}
	if c := e.gitCheck(context.Background()); c.Status != Fail || !strings.Contains(c.Name, "git 2.30.2 is too old (sprawl needs 2.31 or newer)") || c.Fix != "brew install git" {
		t.Errorf("too old: %+v", c)
	}
	e.Git = func(context.Context) (gitver.Version, error) { return gitver.Version{Major: 2, Minor: 47}, nil }
	if c := e.gitCheck(context.Background()); c.Status != OK || c.Name != "git 2.47.0" {
		t.Errorf("current: %+v", c)
	}
	e.Git = func(context.Context) (gitver.Version, error) { return gitver.Version{}, errors.New("git is broken") }
	if c := e.gitCheck(context.Background()); c.Status != Fail {
		t.Errorf("broken: %+v", c)
	}
}
