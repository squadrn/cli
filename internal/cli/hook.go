package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/squadrn/squadrn/internal/config"
	"github.com/squadrn/squadrn/internal/githook"
	"github.com/squadrn/squadrn/internal/guard"
	"github.com/squadrn/squadrn/internal/proc"
	"github.com/squadrn/squadrn/internal/release"
)

// hookDeadline bounds a pre-tool decision; the plugin gives the hook 5 seconds.
const hookDeadline = 3 * time.Second

// hookInput is the part of Claude Code's hook payload squadrn reads.
type hookInput struct {
	Cwd string `json:"cwd"`
	// ToolName is Bash or PowerShell (Windows); missing means Bash (older Claude Code).
	ToolName  string `json:"tool_name"`
	ToolInput struct {
		Command string `json:"command"`
	} `json:"tool_input"`
}

type hookOutput struct {
	HookSpecificOutput struct {
		HookEventName            string `json:"hookEventName"`
		PermissionDecision       string `json:"permissionDecision"`
		PermissionDecisionReason string `json:"permissionDecisionReason"`
	} `json:"hookSpecificOutput"`
}

// hookEnv is what hook handlers need. They run on every tool call and git hook, so they skip
// the account store and command tree and must never fail open.
type hookEnv struct {
	stdin          io.Reader
	stdout, stderr io.Writer
	now            func() time.Time
}

const hookUsage = "usage: squadrn hook guard-git | notify <title> | git [--config <file>] <hook-name> [args...] | statusline [--then <command>] | limit"

// runHook handles `squadrn hook …` and returns the exit code.
func runHook(args []string, h hookEnv) int {
	if len(args) == 0 {
		_, _ = fmt.Fprintln(h.stderr, hookUsage)
		return exitUsage
	}
	switch args[0] {
	case "guard-git":
		if len(args) != 1 {
			// Exit 2 blocks the tool call in Claude Code, so a bad invocation still fails closed.
			_, _ = fmt.Fprintln(h.stderr, hookUsage)
			return exitUsage
		}
		h.guardGit()
		return exitOK
	case "notify":
		if len(args) != 2 {
			_, _ = fmt.Fprintln(h.stderr, hookUsage)
			return exitUsage
		}
		h.notify(args[1])
		return exitOK
	case "git":
		cfgFile := ""
		if len(args) > 2 && args[1] == githook.ConfigFlag {
			cfgFile, args = args[2], args[2:]
		}
		if len(args) < 2 {
			_, _ = fmt.Fprintln(h.stderr, hookUsage)
			return exitUsage
		}
		return h.git(cfgFile, args[1], args[2:])
	case "statusline":
		return h.statusline(args[1:])
	case "limit":
		if len(args) != 1 {
			_, _ = fmt.Fprintln(h.stderr, hookUsage)
			return exitUsage
		}
		return h.limit()
	case "-h", "--help", "help":
		_, _ = fmt.Fprintln(h.stdout, hookUsage)
		return exitOK
	}
	_, _ = fmt.Fprintln(h.stderr, hookUsage)
	return exitUsage
}

// hookGuard builds the guard from the config: cfgFile when the git hook shim names it (baked in
// at install, so the pushing process's environment cannot point it elsewhere), else the usual
// location. A broken config still yields the defaults, and cfgErr is reported so callers can
// refuse to allow.
func hookGuard(cfgFile string) (guard.Guard, error) {
	home, err := config.Home()
	if err != nil {
		return guard.Guard{}, err
	}
	var cfg config.Config
	var cfgErr error
	if cfgFile != "" {
		cfg, cfgErr = config.Read(cfgFile)
	} else {
		cfg, cfgErr = config.Load()
	}
	protected := config.DefaultProtectedBranches()
	if cfgErr == nil && len(cfg.ProtectedBranches) > 0 {
		protected = cfg.ProtectedBranches
	}
	g := guard.Guard{
		Protected: protected, Git: guard.GitCLI{}, Home: home, GitHooks: cfgErr != nil || cfg.GitHooksEnabled(),
		Names: append([]string{cfg.CommandName()}, cfg.Aliases...),
		Dirs:  config.DirsFor(home, config.Slug),
	}
	return g, cfgErr
}

// guardGit is the pre-tool layer. Any failure becomes "ask": a hook that errors or prints
// nothing lets the tool call through.
func (h hookEnv) guardGit() {
	var d guard.Decision
	cwd, cmd := "", ""
	home, _ := config.Home()
	defer func() {
		if r := recover(); r != nil {
			d = guard.Decision{Verdict: guard.Ask, Reason: fmt.Sprintf("guard crashed (%v); confirm this command manually", r)}
		}
		if d.Verdict == guard.Allow {
			return
		}
		if home != "" {
			guard.Log{Path: guard.DefaultLogPath(home)}.Record(h.now(), "pre-tool", d, cwd, cmd)
		}
		h.writeDecision(d)
	}()
	ctx, cancel := context.WithTimeout(context.Background(), hookDeadline)
	defer cancel()
	raw, err := io.ReadAll(io.LimitReader(h.stdin, 4<<20))
	if err != nil {
		d = guard.Decision{Verdict: guard.Ask, Reason: "guard could not read the hook input"}
		return
	}
	var in hookInput
	if err := json.Unmarshal(raw, &in); err != nil {
		d = guard.Decision{Verdict: guard.Ask, Reason: "guard received invalid hook input"}
		return
	}
	cwd, cmd = in.Cwd, in.ToolInput.Command
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	g, cfgErr := hookGuard("")
	if g.Git == nil {
		d = guard.Decision{Verdict: guard.Ask, Reason: "guard could not locate the home directory"}
		return
	}
	switch in.ToolName {
	case "", "Bash":
		d = g.Decide(ctx, cmd, cwd)
	case "PowerShell":
		d = g.DecidePowerShell(ctx, cmd, cwd)
	default:
		d = guard.Decision{Verdict: guard.Ask, Reason: "the guard does not know the " + in.ToolName + " tool; confirm this command manually"}
	}
	if cfgErr != nil && d.Verdict == guard.Allow && strings.Contains(strings.ToLower(cmd), "push") {
		d = guard.Decision{Verdict: guard.Ask, Reason: "squadrn config is invalid (" + cfgErr.Error() + "), so protected branches are uncertain"}
	}
}

