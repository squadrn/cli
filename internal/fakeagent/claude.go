package fakeagent

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// Claude fakes Claude Code. The account is CLAUDE_CONFIG_DIR, else ~/.claude (main). Files in
// it drive the answers: .loggedin (with .org and .plan) for auth status, .mcp for user MCP
// servers, .markets and .plugins for the plugin registry, sessions/ for running sessions.
func Claude(args []string) int {
	cfg := os.Getenv("CLAUDE_CONFIG_DIR")
	dir := cfg
	if cfg == "" {
		cfg, dir = "<unset>", filepath.Join(os.Getenv("HOME"), ".claude")
	}
	cmd := strings.Join(args, " ")
	file := func(name string) string { return filepath.Join(dir, name) }
	read := func(name string) string { b, _ := os.ReadFile(file(name)); return strings.TrimSpace(string(b)) }
	cat := func(name, fallback string) {
		if b, err := os.ReadFile(file(name)); err == nil {
			fmt.Print(string(b))
		} else {
			fmt.Println(fallback)
		}
	}
	write := func(name, content string) { _ = os.WriteFile(file(name), []byte(content), 0o644) }
	switch {
	case cmd == "auth status":
		if _, err := os.Stat(file(".loggedin")); err == nil {
			fmt.Printf(`{"loggedIn":true,"email":"me@%s","orgName":"%s","subscriptionType":"%s"}`+"\n", filepath.Base(dir), read(".org"), read(".plan"))
			return 0
		}
		fmt.Println(`{"loggedIn":false}`)
		return 1
	case cmd == "auth logout":
		_ = os.Remove(file(".loggedin"))
		logCall("cfg=%s args=%s", cfg, cmd)
	case cmd == "auth login":
		if os.Getenv("FAKE_LOGIN_FAILS") == "" {
			write(".loggedin", "")
		}
		logCall("cfg=%s args=%s", cfg, cmd)
	case strings.HasPrefix(cmd, "mcp get ") && len(args) > 2:
		if !slices.Contains(strings.Split(strings.ReplaceAll(read(".mcp"), "\r", ""), "\n"), args[2]) {
			return 1
		}
	case strings.HasPrefix(cmd, "mcp add-json -s user ") && len(args) > 4:
		f, err := os.OpenFile(file(".mcp"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err == nil {
			_, _ = fmt.Fprintln(f, args[4])
			_ = f.Close()
		}
	case cmd == "plugin marketplace list --json":
		cat(".markets", "[]")
	case strings.HasPrefix(cmd, "plugin marketplace add "):
		write(".markets", `[{"name":"squadrn"}]`+"\n")
	case cmd == "plugin list --json":
		cat(".plugins", "[]")
	case strings.HasPrefix(cmd, "plugin install "):
		write(".plugins", `[{"id":"squadrn@squadrn"}]`+"\n")
	case strings.HasPrefix(cmd, "plugin uninstall "):
		_ = os.Remove(file(".plugins"))
		logCall("cfg=%s args=%s", cfg, cmd)
	case strings.HasPrefix(cmd, "plugin marketplace remove "):
		_ = os.Remove(file(".markets"))
		logCall("cfg=%s args=%s", cfg, cmd)
	default:
		fmt.Printf("cfg=%s args=%s\n", cfg, cmd)
		logCall("cfg=%s pwd=%s args=%s", cfg, workdir(), cmd)
		return session(file("sessions"))
	}
	return 0
}
