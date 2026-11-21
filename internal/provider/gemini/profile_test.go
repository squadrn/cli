package gemini

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"

	"github.com/squadrn/squadrn-cli/internal/provider"
)

func TestProfileSettings(t *testing.T) {
	p := Profiles{}
	doc, err := p.Profile(provider.ProfileStandard, []string{"privacy", "no-attribution"}, provider.ProfileContext{Notify: true})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(doc)
	for _, want := range []string{
		`"enableAutoUpdate":false`, `"enableNotifications":false`, `"sessionRetention":{"enabled":true,"maxAge":"30d"}`,
		`"disableYoloMode":true`, `"GIT_CONFIG_KEY_7"`, `"TMUX_PANE"`, `"experimental":{"worktrees":true}`,
		`"privacy":{"usageStatisticsEnabled":false}`, `"telemetry":{"enabled":false}`,
	} {
		if !strings.Contains(string(b), want) {
			t.Errorf("standard lacks %s: %s", want, b)
		}
	}
	doc, _ = p.Profile(provider.ProfileStrict, nil, provider.ProfileContext{})
	b, _ = json.Marshal(doc)
	if !strings.Contains(string(b), `"maxAge":"14d"`) || !strings.Contains(string(b), `"defaultApprovalMode":"default"`) || strings.Contains(string(b), "enableNotifications") {
		t.Errorf("strict: %s", b)
	}
	doc, _ = p.Profile(provider.ProfileMinimal, nil, provider.ProfileContext{})
	b, _ = json.Marshal(doc)
	if strings.Contains(string(b), "enableAutoUpdate") || strings.Contains(string(b), "disableYoloMode") || !strings.Contains(string(b), "allowedEnvironmentVariables") {
		t.Errorf("minimal: %s", b)
	}
	if _, err := p.Profile("nope", nil, provider.ProfileContext{}); err == nil {
		t.Error("unknown profile")
	}
	if _, err := p.Profile(provider.ProfileMinimal, []string{"nope"}, provider.ProfileContext{}); err == nil {
		t.Error("unknown option")
	}
}

type policy struct {
	Rule []struct {
		ToolName      any    `toml:"toolName"`
		CommandPrefix string `toml:"commandPrefix"`
		ArgsPattern   string `toml:"argsPattern"`
		Decision      string `toml:"decision"`
		Priority      int    `toml:"priority"`
	} `toml:"rule"`
}

func readPolicy(t *testing.T, name string, ctx provider.ProfileContext) policy {
	t.Helper()
	files, err := Profiles{}.ProfileFiles(name, nil, ctx)
	if err != nil {
		t.Fatal(err)
	}
	var pol policy
	if _, err := toml.Decode(string(files[PolicyFile]), &pol); err != nil {
		t.Fatalf("%s: %v\n%s", name, err, files[PolicyFile])
	}
	return pol
}

// denies reports whether a rule of pol denies a file tool the JSON arguments args (Gemini's
// stable JSON of the call's arguments); RE2 is Go's regexp syntax.
func denies(t *testing.T, pol policy, args string) bool {
	t.Helper()
	for _, r := range pol.Rule {
		if r.Decision != "deny" {
			continue
		}
		re, err := regexp.Compile(r.ArgsPattern)
		if err != nil {
			t.Fatalf("pattern %q: %v", r.ArgsPattern, err)
		}
		if re.MatchString(args) {
			return true
		}
	}
	return false
}

func TestProfilePolicy(t *testing.T) {
	ctx := provider.ProfileContext{OtherCredentials: []string{"~/.gemini-other/.gemini/oauth_creds.json", "~/.claude-x/.credentials.json"}}
	std := readPolicy(t, provider.ProfileStandard, ctx)
	for _, args := range []string{
		`{"file_path":"/repo/.env"}`, `{"file_path":".env.local"}`, `{"file_path":"/Users/me/.ssh/id_ed25519"}`,
		`{"file_path":"~/.ssh"}`, `{"include":["src/**",".env"]}`, `{"pattern":"**/.env"}`,
		`{"file_path":"/Users/me/.gemini-other/.gemini/oauth_creds.json"}`, `{"file_path":"C:\\Users\\me\\.aws\\credentials"}`,
		`{"file_path":"/Users/me/.config/gh/hosts.yml"}`, `{"file_path":"/repo/secrets/key.pem"}`, `{"file_path":"/Users/me/.npmrc"}`,
	} {
		if !denies(t, std, args) {
			t.Errorf("standard allows %s", args)
		}
	}
	for _, args := range []string{
		`{"file_path":"/repo/main.go"}`, `{"file_path":"/repo/.envrc.example2/x"}`, `{"file_path":"/repo/my.env.go"}`,
		`{"file_path":"/Users/me/.sshd_config"}`, `{"file_path":"/repo/.npmrc.d/x"}`,
	} {
		if denies(t, std, args) {
			t.Errorf("standard denies %s", args)
		}
	}
	var ask bool
	for _, r := range std.Rule {
		if r.Decision == "ask_user" && r.CommandPrefix == "git push" && r.ToolName == shellTool {
			ask = true
		}
		if r.Decision == "deny" && !slices.Equal(anyStrings(r.ToolName), fileTools) {
			t.Errorf("deny rule for %v", r.ToolName)
		}
	}
	if !ask {
		t.Error("standard does not ask before git push")
	}
	min := readPolicy(t, provider.ProfileMinimal, ctx)
	if !denies(t, min, `{"file_path":"/repo/.env"}`) || !denies(t, min, `{"file_path":"/h/.claude-x/.credentials.json"}`) || denies(t, min, `{"file_path":"/h/.aws/credentials"}`) {
		t.Error("minimal")
	}
	for _, r := range min.Rule {
		if r.Decision == "ask_user" {
			t.Error("minimal asks")
		}
	}
	if _, err := (Profiles{}).ProfileFiles("nope", nil, ctx); err == nil {
		t.Error("unknown profile")
	}
}