func (h hookEnv) writeDecision(d guard.Decision) {
	var out hookOutput
	out.HookSpecificOutput.HookEventName = "PreToolUse"
	out.HookSpecificOutput.PermissionDecision = d.Verdict.String()
	out.HookSpecificOutput.PermissionDecisionReason = "squadrn guard: " + d.Reason
	_ = json.NewEncoder(h.stdout).Encode(out)
}

// git runs as git hook <name> (layer 2): pre-push is judged first, then the repository's own
// hook of that name runs with the same arguments and input. pre-push fails closed.
func (h hookEnv) git(cfgFile, name string, args []string) int {
	ctx, cancel := context.WithTimeout(context.Background(), hookDeadline*10)
	defer cancel()
	dir, _ := os.Getwd()
	home, err := config.Home()
	if err != nil {
		return h.gitFail(name, err)
	}
	ours := githook.Dir(release.DefaultLayout(home).DataDir)
	var input []byte
	if name == "pre-push" {
		if input, err = io.ReadAll(h.stdin); err != nil {
			return h.gitFail(name, err)
		}
		if code := h.prePush(ctx, cfgFile, dir, home, args, input); code != 0 {
			return code
		}
	}
	hook, err := githook.RepoHook(ctx, dir, ours, name)
	if err != nil {
		return h.gitFail(name, err)
	}
	if hook == "" {
		return exitOK
	}
	bin, argv := githook.HookCommand(hook, args)
	c := exec.Command(bin, argv...) // runs as long as the repository's hook needs, on the tty
	c.Stdout, c.Stderr = h.stdout, h.stderr
	c.Stdin = h.stdin
	if input != nil {
		c.Stdin = bytes.NewReader(input)
	}
	if err := c.Run(); err != nil {
		if code := proc.ExitCode(err); code > 0 {
			return code
		}
		return h.gitFail(name, err)
	}
	return exitOK
}

// prePush judges the exact ref updates. args are <remote name> <remote url>.
func (h hookEnv) prePush(ctx context.Context, cfgFile, dir, home string, args []string, input []byte) int {
	var d guard.Decision
	func() {
		defer func() {
			if r := recover(); r != nil {
				d = guard.Decision{Verdict: guard.Deny, Reason: fmt.Sprintf("guard crashed (%v)", r)}
			}
		}()
		updates, err := guard.ParsePrePush(bytes.NewReader(input))
		if err != nil {
			d = guard.Decision{Verdict: guard.Deny, Reason: err.Error()}
			return
		}
		g, cfgErr := hookGuard(cfgFile)
		if cfgErr != nil {
			d = guard.Decision{Verdict: guard.Deny, Reason: "squadrn config is invalid: " + cfgErr.Error()}
			return
		}
		d = g.DecidePrePush(ctx, dir, updates)
	}()
	if d.Verdict == guard.Allow {
		return exitOK
	}
	remote := strings.Join(args, " ")
	guard.Log{Path: guard.DefaultLogPath(home)}.Record(h.now(), "pre-push", d, dir, "git push "+remote+" ("+strings.TrimSpace(strings.ReplaceAll(string(input), "\n", "; "))+")")
	_, _ = fmt.Fprintf(h.stderr, "squadrn guard: %s\nPush a feature branch and open a pull request instead.\n", d.Reason)
	return exitError
}

func (h hookEnv) gitFail(name string, err error) int {
	_, _ = fmt.Fprintf(h.stderr, "squadrn guard: %s hook failed: %v\n", name, err)
	return exitError
}

// notify shows a desktop notification titled "Claude: <title>" with the project name.
// It is fire-and-forget: a missing notifier is not an error.
func (h hookEnv) notify(title string) {
	raw, _ := io.ReadAll(io.LimitReader(h.stdin, 4<<20))
	var in hookInput
	_ = json.Unmarshal(raw, &in)
	project := in.Cwd
	if project == "" {
		project, _ = os.Getwd()
	}
	argv := notifyCommand(runtime.GOOS, "Claude: "+title, filepath.Base(project))
	if argv == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _ = proc.Output(ctx, proc.Options{}, argv[0], argv[1:]...)
}

// notifyCommand passes text as arguments, never as script source, so titles cannot inject code.
func notifyCommand(goos, title, body string) []string {
	switch goos {
	case "darwin":
		return []string{
			"osascript",
			"-e", "on run argv",
			"-e", `display notification (item 2 of argv) with title (item 1 of argv) sound name "Glass"`,
			"-e", "end run",
			title, body,
		}
	case "linux":
		if path, err := exec.LookPath("notify-send"); err == nil {
			return []string{path, "--app-name=Claude", title, body}
		}
	}
	return nil
}
