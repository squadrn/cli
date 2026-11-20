package doctor

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/squadrn/squadrn/internal/account"
	"github.com/squadrn/squadrn/internal/config"
	"github.com/squadrn/squadrn/internal/githook"
	"github.com/squadrn/squadrn/internal/gitver"
	"github.com/squadrn/squadrn/internal/provider"
	"github.com/squadrn/squadrn/internal/release"
)

type fakeAgent struct {
	provider.Agent
	loggedIn bool
	authErr  error
	plugins  string
}

func (f fakeAgent) Output(_ context.Context, _ string, args ...string) ([]byte, error) {
	if len(args) == 1 && args[0] == "--version" && f.loggedIn {
		return []byte("2.1.0 (Claude Code)\n"), nil
	}
	return nil, nil
}

func (f fakeAgent) Auth(context.Context, string) (provider.AuthStatus, error) {
	return provider.AuthStatus{LoggedIn: f.loggedIn, Email: "me@x"}, f.authErr
}

func (f fakeAgent) PluginState(context.Context, string) (installed, enabled bool, err error) {
	if f.plugins == "" {
		return false, false, errors.New("no plugins")
	}
	var ps []struct {
		ID      string `json:"id"`
		Enabled bool   `json:"enabled"`
	}
	_ = json.Unmarshal([]byte(f.plugins), &ps)
	for _, p := range ps {
		if p.ID == provider.Claude.Plugin {
			return true, p.Enabled, nil
		}
	}
	return false, false, nil
}

func (fakeAgent) EnablePluginArgs() []string {
	return []string{"plugin", "enable", provider.Claude.Plugin}
}

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
		Layout:   release.Layout{DataDir: filepath.Join(home, ".local/share/squadrn"), BinDir: filepath.Join(home, ".local/bin")},
		LookPath: func(string) (string, error) { return "", errors.New("missing") },
		Names:    []string{"squadrn"},
		Command:  "squadrn",
		Timeout:  5 * time.Second,
	}
}

