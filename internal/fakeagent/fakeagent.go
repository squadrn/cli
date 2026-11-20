// Package fakeagent holds stand-ins for the agent CLIs (claude, …) that tests put first on PATH:
// the testscript scripts of internal/cli and the end-to-end harness in e2e/. A fake answers the
// queries squadrn makes from files in the account's config dir and records every other
// invocation in $HOME/calls.log; it never touches the network or a real account.
//
// A test binary becomes a fake when it is started under the CLI's name (claude, claude.exe) or
// with FAKE_AGENT=<name>; its TestMain calls Main first. Every fake understands:
//
//	FAKE_HOLD=<session>   stay alive like an interactive session, listed in the account's
//	                      session registry (not with FAKE_NOREG), pid in $HOME/held.txt;
//	                      SIGINT/SIGTERM log "stopped <pid>" and exit 0
//	FAKE_EXEC=<json array> run that command in the session's environment and directory and
//	                      exit with its status (e.g. a git push through the guard)
//	FAKE_LOGIN_FAILS=1    login does not log in
//	FAKE_LOGIN_WAIT=<d>   login takes that long, like a person signing in (a Go duration)
//	FAKE_VERSION=<v>      --version prints it the way the CLI does (else it is a session)
//
// See e2e/README.md for adding the fake of a new provider.
package fakeagent

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// agents maps an agent CLI's executable name to its fake.
var agents = map[string]func(args []string) int{
	"claude": Claude,
	"codex":  Codex,
}

// Names lists the fakes, sorted.
func Names() []string {
	var names []string
	for n := range agents {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// Main runs the fake this process was started as and exits; it returns when it is none.
func Main() {
	name := os.Getenv("FAKE_AGENT")
	_ = os.Unsetenv("FAKE_AGENT") // children (git, hooks) are not fakes
	if name == "" {
		name = strings.TrimSuffix(strings.ToLower(filepath.Base(os.Args[0])), ".exe")
	}
	if f, ok := agents[name]; ok {
		os.Exit(f(os.Args[1:]))
	}
}

// logCall appends a line to $HOME/calls.log.
func logCall(format string, args ...any) {
	f, err := os.OpenFile(filepath.Join(os.Getenv("HOME"), "calls.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err == nil {
		_, _ = fmt.Fprintf(f, format+"\n", args...)
		_ = f.Close()
	}
}

// workdir is the physical current directory, like pwd -P.
func workdir() string {
	wd, _ := os.Getwd()
	if real, err := filepath.EvalSymlinks(wd); err == nil {
		return real
	}
	return wd
}

// session plays an interactive session: FAKE_EXEC runs a command, FAKE_HOLD stays alive in
// registry (a dir of <pid>.json files, "" for none), and inside a tmux pane it stays alive too.
func session(registry string) int {
	if spec := os.Getenv("FAKE_EXEC"); spec != "" {
		var argv []string
		if err := json.Unmarshal([]byte(spec), &argv); err != nil || len(argv) == 0 {
			fmt.Fprintf(os.Stderr, "fake agent: FAKE_EXEC must be a JSON array of strings: %v\n", err)
			return 2
		}
		cmd := exec.Command(argv[0], argv[1:]...)
		cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
		if err := cmd.Run(); err != nil {
			if cmd.ProcessState != nil {
				return cmd.ProcessState.ExitCode()
			}
			fmt.Fprintln(os.Stderr, "fake agent:", err)
			return 127
		}
		return 0
	}
	if id := os.Getenv("FAKE_HOLD"); id != "" {
		pid := os.Getpid()
		entry := ""
		if os.Getenv("FAKE_NOREG") == "" && registry != "" {
			b, _ := json.Marshal(map[string]any{"pid": pid, "sessionId": id, "cwd": workdir(), "status": "busy", "kind": "interactive"})
			entry = filepath.Join(registry, strconv.Itoa(pid)+".json")
			_ = os.MkdirAll(registry, 0o755)
			_ = os.WriteFile(entry, b, 0o644)
		}
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
		_ = os.WriteFile(filepath.Join(os.Getenv("HOME"), "held.txt"), []byte(strconv.Itoa(pid)+"\n"), 0o644)
		select {
		case <-sig:
			if entry != "" {
				_ = os.Remove(entry)
			}
			logCall("stopped %d", pid)
		case <-time.After(60 * time.Second):
		}
		return 0
	}
	if os.Getenv("TMUX_PANE") != "" {
		time.Sleep(60 * time.Second)
	}
	return 0
}

// loginWait stands in for the time a person takes to sign in (FAKE_LOGIN_WAIT).
func loginWait() {
	if d, err := time.ParseDuration(os.Getenv("FAKE_LOGIN_WAIT")); err == nil {
		time.Sleep(d)
	}
}
