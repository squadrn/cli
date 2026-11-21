//go:build !windows

package completion

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func testEnv(t *testing.T) Env {
	t.Helper()
	home := t.TempDir()
	return Env{
		Home: home, DataHome: filepath.Join(home, ".local", "share"), ConfigHome: filepath.Join(home, ".config"),
		Fpath: func() ([]string, error) { return nil, nil },
	}
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestZshPlanUsesADirOnFpath(t *testing.T) {
	e := testEnv(t)
	zfunc := filepath.Join(e.Home, ".zfunc")
	if err := os.MkdirAll(zfunc, 0o755); err != nil {
		t.Fatal(err)
	}
	e.Fpath = func() ([]string, error) { return []string{"/x", zfunc}, nil }
	p, _ := e.Plan("zsh", []string{"srn"})
	if p.Dir != zfunc || p.RC != "" || !p.Active() {
		t.Fatalf("plan = %+v", p)
	}
	// Dirs outside the home directory only when asked for.
	e.Fpath = func() ([]string, error) { return []string{"/usr/local/share/zsh/site-functions"}, nil }
	if p, _ := e.Plan("zsh", nil); p.Dir != e.zshDefault() {
		t.Fatalf("plan without System = %+v", p)
	}
	// A dir that does not exist (or cannot be written) is not used.
	e.Fpath = func() ([]string, error) { return []string{filepath.Join(e.Home, ".zsh", "functions")}, nil }
	if p, _ := e.Plan("zsh", nil); p.Dir != e.zshDefault() || p.Active() {
		t.Fatalf("plan for a missing dir = %+v", p)
	}
}

func TestZshPlanAddsABlockAboveCompinit(t *testing.T) {
	e := testEnv(t)
	rc := filepath.Join(e.Home, ".zshrc")
	write(t, rc, "export A=1\nautoload -Uz compinit\ncompinit\n")
	p, _ := e.Plan("zsh", nil)
	if p.Dir != e.zshDefault() || p.RC != rc || p.Block != "fpath=($HOME/.zsh/completions $fpath)" || p.Active() {
		t.Fatalf("plan = %+v", p)
	}
	got := WithBlock(read(rc), p.Block)
	want := "export A=1\n" + Begin + "\nfpath=($HOME/.zsh/completions $fpath)\n" + End + "\nautoload -Uz compinit\ncompinit\n"
	if got != want {
		t.Fatalf("WithBlock:\n%s\nwant:\n%s", got, want)
	}
	write(t, rc, got)
	if p, _ := e.Plan("zsh", nil); !p.Done || !p.Active() {
		t.Fatalf("plan after the block = %+v", p)
	}
	// Idempotent, and removable byte for byte.
	if again := WithBlock(got, p.Block); again != got {
		t.Fatalf("second WithBlock:\n%s", again)
	}
	if back, ok := WithoutBlock(got); !ok || back != "export A=1\nautoload -Uz compinit\ncompinit\n" {
		t.Fatalf("WithoutBlock = %q, %v", back, ok)
	}
}

func TestZshPlanWithoutCompinitRunsIt(t *testing.T) {
	e := testEnv(t)
	p, _ := e.Plan("zsh", nil)
	if !strings.HasSuffix(p.Block, "\nautoload -Uz compinit && compinit") {
		t.Fatalf("block = %q", p.Block)
	}
	got := WithBlock("alias x=y", p.Block)
	if !strings.HasPrefix(got, "alias x=y\n"+Begin+"\n") || !strings.HasSuffix(got, End+"\n") {
		t.Fatalf("WithBlock = %q", got)
	}
	// oh-my-zsh runs compinit: the block goes above it.
	got = WithBlock("plugins=(git)\nsource $ZSH/oh-my-zsh.sh\n", "fpath=(x $fpath)")
	if !strings.HasPrefix(got, "plugins=(git)\n"+Begin) {
		t.Fatalf("oh-my-zsh: %q", got)
	}
}

func TestZshPlanIgnoresDirsAddedAfterCompinit(t *testing.T) {
	e := testEnv(t)
	dir := e.zshDefault()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	e.Fpath = func() ([]string, error) { return []string{dir}, nil }
	rc := filepath.Join(e.Home, ".zshrc")
	write(t, rc, "autoload -Uz compinit && compinit\nfpath=(~/.zsh/completions $fpath)\n")
	if p, _ := e.Plan("zsh", nil); p.Active() || p.RC != rc {
		t.Fatalf("plan = %+v", p)
	}
	write(t, rc, "fpath=(~/.zsh/completions $fpath)\nautoload -Uz compinit && compinit\n")
	if p, _ := e.Plan("zsh", nil); !p.Active() || p.Dir != dir {
		t.Fatalf("plan = %+v", p)
	}
}

func TestZshPlanReadsStartupFilesWithoutZsh(t *testing.T) {
	e := testEnv(t)
	e.Fpath = func() ([]string, error) { return nil, errors.New("no zsh") }
	e.ZDotDir = filepath.Join(e.Home, "zdot")
	dir := e.zshDefault()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(e.ZDotDir, ".zshrc"), "fpath+=($HOME/.zsh/completions)\ncompinit\n")
	if p, _ := e.Plan("zsh", nil); !p.Active() || p.Dir != dir {
		t.Fatalf("plan = %+v", p)
	}
}

