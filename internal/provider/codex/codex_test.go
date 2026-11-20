package codex

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/squadrn/squadrn/internal/provider"
)

func TestParseLoginStatus(t *testing.T) {
	for _, tc := range []struct {
		out    string
		ok     bool
		want   provider.AuthStatus
		hasErr bool
	}{
		{"Logged in using ChatGPT\n", true, provider.AuthStatus{LoggedIn: true, AuthMethod: "ChatGPT"}, false},
		{"Logged in using an API key - sk-proj-***abcd\n", true, provider.AuthStatus{LoggedIn: true, AuthMethod: "API key"}, false},
		{"Logged in using access token\n", true, provider.AuthStatus{LoggedIn: true, AuthMethod: "access token"}, false},
		{"codex login status: codex: exit status 1: Not logged in", false, provider.AuthStatus{}, false},
		{"Not logged in\n", false, provider.AuthStatus{}, false},
		{"something new\n", true, provider.AuthStatus{LoggedIn: true}, false},
		{"something new\n", false, provider.AuthStatus{}, false},
		{"", false, provider.AuthStatus{}, true},
	} {
		got, err := parseLoginStatus(tc.out, tc.ok)
		if got != tc.want || (err != nil) != tc.hasErr {
			t.Errorf("parseLoginStatus(%q, %v) = %+v, %v", tc.out, tc.ok, got, err)
		}
	}
	if s, _ := parseLoginStatus("Logged in using an API key - sk-x\n", true); !s.Metered() {
		t.Error("an API key login is metered")
	}
}

func TestWithAccount(t *testing.T) {
	base := provider.AuthStatus{LoggedIn: true}
	got := withAccount(base, json.RawMessage(`{"account":{"type":"chatgpt","email":"me@x","planType":"pro","workspaceName":"Acme"},"requiresOpenaiAuth":true}`))
	want := provider.AuthStatus{LoggedIn: true, AuthMethod: "ChatGPT", Email: "me@x", OrgName: "Acme", SubscriptionType: "pro"}
	if got != want {
		t.Fatalf("got %+v", got)
	}
	if got := withAccount(base, json.RawMessage(`{"account":{"type":"apiKey"}}`)); got.AuthMethod != "API key" || !got.Metered() {
		t.Fatalf("api key: %+v", got)
	}
	if got := withAccount(base, json.RawMessage(`{"account":{"type":"chatgpt","email":null,"planType":"unknown"}}`)); got.SubscriptionType != "" {
		t.Fatalf("unknown plan: %+v", got)
	}
	for _, raw := range []string{`{"account":null}`, `nope`} {
		if got := withAccount(base, json.RawMessage(raw)); got != base {
			t.Fatalf("%s: %+v", raw, got)
		}
	}
}

