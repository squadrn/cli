package claude

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type fakeRunner struct {
	calls atomic.Int32
	delay time.Duration
	reply func(configDir string, args []string) ([]byte, error)
}

func (f *fakeRunner) Output(ctx context.Context, configDir string, args ...string) ([]byte, error) {
	f.calls.Add(1)
	select {
	case <-time.After(f.delay):
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return f.reply(configDir, args)
}

func TestEnvSetsAndUnsetsConfigDir(t *testing.T) {
	base := []string{"PATH=/bin", "CLAUDE_CONFIG_DIR=/old"}
	if got := Env(base, ""); !slices.Equal(got, []string{"PATH=/bin"}) {
		t.Errorf("main account env = %v", got)
	}
	if got := Env(base, "/h/.claude-p"); !slices.Equal(got, []string{"PATH=/bin", "CLAUDE_CONFIG_DIR=/h/.claude-p"}) {
		t.Errorf("named account env = %v", got)
	}
}

func TestAuthTrustsStdoutOnNonZeroExit(t *testing.T) {
	r := &fakeRunner{reply: func(string, []string) ([]byte, error) {
		return []byte(`{"loggedIn":false}`), errors.New("exit status 1")
	}}
	s, err := Auth(context.Background(), r, "/x")
	if err != nil || s.LoggedIn {
		t.Fatalf("got %+v, %v", s, err)
	}
}

func TestAuthFailsWithoutOutput(t *testing.T) {
	r := &fakeRunner{reply: func(string, []string) ([]byte, error) { return nil, errors.New("boom") }}
	if _, err := Auth(context.Background(), r, "/x"); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("err = %v", err)
	}
}

func TestCLIEnvOverridesInheritedKeys(t *testing.T) {
	t.Setenv("GIT_CONFIG_COUNT", "1")
	env := CLI{ExtraEnv: []string{"GIT_CONFIG_COUNT=2"}}.env("/c")
	n := 0
	for _, kv := range env {
		if strings.HasPrefix(kv, "GIT_CONFIG_COUNT=") {
			n++
			if kv != "GIT_CONFIG_COUNT=2" {
				t.Errorf("kept %s", kv)
			}
		}
	}
	if n != 1 || !slices.Contains(env, "CLAUDE_CONFIG_DIR=/c") {
		t.Fatalf("env = %v", env)
	}
	c := CLI{}
	if !slices.Equal(c.WorktreeArgs("x"), []string{"--worktree", "x"}) || !slices.Equal(c.ResumeArgs("s"), []string{"--resume", "s"}) ||
		!slices.Equal(c.LoginArgs(), []string{"auth", "login"}) || !slices.Equal(c.LogoutArgs(), []string{"auth", "logout"}) {
		t.Fatal("session args")
	}
}

func TestRelocate(t *testing.T) {
	home := t.TempDir()
	oldDir, newDir := filepath.Join(home, ".claude-a"), filepath.Join(home, ".claude-b")
	if err := os.MkdirAll(filepath.Join(newDir, "plugins"), 0o700); err != nil {
		t.Fatal(err)
	}
	j := func(v string) string { b, _ := json.Marshal(v); return string(b) }
	doc := func(dir string) string {
		return `{"p":{"installPath":` + j(dir+string(filepath.Separator)+filepath.Join("plugins", "cache", "x")) +
			`,"slash":` + j(dir+"/plugins/y") + `,"other":` + j(oldDir+"-suffix/y") +
			`,"ext":` + j("/elsewhere/"+oldDir+"/z") + `}}`
	}
	f := filepath.Join(newDir, "plugins", "installed_plugins.json")
	if err := os.WriteFile(f, []byte(doc(oldDir)), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := (CLI{}).Relocate(oldDir, newDir); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(f)
	if want := doc(newDir); string(b) != want {
		t.Fatalf("relocated:\n%s\nwant\n%s", b, want)
	}
}
