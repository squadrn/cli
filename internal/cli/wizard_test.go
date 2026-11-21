package cli

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/squadrn/squadrn-cli/internal/account"
	"github.com/squadrn/squadrn-cli/internal/config"
	"github.com/squadrn/squadrn-cli/internal/fsx"
	"github.com/squadrn/squadrn-cli/internal/provider"
	"github.com/squadrn/squadrn-cli/internal/release"
	"github.com/squadrn/squadrn-cli/internal/ui/prompt"
)

// wizAgent is a provider CLI for wizard tests: logged in when its config dir holds .loggedin,
// which its login writes.
type wizAgent struct{ spec provider.Spec }

func (f wizAgent) Output(context.Context, string, ...string) ([]byte, error) {
	return []byte("1.2.3"), nil
}
func (wizAgent) Exec(string, []string) error { return nil }
func (wizAgent) Run(dir string, _ []string) error {
	return os.WriteFile(filepath.Join(dir, ".loggedin"), nil, 0o600)
}

func (wizAgent) Auth(_ context.Context, dir string) (provider.AuthStatus, error) {
	_, err := os.Stat(filepath.Join(dir, ".loggedin"))
	return provider.AuthStatus{LoggedIn: err == nil, Email: "me@example.com"}, nil
}
func (wizAgent) LoginArgs() []string  { return []string{"login"} }
func (wizAgent) LogoutArgs() []string { return []string{"logout"} }

// wizApp is an app in a throwaway home whose PATH holds the given agent CLIs (fakes).
func wizApp(t *testing.T, clis ...string) (*app, *prompt.Script) {
	t.Helper()
	home := t.TempDir()
	bin := filepath.Join(home, "bin")
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, c := range clis {
		for _, name := range []string{c, c + ".exe"} {
			if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\n"), 0o755); err != nil {
				t.Fatal(err)
			}
		}
	}
	t.Setenv("PATH", bin)
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	for _, k := range []string{"XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME", "XDG_CACHE_HOME", "SQUADRN_MARKETPLACE", "CLAUDE_CONFIG_DIR", "CODEX_HOME"} {
		t.Setenv(k, "")
	}
	s := &prompt.Script{}
	a := &app{
		cmd:         "srn",
		store:       account.Store{Home: home, DefaultProvider: provider.Default},
		layout:      release.DefaultLayout(home),
		stdin:       strings.NewReader(""),
		stdout:      io.Discard,
		stderr:      io.Discard,
		interactive: true,
		cache:       provider.AuthCache{Path: filepath.Join(home, "auth.json")},
		now:         time.Now,
		agents:      func(spec provider.Spec) (provider.Agent, error) { return wizAgent{spec}, nil },
		prompt:      s,
	}
	return a, s
}

func (a *app) dir(name string) string { return filepath.Join(a.store.Home, name) }

func TestWizardHappyPath(t *testing.T) {
	a, s := wizApp(t, "claude", "codex")
	s.Answers = []any{"codex", "work", "Recommended", true, true}
	if err := a.addWizard(addOptions{}); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(s.Asked, ","); got != "Provider,Account name,Setup,Review,Log in now?" {
		t.Fatalf("asked %s", got)
	}
	if !fsx.IsFile(a.dir(".codex-work/.squadrn-account")) || !fsx.IsFile(a.dir(".codex-work/.loggedin")) {
		t.Fatal("codex/work was not created and logged in")
	}
	for _, want := range []string{
		"┌  New account\n",
		"◇  Provider ··", " Codex CLI 1.2.3\n",
		"◇  Account name ··", " work\n",
		"◇  Setup ··", " Recommended\n",
		"│  account   codex/work → ~/.codex-work (private)\n",
		"│  profile   standard   ",
		"│  Create codex/work? Yes\n",
		"◇  Created codex/work\n│  ✓ created ~/.codex-work (private)\n",
		"│  ✓ default account: a bare srn runs codex/work\n",
		"│  ↗ codex login …\n│  ✓ logged in as me@example.com\n",
		"└  Ready in ", " · start: srn work\n",
		"   Same without questions: srn account add codex/work --default --login\n",
	} {
		if !strings.Contains(s.Out.String(), want) {
			t.Errorf("transcript lacks %q:\n%s", want, s.Out.String())
		}
	}
	if a.cfg.DefaultAccount != "codex/work" {
		t.Errorf("the provider's first account is the default: %q", a.cfg.DefaultAccount)
	}
}

