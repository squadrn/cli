package gemini

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/squadrn/squadrn-cli/internal/provider"
)

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestStripComments(t *testing.T) {
	for _, tt := range []struct {
		in, want string
		found    bool
	}{
		{`{"a":1}`, `{"a":1}`, false},
		{"// head\n{\"a\": 1} // tail", "       \n{\"a\": 1}        ", true},
		{`{"u":"http://x/*y*/"}`, `{"u":"http://x/*y*/"}`, false},
		{`{"q":"a\"//b"}`, `{"q":"a\"//b"}`, false},
		{"{/* a\nb */\"a\":1}", "{    \n    \"a\":1}", true},
		{"{\"a\":1} /* open", "{\"a\":1}        ", true},
		{"{\"a\":1}\r\n// x\r\n", "{\"a\":1}\r\n    \r\n", true},
	} {
		got, found := stripComments([]byte(tt.in))
		if string(got) != tt.want || found != tt.found {
			t.Errorf("stripComments(%q) = %q, %v; want %q, %v", tt.in, got, found, tt.want, tt.found)
		}
	}
}

func TestSettingsDecodeEncode(t *testing.T) {
	s := Settings{}
	doc, err := s.Decode([]byte(`// my settings
{
  "general": {"vimMode": true}, /* inline */
  "security": {"auth": {"selectedType": "oauth-personal"}},
  "url": "https://example.com/a//b",
  "n": 1.50
}`))
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.Encode(doc)
	if err != nil {
		t.Fatal(err)
	}
	if s.HasComments(b) || !strings.Contains(string(b), `"n": 1.50`) || !strings.Contains(string(b), `"https://example.com/a//b"`) {
		t.Fatalf("encoded = %s", b)
	}
	if !s.HasComments([]byte("{} // x")) || s.HasComments([]byte(`{"a":"//"}`)) {
		t.Fatal("HasComments")
	}
	if doc, err := s.Decode([]byte("  // only a comment\n")); err != nil || len(doc) != 0 {
		t.Fatalf("empty = %v, %v", doc, err)
	}
	// Gemini reads settings without trailing commas; so does squadrn.
	if _, err := s.Decode([]byte(`{"a": 1,}`)); err == nil {
		t.Fatal("a trailing comma was accepted")
	}
	if !slices.Contains(s.Deep(), "hooks") || !slices.Contains(s.Deep(), "security") || !slices.Equal(s.Private(), []string{"mcpServers"}) {
		t.Fatal("Deep or Private")
	}
	doc = map[string]any{"hooks": map[string]any{"BeforeTool": []any{map[string]any{"hooks": []any{map[string]any{"command": `"/d/plugin/bin/squadrn-hook" guard-git`}}}}}}
	s.FromMain(doc)
	if len(doc) != 0 {
		t.Fatalf("FromMain left %v", doc)
	}
}

