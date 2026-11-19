package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/rogpeppe/go-internal/testscript"

	"github.com/squadrn/cli/internal/release"

	"github.com/squadrn/cli/internal/fsx"
)

func TestMain(m *testing.M) {
	cmds := map[string]func(){
		"squadrn": func() {
			// Scripts sign their fake releases with their own key, never the real one.
			buildSigners = func() string { return os.Getenv("SQUADRN_TEST_SIGNERS") }
			// SQUADRN_TEST_TTY stands in for the controlling terminal: answers come from that file.
			openTTY = func() (io.Reader, io.Writer, func(), bool) {
				f, err := os.Open(os.Getenv("SQUADRN_TEST_TTY"))
				if err != nil {
					return nil, nil, nil, false
				}
				return f, os.Stderr, func() { _ = f.Close() }, true
			}
			if v := os.Getenv("SQUADRN_TEST_VERSION"); v != "" {
				Version = v
			}
			os.Exit(Run(os.Args[1:]))
		},
	}
	if runtime.GOOS == "windows" {
		// Windows cannot run the shell-script fake; claude.exe is this test binary instead.
		cmds["claude"] = func() { os.Exit(fakeClaudeMain(os.Args[1:])) }
	}
	testscript.Main(m, cmds)
}

// fakeClaudeMain behaves exactly like the fakeClaude shell script, for Windows.
func fakeClaudeMain(args []string) int {
	cfg := os.Getenv("CLAUDE_CONFIG_DIR")
	dir := cfg
	if cfg == "" {
		cfg, dir = "<unset>", filepath.Join(os.Getenv("HOME"), ".claude")
	}
	cmd := strings.Join(args, " ")
	exists := func(name string) bool { _, err := os.Stat(filepath.Join(dir, name)); return err == nil }
	cat := func(name, fallback string) {
		if b, err := os.ReadFile(filepath.Join(dir, name)); err == nil {
			fmt.Print(string(b))
		} else {
			fmt.Println(fallback)
		}
	}
	switch {
	case cmd == "auth status":
		org, _ := os.ReadFile(filepath.Join(dir, ".org"))
		plan, _ := os.ReadFile(filepath.Join(dir, ".plan"))
		if exists(".loggedin") {
			fmt.Printf(`{"loggedIn":true,"email":"me@%s","orgName":"%s","subscriptionType":"%s"}`+"\n", filepath.Base(dir), strings.TrimSpace(string(org)), strings.TrimSpace(string(plan)))
			return 0
		}
		fmt.Println(`{"loggedIn":false}`)
		return 1
	case cmd == "auth logout":
		_ = os.Remove(filepath.Join(dir, ".loggedin"))
	case strings.HasPrefix(cmd, "mcp get "):
		b, _ := os.ReadFile(filepath.Join(dir, ".mcp"))
		if !slices.Contains(strings.Split(strings.ReplaceAll(string(b), "\r", ""), "\n"), args[2]) {
			return 1
		}
	case strings.HasPrefix(cmd, "mcp add-json -s user ") && len(args) > 4:
		f, err := os.OpenFile(filepath.Join(dir, ".mcp"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err == nil {
			_, _ = fmt.Fprintln(f, args[4])
			_ = f.Close()
		}
	case cmd == "plugin marketplace list --json":
		cat(".markets", "[]")
	case strings.HasPrefix(cmd, "plugin marketplace add "):
		_ = os.WriteFile(filepath.Join(dir, ".markets"), []byte(`[{"name":"squadrn"}]`+"\n"), 0o644)
	case cmd == "plugin list --json":
		cat(".plugins", "[]")
	case strings.HasPrefix(cmd, "plugin install "):
		_ = os.WriteFile(filepath.Join(dir, ".plugins"), []byte(`[{"id":"squadrn@squadrn"}]`+"\n"), 0o644)
	default:
		fmt.Printf("cfg=%s args=%s\n", cfg, cmd)
		wd, _ := os.Getwd()
		if real, err := filepath.EvalSymlinks(wd); err == nil {
			wd = real
		}
		f, err := os.OpenFile(filepath.Join(os.Getenv("HOME"), "calls.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err == nil {
			_, _ = fmt.Fprintf(f, "cfg=%s pwd=%s args=%s\n", cfg, wd, cmd)
			_ = f.Close()
		}
		if id := os.Getenv("FAKE_HOLD"); id != "" {
			// A running session listed in Claude Code's registry until it is terminated.
			pid := os.Getpid()
			entry, _ := json.Marshal(map[string]any{"pid": pid, "sessionId": id, "cwd": wd, "status": "busy", "kind": "interactive"})
			if os.Getenv("FAKE_NOREG") == "" {
				_ = os.MkdirAll(filepath.Join(dir, "sessions"), 0o755)
				_ = os.WriteFile(filepath.Join(dir, "sessions", strconv.Itoa(pid)+".json"), entry, 0o644)
			}
			_ = os.WriteFile(filepath.Join(os.Getenv("HOME"), "held.txt"), []byte(strconv.Itoa(pid)+"\n"), 0o644)
			time.Sleep(60 * time.Second)
		}
	}
	return 0
}

// fakeClaude answers the queries squadrn makes and records every other invocation in
// $HOME/calls.log. Inside a tmux pane it stays alive like an interactive session. With
// FAKE_HOLD=<session> it lists itself in the account's session registry (not with FAKE_NOREG
// set), writes its pid to $HOME/held.txt and runs until interrupted.
// `auth status` reports logged in when <config dir>/.loggedin exists, with .org and .plan.
const fakeClaude = `#!/bin/sh
cfg=${CLAUDE_CONFIG_DIR:-<unset>}
dir=${CLAUDE_CONFIG_DIR:-$HOME/.claude}
case "$*" in
  "auth status")
    org=$(cat "$dir/.org" 2>/dev/null)
    plan=$(cat "$dir/.plan" 2>/dev/null)
    if [ -f "$dir/.loggedin" ]; then echo '{"loggedIn":true,"email":"me@'"$(basename "$dir")"'","orgName":"'"$org"'","subscriptionType":"'"$plan"'"}'; exit 0; fi
    echo '{"loggedIn":false}'; exit 1 ;;
  "auth logout") rm -f "$dir/.loggedin"; echo "cfg=$cfg args=$*" >>"$HOME/calls.log" ;;
  "mcp get "*) grep -qx "$3" "$dir/.mcp" 2>/dev/null ;;
  "mcp add-json -s user "*) echo "$5" >>"$dir/.mcp" ;;
  "plugin marketplace list --json") cat "$dir/.markets" 2>/dev/null || echo '[]' ;;
  "plugin marketplace add "*) echo '[{"name":"squadrn"}]' >"$dir/.markets" ;;
  "plugin list --json") cat "$dir/.plugins" 2>/dev/null || echo '[]' ;;
  "plugin install "*) echo '[{"id":"squadrn@squadrn"}]' >"$dir/.plugins" ;;
  *)
    echo "cfg=$cfg args=$*"
    echo "cfg=$cfg pwd=$(pwd -P) args=$*" >>"$HOME/calls.log"
    if [ -n "$FAKE_HOLD" ]; then
      [ -n "$FAKE_NOREG" ] || mkdir -p "$dir/sessions"
      [ -n "$FAKE_NOREG" ] || printf '{"pid":%s,"sessionId":"%s","cwd":"%s","status":"busy","kind":"interactive"}\n' $$ "$FAKE_HOLD" "$(pwd -P)" >"$dir/sessions/$$.json"
      trap 'rm -f "$dir/sessions/$$.json"; echo "stopped $$" >>"$HOME/calls.log"; kill $! 2>/dev/null; exit 0' INT TERM
      echo $$ >"$HOME/held.txt"
      sleep 60 &
      wait
    fi
    if [ -n "$TMUX_PANE" ]; then sleep 60; fi ;;
esac
`

// scriptEnv lists the variables Setup controls; exitcode forwards them to its child.
var scriptEnv = []string{
	"HOME", "PATH", "XDG_CONFIG_HOME", "XDG_CACHE_HOME", "XDG_DATA_HOME", "CLAUDE_CONFIG_DIR",
	"SQUADRN_MARKETPLACE", "SQUADRN_SHARED_MCP", "SQUADRN_REPO", "SQUADRN_TEST_SIGNERS", "SQUADRN_TEST_VERSION", "SQUADRN_TEST_TTY", "USERPROFILE", "FAKE_HOLD", "FAKE_NOREG", "XDG_STATE_HOME", "TMUX", "TMUX_TMPDIR", "GIT_CONFIG_GLOBAL", "GIT_CONFIG_NOSYSTEM",
	"GIT_AUTHOR_NAME", "GIT_AUTHOR_EMAIL", "GIT_COMMITTER_NAME", "GIT_COMMITTER_EMAIL",
}

// pathWithoutHome drops PATH entries under the real home dir, so tools installed for the
// developer running the tests (e.g. a real ~/.local/bin/squadrn) cannot leak into scripts.
func pathWithoutHome(path string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	var keep []string
	for _, d := range filepath.SplitList(path) {
		if rel, err := filepath.Rel(home, d); err == nil && !strings.HasPrefix(rel, "..") {
			continue
		}
		keep = append(keep, d)
	}
	return strings.Join(keep, string(os.PathListSeparator))
}

// -update rewrites the expected files embedded in scripts (cmp with an archive file), e.g. the
// golden help, completion and install-layout outputs in golden.txtar.
var updateScripts = flag.Bool("update", false, "update golden files embedded in scripts")

func kindOf(m fs.FileMode) string {
	switch {
	case m.IsDir():
		return "dir"
	case m&fs.ModeSymlink != 0:
		return "link"
	default:
		return "file"
	}
}

func TestScript(t *testing.T) {
	testscript.Run(t, testscript.Params{
		Dir:           filepath.Join("..", "..", "testdata", "script"),
		UpdateScripts: *updateScripts,
		Setup: func(env *testscript.Env) error {
			home := filepath.Join(env.WorkDir, "home")
			bin := filepath.Join(env.WorkDir, "bin")
			for _, d := range []string{filepath.Join(home, ".claude"), bin} {
				if err := os.MkdirAll(d, 0o755); err != nil {
					return err
				}
			}
			if err := os.WriteFile(filepath.Join(bin, "claude"), []byte(fakeClaude), 0o755); err != nil {
				return err
			}
			shim, err := filepath.Abs(filepath.Join("..", "..", "plugin", "bin", "squadrn-hook"))
			if err != nil {
				return err
			}
			// A private tmux server per script. The socket path must stay under ~104 bytes,
			// which the default temp dir on macOS exceeds. (No tmux on Windows.)
			tmpRoot := "/tmp"
			if runtime.GOOS == "windows" {
				tmpRoot = ""
			}
			sock, err := os.MkdirTemp(tmpRoot, "sqdt")
			if err != nil {
				return err
			}
			env.Defer(func() {
				kill := exec.Command("tmux", "kill-server")
				kill.Env = append(os.Environ(), "TMUX_TMPDIR="+sock, "TMUX=")
				_ = kill.Run()
				_ = os.RemoveAll(sock)
			})
			env.Setenv("SHIM", shim)
			env.Setenv("REPO_ROOT", filepath.Dir(filepath.Dir(filepath.Dir(shim))))
			env.Setenv("HOME", home)
			env.Setenv("USERPROFILE", home) // os.UserHomeDir on Windows
			env.Setenv("PATH", bin+string(os.PathListSeparator)+pathWithoutHome(env.Getenv("PATH")))
			env.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
			env.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
			env.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
			env.Setenv("XDG_STATE_HOME", filepath.Join(home, ".local", "state"))
			env.Setenv("TMUX_TMPDIR", sock)
			env.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
			env.Setenv("GIT_CONFIG_NOSYSTEM", "1")
			for _, k := range []string{"GIT_AUTHOR_NAME", "GIT_COMMITTER_NAME"} {
				env.Setenv(k, "t")
			}
			for _, k := range []string{"GIT_AUTHOR_EMAIL", "GIT_COMMITTER_EMAIL"} {
				env.Setenv(k, "t@t")
			}
			for _, k := range []string{"CLAUDE_CONFIG_DIR", "SQUADRN_MARKETPLACE", "SQUADRN_SHARED_MCP", "SQUADRN_REPO", "SQUADRN_TEST_SIGNERS", "SQUADRN_TEST_VERSION", "SQUADRN_TEST_TTY", "TMUX", "FAKE_HOLD", "FAKE_NOREG"} {
				env.Setenv(k, "")
			}
			return nil
		},
		Cmds: map[string]func(ts *testscript.TestScript, neg bool, args []string){
			// waitfor FILE REGEXP polls until FILE matches (output written by tmux panes; generous deadline
			// because panes start slowly on loaded CI machines).
			"waitfor": func(ts *testscript.TestScript, neg bool, args []string) {
				if neg || len(args) != 2 {
					ts.Fatalf("usage: waitfor file regexp")
				}
				re, err := regexp.Compile("(?m)" + args[1])
				ts.Check(err)
				deadline := time.Now().Add(15 * time.Second)
				for {
					if b, err := os.ReadFile(ts.MkAbs(args[0])); err == nil && re.Match(b) {
						return
					}
					if time.Now().After(deadline) {
						ts.Fatalf("%s: no match for %q within 15s", args[0], args[1])
					}
					time.Sleep(50 * time.Millisecond)
				}
			},
			// tree DIR prints every entry under DIR, sorted: relative path, type, permission bits
			// and, for links, the target with $WORK replaced. The install-layout golden uses it.
			"tree": func(ts *testscript.TestScript, neg bool, args []string) {
				if neg || len(args) != 1 {
					ts.Fatalf("usage: tree dir")
				}
				root := ts.MkAbs(args[0])
				work := ts.Getenv("WORK")
				var lines []string
				err := filepath.WalkDir(root, func(p string, _ fs.DirEntry, err error) error {
					if err != nil || p == root {
						return err
					}
					rel, _ := filepath.Rel(root, p)
					info, err := os.Lstat(p)
					if err != nil {
						return err
					}
					line := fmt.Sprintf("%s %s %v", filepath.ToSlash(rel), kindOf(info.Mode()), info.Mode().Perm())
					if info.Mode()&fs.ModeSymlink != 0 {
						// A link's own permission bits differ between Linux and macOS: print the target only.
						target, _ := os.Readlink(p)
						line = filepath.ToSlash(rel) + " link -> " + strings.ReplaceAll(filepath.ToSlash(target), filepath.ToSlash(work), "$WORK")
					}
					lines = append(lines, line)
					return nil
				})
				ts.Check(err)
				sort.Strings(lines)
				_, _ = fmt.Fprintln(ts.Stdout(), strings.Join(lines, "\n"))
			},
			// release DIR lays out an unpacked release in DIR without a shell: this test binary as
			// squadrn(.exe), the plugin and the marketplace; on Windows the Windows hooks.json.
			"release": func(ts *testscript.TestScript, neg bool, args []string) {
				if neg || len(args) != 1 {
					ts.Fatalf("usage: release dir")
				}
				dst := ts.MkAbs(args[0])
				root := ts.Getenv("REPO_ROOT")
				self, err := os.Executable()
				ts.Check(err)
				ts.Check(fsx.CopyFile(self, filepath.Join(dst, release.BinaryName), 0o755, false))
				ts.Check(fsx.CopyTree(filepath.Join(root, "plugin"), filepath.Join(dst, "plugin"), 0o755, false))
				ts.Check(fsx.CopyTree(filepath.Join(root, ".claude-plugin"), filepath.Join(dst, ".claude-plugin"), 0o755, false))
				if runtime.GOOS == "windows" {
					ts.Check(fsx.CopyFile(filepath.Join(root, "scripts", "hooks.windows.json"), filepath.Join(dst, "plugin", "hooks", "hooks.json"), 0o644, false))
				}
			},
			// jsonexpand SRC DST copies SRC to DST (creating its dir), replacing $VAR with the
			// variable's value escaped for a JSON string (Windows paths have backslashes).
			"jsonexpand": func(ts *testscript.TestScript, neg bool, args []string) {
				if neg || len(args) != 2 {
					ts.Fatalf("usage: jsonexpand src dst")
				}
				out := os.Expand(ts.ReadFile(args[0]), func(k string) string {
					b, _ := json.Marshal(ts.Getenv(k))
					return string(b[1 : len(b)-1])
				})
				dst := ts.MkAbs(args[1])
				ts.Check(os.MkdirAll(filepath.Dir(dst), 0o755))
				ts.Check(os.WriteFile(dst, []byte(out), 0o600))
			},
			// chtime FILE DURATION sets FILE's modification time to now plus DURATION (e.g. -1h).
			"chtime": func(ts *testscript.TestScript, neg bool, args []string) {
				if neg || len(args) != 2 {
					ts.Fatalf("usage: chtime file duration")
				}
				d, err := time.ParseDuration(args[1])
				ts.Check(err)
				t := time.Now().Add(d)
				ts.Check(os.Chtimes(ts.MkAbs(args[0]), t, t))
			},
			// livesession CONFIGDIR SESSION CWD lists a running session in CONFIGDIR's Claude Code
			// registry, with the pid of this (live) test process.
			"livesession": func(ts *testscript.TestScript, neg bool, args []string) {
				if neg || len(args) != 3 {
					ts.Fatalf("usage: livesession configdir session cwd")
				}
				dir := filepath.Join(ts.MkAbs(args[0]), "sessions")
				b, err := json.Marshal(map[string]any{"pid": os.Getpid(), "sessionId": args[1], "cwd": ts.MkAbs(args[2])})
				ts.Check(err)
				ts.Check(os.MkdirAll(dir, 0o755))
				ts.Check(os.WriteFile(filepath.Join(dir, strconv.Itoa(os.Getpid())+".json"), b, 0o600))
			},
			// envfile NAME FILE sets NAME to the contents of FILE.
			"envfile": func(ts *testscript.TestScript, neg bool, args []string) {
				if neg || len(args) != 2 {
					ts.Fatalf("usage: envfile NAME FILE")
				}
				ts.Setenv(args[0], ts.ReadFile(args[1]))
			},
			// exitcode N cmd args... asserts the exact exit status, run from the current directory.
			"exitcode": func(ts *testscript.TestScript, neg bool, args []string) {
				if neg || len(args) < 2 {
					ts.Fatalf("usage: exitcode N command [args...]")
				}
				want, err := strconv.Atoi(args[0])
				ts.Check(err)
				cmd := exec.Command(args[1], args[2:]...)
				cmd.Dir = ts.MkAbs(".")
				cmd.Env = os.Environ()
				for _, k := range scriptEnv {
					cmd.Env = append(cmd.Env, k+"="+ts.Getenv(k))
				}
				out, _ := cmd.CombinedOutput()
				if got := cmd.ProcessState.ExitCode(); got != want {
					ts.Fatalf("%v: exit %d, want %d\n%s", args[1:], got, want, out)
				}
			},
		},
	})
}