func TestEnsureFileStore(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := EnsureFileStore(path); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	if string(b) != storeLine+"\n" {
		t.Fatalf("new file: %q", b)
	}
	if fi, _ := os.Stat(path); runtime.GOOS != "windows" && fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", fi.Mode().Perm())
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ in, want string }{
		// Replaced in place, comments and tables kept.
		{
			"# mine\nmodel = \"o3\"\ncli_auth_credentials_store = \"keyring\" # was\n\n[mcp_servers.x]\ncommand = \"y\"\n",
			"# mine\nmodel = \"o3\"\n" + storeLine + "\n\n[mcp_servers.x]\ncommand = \"y\"\n",
		},
		// Added at the top, never inside a table.
		{"[profiles.a]\ncli_auth_credentials_store = \"keyring\"\n", storeLine + "\n[profiles.a]\ncli_auth_credentials_store = \"keyring\"\n"},
		// Already right: untouched.
		{"cli_auth_credentials_store = 'file'\n", "cli_auth_credentials_store = 'file'\n"},
	} {
		if err := os.WriteFile(path, []byte(tc.in), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := EnsureFileStore(path); err != nil {
			t.Fatal(err)
		}
		if b, _ := os.ReadFile(path); string(b) != tc.want {
			t.Errorf("in %q\ngot  %q\nwant %q", tc.in, b, tc.want)
		}
		if v, err := FileStore(path); err != nil || v != "file" {
			t.Errorf("FileStore = %q, %v", v, err)
		}
	}
	if fi, _ := os.Stat(path); runtime.GOOS != "windows" && fi.Mode().Perm() != 0o644 {
		t.Fatalf("mode not kept: %v", fi.Mode().Perm())
	}
	if err := os.WriteFile(path, []byte("not = [toml\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := EnsureFileStore(path); err == nil || !strings.Contains(err.Error(), "not valid TOML") {
		t.Fatalf("broken file: %v", err)
	}
	if b, _ := os.ReadFile(path); string(b) != "not = [toml\n" {
		t.Fatal("a broken file was changed")
	}
	if err := (CLI{}).CheckCredentials(dir); err == nil {
		t.Fatal("broken file passes the check")
	}
	if err := os.WriteFile(path, []byte("model = \"o3\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := (CLI{}).CheckCredentials(dir); err == nil || !strings.Contains(err.Error(), "is unset") {
		t.Fatalf("unset: %v", err)
	}
}

func TestSetGuardHook(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hooks.json")
	cmd := hookCommand("/opt/squadrn")
	if cmd != `"/opt/squadrn/plugin/bin/squadrn-hook" guard-git --no-ask` {
		t.Fatalf("command %s", cmd)
	}
	user := `{"description":"mine","hooks":{"Stop":[{"hooks":[{"type":"command","command":"notify"}]}],
"PreToolUse":[{"matcher":"apply_patch","hooks":[{"type":"command","command":"lint"}]},
{"matcher":"Bash","hooks":[{"type":"command","command":"\"/old/plugin/bin/squadrn-hook\" guard-git --no-ask"}]}]}}`
	if err := os.WriteFile(path, []byte(user), 0o600); err != nil {
		t.Fatal(err)
	}
	changed, err := setGuardHook(path, cmd)
	if err != nil || !changed {
		t.Fatalf("install: %v %v", changed, err)
	}
	if got, _ := installedCommand(path); got != cmd {
		t.Fatalf("installed %q", got)
	}
	b, _ := os.ReadFile(path)
	for _, want := range []string{`"description": "mine"`, `"command": "notify"`, `"command": "lint"`, `"matcher": "Bash"`, `"timeout": 10`} {
		if !strings.Contains(string(b), want) {
			t.Errorf("lost %s:\n%s", want, b)
		}
	}
	if strings.Contains(string(b), "/old/") || strings.Count(string(b), "squadrn-hook") != 1 {
		t.Fatalf("old hook kept:\n%s", b)
	}
	if changed, err := setGuardHook(path, cmd); err != nil || changed {
		t.Fatalf("second install: %v %v", changed, err)
	}
	if changed, err := setGuardHook(path, ""); err != nil || !changed {
		t.Fatalf("remove: %v %v", changed, err)
	}
	if got, _ := installedCommand(path); got != "" {
		t.Fatalf("still installed: %q", got)
	}
	if b, _ := os.ReadFile(path); !strings.Contains(string(b), `"command": "lint"`) {
		t.Fatalf("remove dropped a user hook:\n%s", b)
	}
	if err := os.WriteFile(path, []byte(`{"hooks":[1]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := setGuardHook(path, cmd); err == nil {
		t.Fatal("a malformed hooks.json is overwritten")
	}
}

func TestGuardHook(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hooks.json")
	if err := os.WriteFile(path, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]any{"data": []any{map[string]any{"cwd": dir, "hooks": []any{
		map[string]any{"key": "other", "command": "lint", "sourcePath": path, "enabled": true, "trustStatus": "trusted"},
		map[string]any{"key": path + ":pre_tool_use:0:0", "command": hookCommand("/x"), "sourcePath": path, "enabled": true, "currentHash": "sha256:1", "trustStatus": "modified"},
	}}}})
	h, ok := guardHook(raw, path)
	if !ok || h.CurrentHash != "sha256:1" || trusted(h) {
		t.Fatalf("got %+v %v", h, ok)
	}
	if _, ok := guardHook(raw, filepath.Join(dir, "elsewhere.json")); ok {
		t.Fatal("hook of another file")
	}
	for _, s := range []string{"trusted", "managed"} {
		if !trusted(listedHook{Enabled: true, TrustStatus: s}) {
			t.Error(s)
		}
	}
	if trusted(listedHook{Enabled: false, TrustStatus: "trusted"}) {
		t.Error("a disabled hook does not guard")
	}
}

func TestMCPAddArgs(t *testing.T) {
	for _, tc := range []struct {
		cfg  string
		want []string
	}{
		{`{"command":"npx","args":["-y","x"],"env":{"B":"2","A":"1"}}`, []string{"mcp", "add", "s", "--env", "A=1", "--env", "B=2", "--", "npx", "-y", "x"}},
		{`{"type":"http","url":"https://h/mcp"}`, []string{"mcp", "add", "s", "--url", "https://h/mcp"}},
		{`{"type":"sse","url":"https://h/sse"}`, nil},
		{`{"type":"http","url":"https://h","headers":{"Authorization":"x"}}`, nil},
		{`{}`, nil},
		{`nope`, nil},
	} {
		got, err := mcpAddArgs("s", []byte(tc.cfg))
		if !slices.Equal(got, tc.want) || (tc.want == nil) != (err != nil) {
			t.Errorf("%s: %q, %v", tc.cfg, got, err)
		}
	}
}

func TestParseMCPList(t *testing.T) {
	out := []byte(`[{"name":"fs","transport":{"type":"stdio","command":"npx","args":["-y"],"env":{"FOO":"bar"}}},
{"name":"web","transport":{"type":"streamable_http","url":"https://e/mcp","http_headers":null}},
{"name":"odd","transport":{"type":"future"}}]`)
	got, err := parseMCPList(out)
	if err != nil {
		t.Fatal(err)
	}
	if string(got["fs"]) != `{"command":"npx","args":["-y"],"env":{"FOO":"bar"}}` || string(got["web"]) != `{"type":"http","url":"https://e/mcp"}` || len(got) != 2 {
		t.Fatalf("got %s", got)
	}
	if _, err := parseMCPList([]byte("{")); err == nil {
		t.Fatal("bad JSON")
	}
	if _, err := parseMCPList([]byte(`[{"transport":{"type":"stdio","command":"x"}}]`)); err == nil {
		t.Fatal("nameless server")
	}
}

func TestParseRateLimits(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	got, err := parseRateLimits(json.RawMessage(`{"rateLimits":{"primary":{"usedPercent":1}},
"rateLimitsByLimitId":{"codex":{"primary":{"usedPercent":42,"windowDurationMins":300,"resetsAt":1800003600},
"secondary":{"usedPercent":7,"windowDurationMins":10080,"resetsAt":1800500000}}}}`), now)
	if err != nil || got.Five == nil || got.Seven == nil || got.Five.UsedPct != 42 || got.Seven.UsedPct != 7 ||
		!got.Five.ResetsAt.Equal(time.Unix(1800003600, 0)) || !got.ObservedAt.Equal(now) {
		t.Fatalf("got %+v %v", got, err)
	}
	got, err = parseRateLimits(json.RawMessage(`{"rateLimits":{"primary":{"usedPercent":10080,"windowDurationMins":10080},"secondary":null}}`), now)
	if err != nil || got.Five != nil || got.Seven == nil {
		t.Fatalf("weekly only: %+v %v", got, err)
	}
	got, err = parseRateLimits(json.RawMessage(`{"rateLimits":{"primary":{"usedPercent":3},"secondary":{"usedPercent":4}}}`), now)
	if err != nil || got.Five.UsedPct != 3 || got.Seven.UsedPct != 4 {
		t.Fatalf("no durations: %+v %v", got, err)
	}
	if _, err := parseRateLimits(json.RawMessage(`{"rateLimits":{}}`), now); !errors.Is(err, ErrNoUsage) {
		t.Fatalf("empty: %v", err)
	}
	if _, err := parseRateLimits(json.RawMessage(`[`), now); err == nil {
		t.Fatal("bad JSON")
	}
}

func TestSessionReplies(t *testing.T) {
	s := &session{replies: make([]reply, 2), pending: 3, initialized: make(chan struct{}), done: make(chan struct{})}
	for _, chunk := range []string{
		`{"id":0,"result":{}}` + "\n" + `{"method":"remoteControl/status/changed","params":{}}` + "\n{\"id\":2,",
		`"error":{"code":-32600,"message":"no"}}` + "\n" + `{"id":0,"result":{}}` + "\n" + `{"id":9,"result":1}` + "\n",
		`{"id":1,"method":"item/tool/requestUserInput","params":{}}` + "\n" + `{"id":1}` + "\n",
	} {
		if _, err := s.Write([]byte(chunk)); err != nil {
			t.Fatal(err)
		}
	}
	select {
	case <-s.done:
	default:
		t.Fatal("not done")
	}
	if string(s.replies[0].Result) != "null" || s.replies[0].Err != nil || s.replies[1].Err == nil || s.replies[1].Err.Error() != "no (-32600)" {
		t.Fatalf("replies %+v", s.replies)
	}
}

func TestEnv(t *testing.T) {
	t.Setenv("CODEX_HOME", "/elsewhere")
	t.Setenv("GIT_CONFIG_COUNT", "9")
	c := CLI{Home: "/h", ExtraEnv: []string{"GIT_CONFIG_COUNT=1"}}
	env := c.env("")
	if slices.ContainsFunc(env, func(kv string) bool { return strings.HasPrefix(kv, "CODEX_HOME=") }) {
		t.Fatal("main account must run with CODEX_HOME unset")
	}
	if !slices.Contains(env, "GIT_CONFIG_COUNT=1") || slices.Contains(env, "GIT_CONFIG_COUNT=9") {
		t.Fatal("ExtraEnv must replace inherited entries")
	}
	if !slices.Contains(c.env("/h/.codex-w"), "CODEX_HOME=/h/.codex-w") {
		t.Fatal("account dir")
	}
	if c.dir("") != filepath.Join("/h", ".codex") || c.dir("/a") != "/a" {
		t.Fatal("dir")
	}
}

func TestRelocate(t *testing.T) {
	home := t.TempDir()
	oldDir, newDir := filepath.Join(home, ".codex-a"), filepath.Join(home, ".codex-b")
	if err := os.MkdirAll(newDir, 0o700); err != nil {
		t.Fatal(err)
	}
	cfg := "x = 1\n[hooks.state." + strconv.Quote(filepath.Join(oldDir, "hooks.json")+":pre_tool_use:0:0") + "]\ntrusted_hash = \"h\"\n"
	if err := os.WriteFile(filepath.Join(newDir, "config.toml"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := (CLI{}).Relocate(oldDir, newDir); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(newDir, "config.toml"))
	if !strings.Contains(string(b), strings.Trim(strconv.Quote(filepath.Join(newDir, "hooks.json")+":pre_tool_use"), `"`)) || strings.Contains(string(b), ".codex-a") {
		t.Fatalf("got %s", b)
	}
	if err := (CLI{}).Relocate(oldDir, filepath.Join(home, "none")); err != nil {
		t.Fatal("missing config.toml:", err)
	}
}

func TestArgs(t *testing.T) {
	c := CLI{}
	if !slices.Equal(c.LoginArgs(), []string{"login"}) || !slices.Equal(c.LogoutArgs(), []string{"logout"}) || c.EnablePluginArgs() != nil {
		t.Fatal("args")
	}
}

func TestSettingsRoundTrip(t *testing.T) {
	var f Settings
	doc, err := f.Decode([]byte("model = \"o3\"\n[hooks.state.\"/h/.codex-w/hooks.json:pre_tool_use:0:0\"]\ntrusted_hash = \"sha256:1\"\n[[skills.config]]\npath = \"/p\"\nenabled = false\n"))
	if err != nil {
		t.Fatal(err)
	}
	b, err := f.Encode(doc)
	if err != nil {
		t.Fatal(err)
	}
	again, err := f.Decode(b)
	if err != nil {
		t.Fatalf("%v\n%s", err, b)
	}
	if hj, _ := json.Marshal(doc); string(hj) != func() string { a, _ := json.Marshal(again); return string(a) }() {
		t.Fatalf("round trip changed the document:\n%s", b)
	}
	if empty, err := f.Decode([]byte(" \n")); err != nil || len(empty) != 0 {
		t.Fatal("empty file")
	}
	if _, err := f.Decode([]byte("x = [")); err == nil {
		t.Fatal("bad TOML")
	}
	if !slices.Contains(f.Private(), "mcp_servers") || !slices.Contains(f.Private(), storeKey) || !slices.Contains(f.Deep(), "sandbox_workspace_write") {
		t.Fatal("private or deep keys")
	}
}
