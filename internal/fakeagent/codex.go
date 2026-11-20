package fakeagent

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// Codex fakes the Codex CLI. The account is CODEX_HOME, else ~/.codex (main). Files in it drive
// the answers: .loggedin for `login status` (printed on stderr, like Codex), .mcp and .mcplist
// for MCP servers, .limits for account/rateLimits/read. `app-server` speaks stdio JSONL:
// initialize, account/read, account/rateLimits/read, hooks/list (squadrn's hooks from
// hooks.json, keyed and hashed like Codex, trusted when config.toml records that hash) and
// config/batchWrite (records the trust).
func Codex(args []string) int {
	cfg := os.Getenv("CODEX_HOME")
	dir := cfg
	if cfg == "" {
		cfg, dir = "<unset>", filepath.Join(os.Getenv("HOME"), ".codex")
	}
	cmd := strings.Join(args, " ")
	file := func(name string) string { return filepath.Join(dir, name) }
	read := func(name string) string { b, _ := os.ReadFile(file(name)); return strings.TrimSpace(string(b)) }
	loggedIn := func() bool { _, err := os.Stat(file(".loggedin")); return err == nil }
	switch {
	case cmd == "--version":
		v := os.Getenv("FAKE_VERSION")
		if v == "" {
			v = "0.171.0"
		}
		fmt.Println("codex-cli " + v)
	case cmd == "login status":
		if loggedIn() {
			fmt.Fprintln(os.Stderr, "Logged in using ChatGPT")
			return 0
		}
		fmt.Fprintln(os.Stderr, "Not logged in")
		return 1
	case cmd == "login":
		loginWait()
		if os.Getenv("FAKE_LOGIN_FAILS") == "" {
			_ = os.WriteFile(file(".loggedin"), nil, 0o600)
		}
		logCall("cfg=%s args=%s", cfg, cmd)
	case cmd == "logout":
		_ = os.Remove(file(".loggedin"))
		logCall("cfg=%s args=%s", cfg, cmd)
	case strings.HasPrefix(cmd, "mcp get ") && len(args) > 2:
		if !slices.Contains(strings.Split(strings.ReplaceAll(read(".mcp"), "\r", ""), "\n"), args[2]) {
			fmt.Fprintf(os.Stderr, "Error: No MCP server named '%s' found.\n", args[2])
			return 1
		}
	case strings.HasPrefix(cmd, "mcp add ") && len(args) > 2:
		f, err := os.OpenFile(file(".mcp"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err == nil {
			_, _ = fmt.Fprintln(f, args[2])
			_ = f.Close()
		}
		logCall("cfg=%s args=%s", cfg, cmd)
	case cmd == "mcp list --json":
		if b, err := os.ReadFile(file(".mcplist")); err == nil {
			fmt.Print(string(b))
		} else {
			fmt.Println("[]")
		}
	case cmd == "app-server":
		codexAppServer(dir, cfg, loggedIn())
	default:
		fmt.Printf("cfg=%s args=%s\n", cfg, cmd)
		logCall("cfg=%s pwd=%s args=%s", cfg, workdir(), cmd)
		return session("")
	}
	return 0
}

var trustedHash = regexp.MustCompile(`(?m)^trusted_hash = "([^"]*)"$`)

func codexAppServer(dir, cfg string, loggedIn bool) {
	real := dir
	if r, err := filepath.EvalSymlinks(dir); err == nil {
		real = r
	}
	hooksPath := filepath.Join(real, "hooks.json")
	configPath := filepath.Join(dir, "config.toml")
	out := json.NewEncoder(os.Stdout)
	reply := func(id json.RawMessage, result any) {
		_ = out.Encode(map[string]any{"id": id, "result": result})
	}
	in := bufio.NewScanner(os.Stdin)
	in.Buffer(make([]byte, 1<<20), 1<<20)
	for in.Scan() {
		var m struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if json.Unmarshal(in.Bytes(), &m) != nil || m.ID == nil {
			continue
		}
		switch m.Method {
		case "initialize":
			_ = out.Encode(map[string]any{"method": "remoteControl/status/changed", "params": map[string]any{}})
			reply(m.ID, map[string]any{"userAgent": "fake"})
		case "account/read":
			var account any
			if loggedIn {
				account = map[string]any{"type": "chatgpt", "email": "me@" + filepath.Base(dir), "planType": "pro"}
			}
			reply(m.ID, map[string]any{"account": account, "requiresOpenaiAuth": true})
		case "account/rateLimits/read":
			logCall("cfg=%s args=app-server account/rateLimits/read", cfg)
			b, err := os.ReadFile(filepath.Join(dir, ".limits"))
			if err != nil {
				_ = out.Encode(map[string]any{"id": m.ID, "error": map[string]any{"code": -32600, "message": "codex account authentication required to read rate limits"}})
				continue
			}
			reply(m.ID, json.RawMessage(b))
		case "hooks/list":
			reply(m.ID, map[string]any{"data": []any{map[string]any{"cwd": "/", "warnings": []any{}, "errors": []any{}, "hooks": codexHooks(hooksPath, configPath)}}})
		case "config/batchWrite":
			var p struct {
				Edits []struct {
					Value map[string]struct {
						TrustedHash string `json:"trusted_hash"`
					} `json:"value"`
				} `json:"edits"`
			}
			_ = json.Unmarshal(m.Params, &p)
			for _, e := range p.Edits {
				for k, v := range e.Value {
					setCodexTrust(configPath, k, v.TrustedHash)
				}
			}
			logCall("cfg=%s args=app-server config/batchWrite", cfg)
			reply(m.ID, map[string]any{"status": "ok"})
		default:
			_ = out.Encode(map[string]any{"id": m.ID, "error": map[string]any{"code": -32601, "message": "unknown method"}})
		}
	}
}

// codexHooks lists squadrn's hooks from hooks.json, each keyed and hashed by its event, position
// and definition as Codex does.
func codexHooks(hooksPath, configPath string) []any {
	b, err := os.ReadFile(hooksPath)
	if err != nil {
		return []any{}
	}
	var doc struct {
		Hooks map[string][]struct {
			Matcher string `json:"matcher"`
			Hooks   []struct {
				Command string `json:"command"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	_ = json.Unmarshal(b, &doc)
	cfg, _ := os.ReadFile(configPath)
	out := []any{}
	for _, event := range []string{"PreToolUse", "PermissionRequest", "UserPromptSubmit", "PostToolUse", "Stop"} {
		for gi, g := range doc.Hooks[event] {
			for hi, h := range g.Hooks {
				if !strings.Contains(h.Command, "squadrn-hook") && !strings.Contains(h.Command, "squadrn.exe") {
					continue
				}
				sum := sha256.Sum256([]byte(event + "\x00" + g.Matcher + "\x00" + h.Command))
				hash := "sha256:" + hex.EncodeToString(sum[:])
				status := "untrusted"
				for _, m := range trustedHash.FindAllStringSubmatch(string(cfg), -1) {
					if m[1] == hash {
						status = "trusted"
					}
				}
				out = append(out, map[string]any{
					"key": fmt.Sprintf("%s:%s:%d:%d", hooksPath, snake(event), gi, hi), "eventName": event, "handlerType": "command",
					"command": h.Command, "matcher": g.Matcher, "sourcePath": hooksPath, "source": "user", "enabled": true,
					"isManaged": false, "currentHash": hash, "trustStatus": status,
				})
			}
		}
	}
	return out
}

// snake is PreToolUse as pre_tool_use.
func snake(s string) string {
	var b strings.Builder
	for i, r := range s {
		if r >= 'A' && r <= 'Z' {
			if i > 0 {
				b.WriteByte('_')
			}
			r += 'a' - 'A'
		}
		b.WriteRune(r)
	}
	return b.String()
}

// setCodexTrust upserts hooks.state."<key>".trusted_hash in config.toml, as Codex does.
func setCodexTrust(configPath, key, hash string) {
	b, _ := os.ReadFile(configPath)
	header := "[hooks.state." + fmt.Sprintf("%q", key) + "]"
	var keep []string
	skip := false
	for _, l := range strings.Split(strings.TrimRight(string(b), "\n"), "\n") {
		switch {
		case l == header:
			skip = true
			continue
		case skip && strings.HasPrefix(l, "["):
			skip = false
		case skip:
			continue
		}
		keep = append(keep, l)
	}
	content := strings.TrimLeft(strings.Join(keep, "\n")+"\n\n"+header+"\ntrusted_hash = "+fmt.Sprintf("%q", hash)+"\n", "\n")
	_ = os.WriteFile(configPath, []byte(content), 0o600)
}