func TestWizardCustomizeAndFlags(t *testing.T) {
	a, s := wizApp(t, "claude", "codex")
	s.Answers = []any{"claude", "deep", "Customize", "strict", []string{"privacy"}, false, false, true, false}
	if err := a.addWizard(addOptions{}); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(s.Asked, ","); got != "Provider,Account name,Setup,Profile,Options,Track usage limits,Make it the default account,Review,Log in now?" {
		t.Fatalf("asked %s", got)
	}
	m, _, _ := account.ReadManaged(a.dir(".claude-deep"))
	if m.Profile != "strict" || !slices.Equal(m.Options, []string{"privacy"}) {
		t.Fatalf("managed %+v", m)
	}
	out := s.Out.String()
	for _, want := range []string{
		"Same without questions: srn account add claude/deep --profile strict --with privacy --no-usage --no-login\n",
		"▲  not logged in · srn account login claude/deep\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("transcript lacks %q:\n%s", want, out)
		}
	}
	if a.cfg.DefaultAccount != "" {
		t.Errorf("no default: %q", a.cfg.DefaultAccount)
	}

	// Flags answer their questions: provider, profile, options, usage, default and login.
	a, s = wizApp(t, "claude", "codex")
	no := false
	s.Answers = []any{"flagged", "Customize", true}
	if err := a.addWizard(addOptions{provider: "claude", profile: "minimal", with: []string{}, usage: &no, login: &no, makeDefault: true}); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(s.Asked, ","); got != "Account name,Setup,Review" {
		t.Fatalf("asked %s", got)
	}
	if a.cfg.DefaultAccount != "claude/flagged" {
		t.Errorf("--default: %q", a.cfg.DefaultAccount)
	}
	if !strings.Contains(s.Out.String(), "srn account add claude/flagged --profile minimal --no-usage --default --no-login") {
		t.Errorf("command:\n%s", s.Out.String())
	}
}

func TestWizardEscGoesBackThroughEveryStep(t *testing.T) {
	a, s := wizApp(t, "claude", "codex")
	back := prompt.ErrBack
	s.Answers = []any{
		"claude", "work", "Customize", "strict",
		[]string{},
		true, false,
		back, back, back, back, back, back, back, back,
	}
	err := a.addWizard(addOptions{})
	if !errors.Is(err, prompt.ErrCancelled) || exitCode(err) != exitCancelled {
		t.Fatalf("esc on the first question cancels: %v", err)
	}
	want := "Provider,Account name,Setup,Profile,Options,Track usage limits,Make it the default account,Review," +
		"Make it the default account,Track usage limits,Options,Profile,Setup,Account name,Provider"
	if got := strings.Join(s.Asked, ","); got != want {
		t.Fatalf("asked\n%s\nwant\n%s", got, want)
	}
	if fsx.IsDir(a.dir(".claude-work")) || !strings.HasSuffix(s.Out.String(), "■  Cancelled · nothing changed\n") {
		t.Fatalf("cancel changed something:\n%s", s.Out.String())
	}
}

func TestWizardCancelAtReviewChangesNothing(t *testing.T) {
	a, s := wizApp(t, "claude")
	before := tree(t, a.store.Home)
	s.Answers = []any{"work", "Recommended", false}
	if err := a.addWizard(addOptions{}); !errors.Is(err, prompt.ErrCancelled) {
		t.Fatalf("no at the review cancels: %v", err)
	}
	if got := strings.Join(s.Asked, ","); got != "Account name,Setup,Review" {
		t.Fatalf("one CLI: no provider question; asked %s", got)
	}
	if after := tree(t, a.store.Home); after != before {
		t.Fatalf("tree changed:\n%s\n---\n%s", before, after)
	}
	// Ctrl-C (or the end of the input) at any question cancels too.
	a, s = wizApp(t, "claude")
	s.Answers = []any{"work"}
	if err := a.addWizard(addOptions{}); !errors.Is(err, prompt.ErrCancelled) {
		t.Fatalf("end of input: %v", err)
	}
}