func TestReadAuth(t *testing.T) {
	env := func(kv ...string) func(string) string {
		return func(k string) string {
			for i := 0; i+1 < len(kv); i += 2 {
				if kv[i] == k {
					return kv[i+1]
				}
			}
			return ""
		}
	}
	oauth := `{"security":{"auth":{"selectedType":"oauth-personal"}}}`
	for _, tt := range []struct {
		name     string
		files    map[string]string
		env      func(string) string
		want     provider.AuthStatus
		metered  bool
		parseErr bool
	}{
		{name: "empty", env: env()},
		{name: "oauth without token file", files: map[string]string{"settings.json": oauth}, env: env()},
		{name: "oauth without refresh token", files: map[string]string{"settings.json": oauth, "oauth_creds.json": `{"access_token":"a"}`}, env: env()},
		{
			name:  "oauth",
			files: map[string]string{"settings.json": "// c\n" + oauth, "oauth_creds.json": `{"access_token":"a","refresh_token":"r"}`, "google_accounts.json": `{"active":"me@example.com","old":[]}`},
			env:   env(), want: provider.AuthStatus{LoggedIn: true, AuthMethod: "Google account", Email: "me@example.com"},
		},
		{name: "token but no auth type", files: map[string]string{"oauth_creds.json": `{"refresh_token":"r"}`}, env: env()},
		{name: "api key unset", files: map[string]string{"settings.json": `{"security":{"auth":{"selectedType":"gemini-api-key"}}}`}, env: env()},
		{
			name: "api key", files: map[string]string{"settings.json": `{"security":{"auth":{"selectedType":"gemini-api-key"}}}`},
			env: env("GEMINI_API_KEY", "k"), want: provider.AuthStatus{LoggedIn: true, AuthMethod: "Gemini API key"}, metered: true,
		},
		{
			name: "vertex key", files: map[string]string{"settings.json": `{"security":{"auth":{"selectedType":"vertex-ai"}}}`},
			env: env("GOOGLE_API_KEY", "k"), want: provider.AuthStatus{LoggedIn: true, AuthMethod: "Vertex AI API key"}, metered: true,
		},
		{
			name: "vertex project", files: map[string]string{"settings.json": `{"security":{"auth":{"selectedType":"vertex-ai"}}}`},
			env: env("GOOGLE_CLOUD_PROJECT", "p", "GOOGLE_CLOUD_LOCATION", "l"), want: provider.AuthStatus{LoggedIn: true, AuthMethod: "Vertex AI API (project p)"}, metered: true,
		},
		{name: "vertex nothing", files: map[string]string{"settings.json": `{"security":{"auth":{"selectedType":"vertex-ai"}}}`}, env: env()},
		{
			name: "cloud shell", files: map[string]string{"settings.json": `{"security":{"auth":{"selectedType":"cloud-shell"}}}`},
			env: env(), want: provider.AuthStatus{LoggedIn: true, AuthMethod: "cloud-shell"},
		},
		{name: "bad settings", files: map[string]string{"settings.json": `{`}, env: env(), parseErr: true},
		{name: "bad token file", files: map[string]string{"settings.json": oauth, "oauth_creds.json": `{"refresh_token":"SECRET`}, env: env(), parseErr: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			state := t.TempDir()
			for name, content := range tt.files {
				write(t, filepath.Join(state, name), content)
			}
			got, err := readAuth(state, tt.env)
			if tt.parseErr {
				if err == nil || strings.Contains(err.Error(), "SECRET") {
					t.Fatalf("err = %v", err)
				}
				return
			}
			if err != nil || got != tt.want || got.Metered() != tt.metered {
				t.Fatalf("readAuth = %+v (metered %v), %v; want %+v", got, got.Metered(), err, tt.want)
			}
		})
	}
}

func TestCLIPaths(t *testing.T) {
	c := CLI{Home: "/h"}
	if got := filepath.ToSlash(c.state("")); got != "/h/.gemini" {
		t.Errorf("main state = %s", got)
	}
	if got := filepath.ToSlash(c.state("/h/.gemini-work")); got != "/h/.gemini-work/.gemini" {
		t.Errorf("state = %s", got)
	}
	files := c.LogoutFiles("/h/.gemini-work")
	if len(files) != 3 || filepath.ToSlash(files[0]) != "/h/.gemini-work/.gemini/oauth_creds.json" {
		t.Errorf("LogoutFiles = %v", files)
	}
	if c.LogoutArgs() != nil || !slices.Equal(c.LoginArgs(), []string{"--skip-trust"}) || !strings.Contains(c.LoginHint(), "/quit") {
		t.Error("login and logout")
	}
	w := CLI{Bin: "node", Prefix: []string{"/p/bundle/gemini.js"}}
	if got := w.argv([]string{"-p", "hi"}); !slices.Equal(got, []string{"gemini", "/p/bundle/gemini.js", "-p", "hi"}) {
		t.Errorf("argv = %v", got)
	}
}

func TestEnv(t *testing.T) {
	t.Setenv("GEMINI_CLI_HOME", "/elsewhere")
	t.Setenv(encryptedStorageEnv, "true")
	t.Setenv("NO_BROWSER", "true")
	t.Setenv("GIT_CONFIG_COUNT", "9")
	c := CLI{ExtraEnv: []string{"GIT_CONFIG_COUNT=1"}}
	keys := func(env []string, k string) int {
		n := 0
		for _, kv := range env {
			if strings.HasPrefix(kv, k+"=") {
				n++
			}
		}
		return n
	}
	main := c.env("")
	if keys(main, "GEMINI_CLI_HOME") != 0 || keys(main, encryptedStorageEnv) != 0 || !slices.Contains(main, "NO_BROWSER=true") || keys(main, "GIT_CONFIG_COUNT") != 1 || !slices.Contains(main, "GIT_CONFIG_COUNT=1") {
		t.Errorf("main env = %v", main)
	}
	work := c.env("/h/.gemini-work")
	if !slices.Contains(work, "GEMINI_CLI_HOME=/h/.gemini-work") || keys(work, "GEMINI_CLI_HOME") != 1 || keys(work, encryptedStorageEnv) != 0 {
		t.Errorf("work env = %v", work)
	}
}