// fakeInstall puts the real plugin hooks.json and a stand-in hook entry point that prints
// decision into the layout, as install.sh would.
func fakeInstall(t *testing.T, e Env, decision string) {
	t.Helper()
	root := filepath.Join(e.Layout.DataDir, "plugin")
	for _, d := range []string{filepath.Join(root, "hooks"), filepath.Join(root, "bin"), filepath.Dir(e.Layout.Binary())} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	hooks, err := os.ReadFile(filepath.Join("..", "..", "plugin", "hooks", "hooks.json"))
	if err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\ncat >/dev/null\n[ \"$1\" = guard-git ] && printf '{\"hookSpecificOutput\":{\"permissionDecision\":\"%s\"}}\\n' " + decision + "\n"
	for p, b := range map[string][]byte{
		filepath.Join(root, "hooks", "hooks.json"): hooks,
		filepath.Join(root, "bin", "squadrn-hook"): []byte(script),
		e.Layout.Binary():                          []byte("bin"),
	} {
		if err := os.WriteFile(p, b, 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

func TestGuardLayers(t *testing.T) {
	e := env(t, fakeAgent{}, nil)
	checks := e.guard(context.Background())
	if c, _ := find(checks, "layer 1"); c.Status != Fail || !strings.Contains(c.Name, "not installed") {
		t.Errorf("layer 1 without an install: %+v", c)
	}
	if c, _ := find(checks, "layer 2"); c.Status != Warn || c.Fix != reinstallHint {
		t.Errorf("layer 2 missing: %+v", c)
	}
	if err := githook.Install(e.Layout.GitHooks(), e.Layout.Binary(), e.configFile()); err != nil {
		t.Fatal(err)
	}
	if c, _ := find(e.guard(context.Background()), "layer 2"); c.Status != OK {
		t.Errorf("layer 2 installed: %+v", c)
	}
	if err := githook.Install(e.Layout.GitHooks(), e.Layout.Binary(), "/elsewhere/config.toml"); err != nil {
		t.Fatal(err)
	}
	if c, _ := find(e.guard(context.Background()), "layer 2"); c.Status != Warn {
		t.Errorf("layer 2 for another config file: %+v", c)
	}
	off := false
	e.Config = config.Config{GitHooks: &off}
	if c, _ := find(e.guard(context.Background()), "layer 2"); c.Status != Warn || !strings.Contains(c.Fix, "git_hooks true") {
		t.Errorf("layer 2 off: %+v", c)
	}
}

func TestHookProbe(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the Windows hooks.json starts squadrn.exe itself; covered by the e2e harness")
	}
	e := env(t, fakeAgent{}, nil)
	fakeInstall(t, e, "deny")
	if c := e.hookProbe(context.Background()); c.Status != OK {
		t.Errorf("deny: %+v", c)
	}
	fakeInstall(t, e, "allow")
	if c := e.hookProbe(context.Background()); c.Status != Fail || !strings.Contains(c.Name, "did not deny") {
		t.Errorf("allow: %+v", c)
	}
	hooks := filepath.Join(e.Layout.DataDir, "plugin", "hooks", "hooks.json")
	for body, want := range map[string]string{
		`{`:                           "invalid",
		`{"hooks":{"PreToolUse":[]}}`: "no guard-git",
		`{"hooks":{"PreToolUse":[{"hooks":[{"command":"sh","args":["-c","${CLAUDE_PLUGIN_ROOT}/bin/squadrn-hook guard-git"]}]}]}}`: "",
	} {
		if err := os.WriteFile(hooks, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		c := e.hookProbe(context.Background())
		if want == "" && c.Status != Fail || want != "" && !strings.Contains(c.Name, want) {
			t.Errorf("%s: %+v", body, c)
		}
	}
	_ = os.Remove(hooks)
	if c := e.hookProbe(context.Background()); !strings.Contains(c.Name, "plugin hooks not installed") {
		t.Errorf("missing hooks.json: %+v", c)
	}
}

func TestBranchProtection(t *testing.T) {
	e := env(t, fakeAgent{}, nil)
	e.Repo = "/r"
	type reply struct {
		body string
		err  error
	}
	gh := func(view string, rules, prot reply) GH {
		return func(_ context.Context, args ...string) ([]byte, error) {
			switch {
			case args[0] == "repo":
				return []byte(view), nil
			case strings.Contains(args[1], "/rules/branches/"):
				return []byte(rules.body), rules.err
			}
			return []byte(prot.body), prot.err
		}
	}
	none := reply{"[]", nil}
	strict := reply{`{"allow_force_pushes":{"enabled":false},"allow_deletions":{"enabled":false},"enforce_admins":{"enabled":true}}`, nil}
	for name, tt := range map[string]struct {
		gh     GH
		status Status
		want   string
	}{
		"protected":   {gh("me/app main\n", none, strict), OK, "layer 3 (server) protects main on me/app"},
		"ruleset":     {gh("me/app main", reply{`[{"type":"deletion"},{"type":"non_fast_forward"}]`, nil}, reply{"", errors.New("HTTP 404")}), OK, "(ruleset)"},
		"weak rules":  {gh("me/app main", reply{`[{"type":"deletion"}]`, nil}, reply{"", errors.New("gh: Branch not protected (HTTP 404)")}), Warn, "main on me/app is not protected"},
		"bad rules":   {gh("me/app main", reply{`x`, nil}, strict), OK, "protects main"},
		"no rules":    {gh("me/app main", reply{"", errors.New("HTTP 404")}, strict), OK, "protects main"},
		"force":       {gh("me/app main", none, reply{`{"allow_force_pushes":{"enabled":true}}`, nil}), Warn, "allows force pushes"},
		"deletions":   {gh("me/app main", none, reply{`{"allow_deletions":{"enabled":true}}`, nil}), Warn, "can be deleted"},
		"unreadable":  {gh("me/app main", none, reply{`x`, nil}), Warn, "unreadable"},
		"unprotected": {gh("me/app main", none, reply{"", errors.New("gh: Branch not protected (HTTP 404)")}), Warn, "main on me/app is not protected"},
		"plan":        {gh("me/app main", none, reply{"", errors.New("Upgrade to GitHub Pro or make this repository public to enable this feature. (HTTP 403)")}), Warn, "needs GitHub Pro"},
		"forbidden":   {gh("me/app main", none, reply{"", errors.New("HTTP 403")}), Warn, "needs admin"},
		"other":       {gh("me/app main", none, reply{"", errors.New("network down")}), Warn, "unknown: network down"},
	} {
		e.GH = tt.gh
		checks := e.branchProtection(context.Background())
		if len(checks) == 0 || checks[0].Status != tt.status || !strings.Contains(checks[0].Name, tt.want) {
			t.Errorf("%s: %+v", name, checks)
		}
	}
	e.GH = gh("me/app main", none, reply{`{"enforce_admins":{"enabled":false}}`, nil})
	if c, _ := find(e.branchProtection(context.Background()), "admins can bypass"); c.Status != Warn {
		t.Errorf("enforce_admins off: %+v", c)
	}
	e.GH = func(context.Context, ...string) ([]byte, error) { return nil, errors.New("not github") }
	if checks := e.branchProtection(context.Background()); checks != nil {
		t.Errorf("not GitHub: %+v", checks)
	}
	e.GH = gh("garbage", none, strict)
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
	if c, _ := find(e.settings(), "edit squadrn"); c.Status != Warn || !strings.HasSuffix(c.Fix, `: ["Edit(~/.local/share/squadrn/**)","Edit(~/.config/squadrn/**)"]`) {
		t.Fatalf("no settings: %+v", c)
	}
	for _, tt := range []struct {
		name string
		deny string
		want Status
	}{
		{"edit rules", `"Edit(~/.local/share/squadrn/**)","Edit(~/.config/squadrn/**)"`, OK},
		{"edit and write rules", `"Edit(~/.local/share/squadrn/**)","Write(~/.local/share/squadrn/**)","Edit(~/.config/squadrn/**)","Write(~/.config/squadrn/**)"`, OK},
		{"write rules only", `"Write(~/.local/share/squadrn/**)","Write(~/.config/squadrn/**)"`, Warn},
	} {
		if err := os.WriteFile(settings, []byte(`{"permissions":{"deny":[`+tt.deny+`]}}`), 0o600); err != nil {
			t.Fatal(err)
		}
		if c, _ := find(e.settings(), "edit squadrn"); c.Status != tt.want || strings.Contains(c.Fix, "Write(") {
			t.Fatalf("%s: %+v", tt.name, c)
		}
	}
	if err := os.WriteFile(settings, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if c, _ := find(e.settings(), "not valid JSON"); c.Status != Fail {
		t.Fatalf("invalid JSON: %+v", c)
	}
}

func TestAccountChecks(t *testing.T) {
	e := env(t, fakeAgent{loggedIn: true, plugins: `[{"id":"squadrn@squadrn","enabled":false}]`}, nil)
	dir := filepath.Join(e.Store.Home, ".claude-work")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, account.Marker), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	checks := e.accounts(context.Background())
	if c, _ := find(checks, "plugin disabled"); c.Status != Fail || c.Fix != "claude plugin enable squadrn@squadrn" {
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
	for _, want := range []string{"squadrn v1.2.0 is available (you have v1.0.0)", "cannot verify release signatures", "claude not on PATH", "squadrn not on PATH", "git missing", "plugin files not installed", "no settings.json", "logged in as me@x", "squadrn plugin not installed"} {
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
	if c := e.gitCheck(context.Background()); c.Status != Fail || !strings.Contains(c.Name, "git 2.30.2 is too old (squadrn needs 2.31 or newer)") || c.Fix != "brew install git" {
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

func TestPathFixNamesTheDirectory(t *testing.T) {
	dir := filepath.Join("home", "u", ".local", "bin")
	fix := PathFix(dir)
	want := `export PATH="` + dir + `:$PATH"`
	if runtime.GOOS == "windows" {
		want = `[Environment]::SetEnvironmentVariable("Path", [Environment]::GetEnvironmentVariable("Path", "User") + ";` + dir + `", "User")`
	}
	if fix != want {
		t.Fatalf("PathFix = %s, want %s", fix, want)
	}
}

func TestConfigAndInstalledRelease(t *testing.T) {
	e := env(t, fakeAgent{}, nil)
	e.ConfigErr = errors.New("config x: unknown key")
	if c, _ := find(e.install(context.Background()), "config invalid: config x: unknown key"); c.Status != Fail || !strings.Contains(c.Fix, "squadrn doctor") {
		t.Fatalf("config check: %+v", c)
	}
	e.ConfigErr = nil
	if _, found := find(e.install(context.Background()), "installed release"); found {
		t.Fatal("no install: nothing to compare")
	}
	bin := e.Layout.Binary()
	for p, body := range map[string]string{
		bin: "bin",
		filepath.Join(e.Layout.DataDir, "VERSION"):                            "1.1.0\n",
		filepath.Join(e.Layout.DataDir, ".claude-plugin", "marketplace.json"): "{}",
	} {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	e.Self, e.Version = bin, "1.2.0"
	if c, _ := find(e.install(context.Background()), "the installed copy of v1.2.0 is incomplete"); c.Status != Fail || c.Fix != "squadrn self-update" {
		t.Fatalf("mismatch: %+v", c)
	}
	e.Version = "v1.1.0"
	if c, _ := find(e.install(context.Background()), "installed release 1.1.0"); c.Status != OK {
		t.Fatalf("match: %+v", c)
	}
	e.Version = "dev"
	if _, found := find(e.install(context.Background()), "installed release"); found {
		t.Fatal("a development build is not compared")
	}
	e.Version, e.Self = "1.2.0", filepath.Join(t.TempDir(), "other")
	if _, found := find(e.install(context.Background()), "installed release"); found {
		t.Fatal("another binary is not compared")
	}
}

func TestVersions(t *testing.T) {
	e := env(t, fakeAgent{loggedIn: true}, nil)
	if len(e.versions(context.Background())) != 0 {
		t.Fatal("version of a CLI that is not on PATH")
	}
	e.LookPath = func(string) (string, error) { return "/bin/claude", nil }
	if c, _ := find(e.versions(context.Background()), "claude 2.1.0 (Claude Code)"); c.Status != OK || c.Group != "install" {
		t.Errorf("version: %+v", c)
	}
	e.Agent = func(provider.Spec) (provider.Agent, error) { return fakeAgent{}, nil }
	if c, _ := find(e.versions(context.Background()), "--version failed"); c.Status != Warn {
		t.Errorf("no version: %+v", c)
	}
	e.Agent = func(provider.Spec) (provider.Agent, error) { return nil, errors.New("x") }
	if len(e.versions(context.Background())) != 0 {
		t.Error("agent error reported twice")
	}
}

func TestUsageTrackingLine(t *testing.T) {
	e := env(t, fakeAgent{loggedIn: true}, nil)
	e.Command = "srn"
	e.Usage = func(account.Account) bool { return false }
	if c, ok := find(e.accounts(context.Background()), "usage tracking: off (srn usage setup claude/main)"); !ok || c.Status != OK {
		t.Errorf("off: %+v", c)
	}
	e.Usage = func(account.Account) bool { return true }
	if c, ok := find(e.accounts(context.Background()), "usage tracking: on"); !ok || c.Status != OK {
		t.Errorf("on: %+v", c)
	}
}