func anyStrings(v any) []string {
	l, _ := v.([]any)
	var out []string
	for _, s := range l {
		out = append(out, s.(string))
	}
	return out
}

func TestWithPolicyAndWorktree(t *testing.T) {
	home := t.TempDir()
	c := CLI{Home: home}
	acct := filepath.Join(home, ".gemini-work")
	if got := c.withPolicy(acct, []string{"-p", "x"}); !slices.Equal(got, []string{"-p", "x"}) {
		t.Errorf("no policy file: %v", got)
	}
	write(t, filepath.Join(acct, ".gemini", PolicyFile), "")
	pol := filepath.Join(acct, ".gemini", PolicyFile)
	if got := c.withPolicy(acct, []string{"-p", "x"}); !slices.Equal(got, []string{"--policy", pol, "-p", "x"}) {
		t.Errorf("session: %v", got)
	}
	if got := c.withPolicy(acct, []string{"mcp", "list"}); !slices.Equal(got, []string{"mcp", "list"}) {
		t.Errorf("subcommand: %v", got)
	}
	if got := c.withPolicy("", nil); got != nil {
		t.Errorf("main: %v", got)
	}
	if !slices.Equal(c.WorktreeArgs("feat"), []string{"--worktree", "feat"}) {
		t.Error("WorktreeArgs")
	}
}

func TestMCP(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	c := CLI{Home: home}
	write(t, filepath.Join(home, ".gemini", "settings.json"), "// main\n{\"mcpServers\":{\"docs\":{\"command\":\"npx\",\"args\":[\"docs\"]},\"secret\":{\"command\":\"s\",\"env\":{\"T\":\"x\"}}}}")
	servers, err := c.ListMCP(ctx, "")
	if err != nil || len(servers) != 2 || !strings.Contains(string(servers["docs"]), `"npx"`) {
		t.Fatalf("ListMCP = %v, %v", servers, err)
	}
	acct := filepath.Join(home, ".gemini-work")
	if c.HasMCP(ctx, acct, "docs") {
		t.Fatal("not added yet")
	}
	if err := c.AddMCP(ctx, acct, "docs", servers["docs"]); err != nil {
		t.Fatal(err)
	}
	if !c.HasMCP(ctx, acct, "docs") || c.HasMCP(ctx, acct, "secret") {
		t.Fatal("HasMCP")
	}
	if err := c.AddMCP(ctx, "", "x", []byte(`{}`)); err == nil {
		t.Fatal("main edited")
	}
	if err := c.AddMCP(ctx, acct, "x", []byte(`{`)); err == nil {
		t.Fatal("bad definition")
	}
}

func TestTrust(t *testing.T) {
	home := t.TempDir()
	c := CLI{Home: home}
	acct := filepath.Join(home, ".gemini-work")
	repo := filepath.Join(home, "src", "api")
	if ok, err := c.Trusted(acct, repo); ok || err != nil {
		t.Fatalf("empty: %v %v", ok, err)
	}
	if err := c.Trust(acct, repo); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(acct, ".gemini", trustFile))
	if !strings.Contains(string(b), `"TRUST_FOLDER"`) {
		t.Fatalf("trust file: %s", b)
	}
	for dir, want := range map[string]bool{
		repo: true, filepath.Join(repo, ".gemini", "worktrees", "x"): true, filepath.Join(home, "src"): false, filepath.Join(home, "src", "web"): false,
	} {
		if ok, _ := c.Trusted(acct, dir); ok != want {
			t.Errorf("Trusted(%s) = %v", dir, ok)
		}
	}
	// A decision Gemini recorded (TRUST_PARENT on a sibling, DO_NOT_TRUST) is one too.
	write(t, filepath.Join(acct, ".gemini", trustFile), `{"`+filepath.ToSlash(filepath.Join(home, "src", "web", "x"))+`": "TRUST_PARENT", "`+filepath.ToSlash(filepath.Join(home, "no"))+`": "DO_NOT_TRUST"}`)
	if ok, _ := c.Trusted(acct, filepath.Join(home, "src", "web")); !ok {
		t.Error("TRUST_PARENT")
	}
	if ok, _ := c.Trusted(acct, filepath.Join(home, "no")); !ok {
		t.Error("DO_NOT_TRUST is a decision")
	}
	write(t, filepath.Join(acct, ".gemini", trustFile), `{`)
	if _, err := c.Trusted(acct, repo); err == nil {
		t.Error("bad file")
	}
	write(t, filepath.Join(acct, ".gemini", trustFile), "  \n")
	if err := c.Trust(acct, repo); err != nil {
		t.Fatal(err)
	}
}