func TestBundleFor(t *testing.T) {
	// Windows: %APPDATA%\npm\gemini.cmd next to node_modules.
	win := t.TempDir()
	js := filepath.Join(win, "node_modules", "@google", "gemini-cli", "bundle", "gemini.js")
	write(t, js, "")
	write(t, filepath.Join(win, "gemini.cmd"), "@ECHO off\r\nendLocal & goto #_undefined_# 2>NUL || title %COMSPEC% & \"%_prog%\"  \"%dp0%\\node_modules\\@google\\gemini-cli\\bundle\\gemini.js\" %*\r\n")
	if got := bundleFor(filepath.Join(win, "gemini.cmd")); got != js {
		t.Errorf("from the shim: %q, want %q", got, js)
	}
	// A shim naming nothing squadrn can find: the prefix layout.
	write(t, filepath.Join(win, "other.cmd"), "@ECHO off\r\n\"%dp0%\\node_modules\\gone\\x.js\" %*\r\n")
	if got := bundleFor(filepath.Join(win, "other.cmd")); got != js {
		t.Errorf("from the layout: %q, want %q", got, js)
	}
	// Unix-style prefix: <prefix>/bin/gemini and <prefix>/lib/node_modules.
	unix := t.TempDir()
	js = filepath.Join(unix, "lib", "node_modules", "@google", "gemini-cli", "bundle", "gemini.js")
	write(t, js, "")
	write(t, filepath.Join(unix, "bin", "gemini"), "#!/usr/bin/env node\n")
	if got := bundleFor(filepath.Join(unix, "bin", "gemini")); got != js {
		t.Errorf("unix layout: %q, want %q", got, js)
	}
	if got := bundleFor(filepath.Join(t.TempDir(), "gemini.cmd")); got != "" {
		t.Errorf("nothing installed: %q", got)
	}
}

func TestDiagnose(t *testing.T) {
	node := func(v string, err error) func(context.Context) (string, error) {
		return func(context.Context) (string, error) { return v, err }
	}
	none := func(string) string { return "" }
	ns := diagnose(context.Background(), none, node("v22.3.0", nil))
	if len(ns) != 1 || ns[0].Warn || ns[0].Text != "node v22.3.0 (Gemini CLI)" {
		t.Errorf("node 22: %+v", ns)
	}
	if ns := diagnose(context.Background(), none, node("v18.19.0", nil)); len(ns) != 1 || !ns[0].Warn || !strings.Contains(ns[0].Text, "too old") {
		t.Errorf("node 18: %+v", ns)
	}
	if ns := diagnose(context.Background(), none, node("", errors.New("no node"))); len(ns) != 1 || !ns[0].Warn || !strings.Contains(ns[0].Text, "not found") {
		t.Errorf("no node: %+v", ns)
	}
	set := func(k string) string {
		if k == encryptedStorageEnv || k == "GOOGLE_APPLICATION_CREDENTIALS" {
			return "x"
		}
		return ""
	}
	ns = diagnose(context.Background(), set, node("v20.0.0", nil))
	if len(ns) != 3 || !ns[1].Warn || !strings.Contains(ns[1].Text, encryptedStorageEnv) || !ns[2].Warn || !strings.Contains(ns[2].Text, "GOOGLE_APPLICATION_CREDENTIALS") {
		t.Errorf("env: %+v", ns)
	}
	if n := nodeMajor("garbage"); n != 0 {
		t.Errorf("nodeMajor = %d", n)
	}
	if ns := (CLI{}).AccountNotices("/x"); len(ns) != 1 || !ns[0].Warn || !strings.Contains(ns[0].Text, "core.hooksPath") {
		t.Errorf("AccountNotices = %+v", ns)
	}
}

func TestCheckCredentials(t *testing.T) {
	dir := t.TempDir()
	c := CLI{Home: t.TempDir()}
	if err := c.CheckCredentials(dir); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(dir, ".gemini", "gemini-credentials.json"), "x")
	if err := c.CheckCredentials(dir); err == nil {
		t.Fatal("the encrypted store went unnoticed")
	}
}