func tree(t *testing.T, root string) string {
	t.Helper()
	var lines []string
	err := filepath.WalkDir(root, func(p string, _ os.DirEntry, err error) error {
		rel, _ := filepath.Rel(root, p)
		lines = append(lines, rel)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return strings.Join(lines, "\n")
}

func TestWizardBranches(t *testing.T) {
	// 2a: an existing account; "Choose another name" goes back to the name, "Repair it" repairs.
	a, s := wizApp(t, "claude")
	if err := os.MkdirAll(a.dir(".claude-work"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := account.Mark(account.Account{Dir: a.dir(".claude-work")}); err != nil {
		t.Fatal(err)
	}
	s.Answers = []any{"work", "Choose another name", "work", prompt.ErrBack, "work", "Repair it", true, false}
	if err := a.addWizard(addOptions{}); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(s.Asked, ","); got != "Account name,claude/work already exists,Account name,claude/work already exists,Account name,claude/work already exists,Review,Log in now?" {
		t.Fatalf("asked %s", got)
	}
	if out := s.Out.String(); !strings.Contains(out, "Repair claude/work? Yes") || !strings.Contains(out, "srn account repair claude/work") {
		t.Fatalf("repair:\n%s", out)
	}

	// 2b: a dir another tool made; adopting it marks it.
	a, s = wizApp(t, "claude")
	if err := os.MkdirAll(a.dir(".claude-tool"), 0o700); err != nil {
		t.Fatal(err)
	}
	s.Answers = []any{"tool", "Adopt it", true, false}
	if err := a.addWizard(addOptions{}); err != nil {
		t.Fatal(err)
	}
	if !fsx.IsFile(a.dir(".claude-tool/.squadrn-account")) || !strings.Contains(s.Out.String(), "◇  Adopted claude/tool") {
		t.Fatalf("adopt:\n%s", s.Out.String())
	}
	if !strings.Contains(s.Out.String(), "srn account add claude/tool --adopt") {
		t.Fatalf("command:\n%s", s.Out.String())
	}

	// 2c: main sets up the provider's default account.
	a, s = wizApp(t, "claude")
	s.Answers = []any{"main", "Set up claude/main", true, false}
	if err := a.addWizard(addOptions{}); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(s.Asked, ","); got != "Account name,main is the default account,Review,Log in now?" {
		t.Fatalf("asked %s", got)
	}
	if !strings.Contains(s.Out.String(), "Set up claude/main? Yes") {
		t.Fatalf("main:\n%s", s.Out.String())
	}
}

func TestWizardCrossProviderName(t *testing.T) {
	a, s := wizApp(t, "claude", "codex")
	if err := os.MkdirAll(a.dir(".codex-work"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := account.Mark(account.Account{Dir: a.dir(".codex-work")}); err != nil {
		t.Fatal(err)
	}
	s.Answers = []any{"claude", "work", "Recommended", true, false}
	if err := a.addWizard(addOptions{}); err != nil {
		t.Fatal(err)
	}
	out := s.Out.String()
	for _, want := range []string{
		"│  \"work\" is also codex/work: start it with srn claude/work\n",
		"start: srn claude/work\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("transcript lacks %q:\n%s", want, out)
		}
	}
	w := &wizard{a: a, spec: provider.Codex}
	if h := w.nameHint("solo"); h.Warn || !strings.Contains(h.Text, "~/.codex-solo · start with: srn solo") {
		t.Errorf("hint %+v", h)
	}
	if h := w.nameHint("Bad"); h.Text != "" {
		t.Errorf("no hint for an invalid name: %+v", h)
	}
	if h := w.nameHint("main"); !strings.Contains(h.Text, "default account") {
		t.Errorf("main hint %+v", h)
	}
}

func TestWizardWithoutATerminalOrCLI(t *testing.T) {
	a, _ := wizApp(t, "claude")
	a.interactive = false
	if err := a.addWizard(addOptions{}); exitCode(err) != exitUsage {
		t.Fatalf("no terminal: %v", err)
	}
	a.interactive = true
	if err := a.addWizard(addOptions{json: true}); exitCode(err) != exitUsage {
		t.Fatalf("--json: %v", err)
	}
	a, s := wizApp(t)
	if err := a.addWizard(addOptions{}); !errors.Is(err, errQuiet) {
		t.Fatalf("no CLI: %v", err)
	}
	if out := s.Out.String(); !strings.Contains(out, "▲  No agent CLI found") || !strings.Contains(out, "Claude Code: ") {
		t.Fatalf("no CLI:\n%s", out)
	}
	a, s = wizApp(t, "claude")
	s.Answers = []any{"work", "Recommended"}
	if err := a.addWizard(addOptions{dryRun: true}); err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if fsx.IsDir(a.dir(".claude-work")) || !strings.Contains(s.Out.String(), "Dry run · nothing was changed") {
		t.Fatalf("dry run:\n%s", s.Out.String())
	}
	if _, err := os.Stat(filepath.Join(config.DirsFor(a.store.Home, config.Slug).Config, "config.toml")); err == nil {
		t.Fatal("a dry run wrote the config")
	}
}