func TestStaticFpath(t *testing.T) {
	got := StaticFpath(`fpath=(~/.zfunc "$HOME/a" ${HOME}/b /opt/c $fpath)
# fpath=(/commented)
fpath+=/d
typeset -U fpath=($ZSH/x /e)
  fpath+=( /f )`, "/h")
	want := []string{"/h/.zfunc", "/h/a", "/h/b", "/opt/c", "/d", "/e", "/f"}
	for i := range want {
		want[i] = filepath.FromSlash(want[i])
	}
	if !slices.Equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestBashPlan(t *testing.T) {
	old := bashLoaders
	defer func() { bashLoaders = old }()
	e := testEnv(t)
	bashLoaders = []string{filepath.Join(e.Home, "missing")}
	p, _ := e.Plan("bash", []string{"srn", "squadrn"})
	if p.Dir != filepath.Join(e.DataHome, "bash-completion", "completions") || p.RC != filepath.Join(e.Home, ".bashrc") || p.Active() {
		t.Fatalf("plan = %+v", p)
	}
	if want := `[ -f "$HOME/.local/share/bash-completion/completions/srn" ] && . "$HOME/.local/share/bash-completion/completions/srn"`; !strings.HasPrefix(p.Block, want+"\n") {
		t.Fatalf("block = %q", p.Block)
	}
	loader := filepath.Join(e.Home, "bash_completion")
	write(t, loader, "")
	bashLoaders = []string{loader}
	if p, _ := e.Plan("bash", nil); !p.Active() {
		t.Fatalf("with bash-completion: %+v", p)
	}
}

func TestOtherShells(t *testing.T) {
	e := testEnv(t)
	if p, ok := e.Plan("fish", nil); !ok || !p.Active() || p.Dir != filepath.Join(e.ConfigHome, "fish", "completions") {
		t.Fatalf("fish: %+v", p)
	}
	if _, ok := e.Plan("tcsh", nil); ok {
		t.Fatal("tcsh planned")
	}
	if len(e.Dirs("tcsh")) != 0 || len(e.Dirs("zsh")) < 2 || len(e.RCFiles()) != 2 {
		t.Fatal("Dirs/RCFiles")
	}
	for shell, want := range map[string]string{"zsh": "/d/_srn", "bash": "/d/srn", "fish": "/d/srn.fish"} {
		if got := File(shell, "/d", "srn"); got != filepath.FromSlash(want) {
			t.Errorf("File(%s) = %s", shell, got)
		}
	}
	if _, ok := WithoutBlock("no block"); ok {
		t.Fatal("removed a missing block")
	}
	if _, ok := WithoutBlock(Begin + "\nunterminated"); ok {
		t.Fatal("removed an unterminated block")
	}
}
