//go:build !windows

package codex

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeCodex answers `login status` and the app server's methods; hooks/list reports the guard
// hook untrusted until config/batchWrite was called, which it records in $CODEX_HOME/trusted.
const fakeCodex = `#!/bin/sh
case "$*" in
  "login status") [ -f "$CODEX_HOME/in" ] || { echo "Not logged in" >&2; exit 1; }; echo "Logged in using ChatGPT" >&2 ;;
  "mcp list --json") echo '[{"name":"fs","transport":{"type":"stdio","command":"x"}}]' ;;
  "app-server")
    while IFS= read -r line; do
      id=$(printf '%s' "$line" | sed -n 's/.*"id":\([0-9]*\).*/\1/p')
      method=$(printf '%s' "$line" | sed -n 's/.*"method":"\([^"]*\)".*/\1/p')
      [ -n "$id" ] || continue
      case "$method" in
        initialize) echo '{"method":"note","params":{}}'; echo '{"id":0,"result":{}}' ;;
        account/read) echo '{"id":'$id',"result":{"account":{"type":"chatgpt","email":"me@x","planType":"plus"}}}' ;;
        account/rateLimits/read) echo '{"id":'$id',"result":{"rateLimits":{"primary":{"usedPercent":5,"windowDurationMins":300}}}}' ;;
        hooks/list)
          t=untrusted; [ -f "$CODEX_HOME/trusted" ] && t=trusted
          echo '{"id":'$id',"result":{"data":[{"cwd":"/","hooks":[{"key":"k","command":"\"/m/plugin/bin/squadrn-hook\" guard-git --no-ask","sourcePath":"'"$CODEX_HOME"'/hooks.json","enabled":true,"currentHash":"sha256:1","trustStatus":"'$t'"}]}]}}' ;;
        config/batchWrite) printf '%s\n' "$line" >"$CODEX_HOME/trusted"; echo '{"id":'$id',"result":{}}' ;;
        hang) sleep 30 ;;
      esac
    done ;;
esac
`

func fakeCLI(t *testing.T) (CLI, string) {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "codex")
	if err := os.WriteFile(bin, []byte(fakeCodex), 0o755); err != nil {
		t.Fatal(err)
	}
	acct := filepath.Join(dir, ".codex-w")
	if err := os.Mkdir(acct, 0o700); err != nil {
		t.Fatal(err)
	}
	return CLI{Bin: bin, Home: dir}, acct
}

func TestAuthThroughTheCLI(t *testing.T) {
	c, acct := fakeCLI(t)
	ctx := context.Background()
	s, err := c.Auth(ctx, acct)
	if err != nil || s.LoggedIn {
		t.Fatalf("logged out: %+v %v", s, err)
	}
	if v, _ := FileStore(filepath.Join(acct, "config.toml")); v != "file" {
		t.Fatal("running codex for an account must force the file credentials store")
	}
	if err := os.WriteFile(filepath.Join(acct, "in"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	s, err = c.Auth(ctx, acct)
	if err != nil || !s.LoggedIn || s.Email != "me@x" || s.SubscriptionType != "plus" || s.AuthMethod != "ChatGPT" {
		t.Fatalf("logged in: %+v %v", s, err)
	}
	snap, err := c.RateLimits(ctx, acct)
	if err != nil || snap.Five == nil || snap.Five.UsedPct != 5 {
		t.Fatalf("rate limits: %+v %v", snap, err)
	}
	servers, err := c.ListMCP(ctx, acct)
	if err != nil || len(servers) != 1 {
		t.Fatalf("mcp: %v %v", servers, err)
	}
}

func TestInstallPluginTrustsTheHook(t *testing.T) {
	c, acct := fakeCLI(t)
	ctx := context.Background()
	if installed, _, err := c.PluginState(ctx, acct); err != nil || installed {
		t.Fatalf("before: %v %v", installed, err)
	}
	changed, err := c.InstallPlugin(ctx, acct, "/m")
	if err != nil || !changed {
		t.Fatalf("install: %v %v", changed, err)
	}
	b, _ := os.ReadFile(filepath.Join(acct, "trusted"))
	if !strings.Contains(string(b), `"keyPath":"hooks.state"`) || !strings.Contains(string(b), `"k":{"enabled":true,"trusted_hash":"sha256:1"}`) {
		t.Fatalf("trust write: %s", b)
	}
	installed, enabled, err := c.PluginState(ctx, acct)
	if err != nil || !installed || !enabled {
		t.Fatalf("state: %v %v %v", installed, enabled, err)
	}
	if changed, err := c.InstallPlugin(ctx, acct, "/m"); err != nil || changed {
		t.Fatalf("again: %v %v", changed, err)
	}
	if err := c.UpdatePlugin(ctx, acct); err != nil {
		t.Fatal(err)
	}
	if err := c.RemovePlugin(ctx, acct); err != nil {
		t.Fatal(err)
	}
	if installed, _, _ := c.PluginState(ctx, acct); installed {
		t.Fatal("removed")
	}
	if err := c.UpdatePlugin(ctx, acct); err != nil {
		t.Fatal("update without the hook:", err)
	}
}

func TestAppServerDeadline(t *testing.T) {
	c, acct := fakeCLI(t)
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := c.appServer(ctx, acct, request{Method: "hang"}); err == nil {
		t.Fatal("no reply must be an error")
	}
	if time.Since(start) > 5*time.Second {
		t.Fatal("the deadline did not stop the server")
	}
}
