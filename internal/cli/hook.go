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

// maxHookInput bounds what a hook reads from the agent CLI.
const maxHookInput = 4 << 20

// readInput reads the JSON the agent CLI passes a hook on stdin.
func (h hookEnv) readInput() ([]byte, error) {
	return io.ReadAll(io.LimitReader(h.stdin, maxHookInput))
}

// hookUsage describes the hook subcommands under the user's command name; the plugin (through
// squadrn-hook) and the git hook shims run them through the installed binary whatever it is called.
func hookUsage() string {
	cfg, _ := config.Load()
	return "usage: " + cfg.CommandName() + " hook guard-git [--no-ask] | notify | git [--config <file>] <hook-name> [args...] | statusline [--then <command>] | limit | config-change | deliver <payload>\n" +
		"internal: the " + config.Slug + " plugin (squadrn-hook) and git hooks run this through the installed binary"
}

// runHook handles `squadrn hook …` and returns the exit code.
func runHook(args []string, h hookEnv) int {
	if len(args) == 0 {
		_, _ = fmt.Fprintln(h.stderr, hookUsage())
		return exitUsage
	}
	if len(args) == 2 && (args[1] == "-h" || args[1] == "--help") {
		_, _ = fmt.Fprintln(h.stdout, hookUsage())
		return exitOK
	}
	switch args[0] {
	case "guard-git":
		noAsk := len(args) == 2 && args[1] == "--no-ask"
		if len(args) != 1 && !noAsk {
			// Exit 2 blocks the tool call in Claude Code and Codex, so a bad invocation still
			// fails closed.
			_, _ = fmt.Fprintln(h.stderr, hookUsage())
			return exitUsage
		}
		h.guardGit(noAsk)
		return exitOK
	case "notify":
		// The rc.3 plugin passed a title (notify 'Done'); the event now comes from the input.
		if len(args) > 2 {
			_, _ = fmt.Fprintln(h.stderr, hookUsage())
			return exitUsage
		}
		raw, _ := h.readInput()
		h.notifyEvent(hookSpec(), raw)
		return exitOK
	case "deliver":
		if len(args) != 2 {
			_, _ = fmt.Fprintln(h.stderr, hookUsage())
			return exitUsage
		}
		return h.deliver(args[1])
	case "git":
		cfgFile := ""
		if len(args) > 2 && args[1] == githook.ConfigFlag {
			cfgFile, args = args[2], args[2:]
		}
		if len(args) < 2 {
			_, _ = fmt.Fprintln(h.stderr, hookUsage())
			return exitUsage
		}
		return h.git(cfgFile, args[1], args[2:])
	case "statusline":
		return h.statusline(args[1:])
	case "limit":
		if len(args) != 1 {
			_, _ = fmt.Fprintln(h.stderr, hookUsage())
			return exitUsage
		}
		return h.limit()
	case "config-change":
		if len(args) != 1 {
			_, _ = fmt.Fprintln(h.stderr, hookUsage())
			return exitUsage
		}
		h.configChange()
		return exitOK
	case "-h", "--help", "help":
		_, _ = fmt.Fprintln(h.stdout, hookUsage())
		return exitOK
	}
	_, _ = fmt.Fprintln(h.stderr, hookUsage())
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
// nothing lets the tool call through. noAsk is for a CLI whose hooks cannot ask (Codex: "ask"
// lets the call through), where asking becomes denying.
func (h hookEnv) guardGit(noAsk bool) {
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
		if noAsk && d.Verdict == guard.Ask {
			d = guard.Decision{Verdict: guard.Deny, Reason: d.Reason + " (this agent cannot ask for confirmation: run the command yourself if it is intended)"}
		}
		if home != "" {
			guard.Log{Path: guard.DefaultLogPath(home)}.Record(h.now(), "pre-tool", d, cwd, cmd)
		}
		h.writeDecision(d)
	}()
	ctx, cancel := context.WithTimeout(context.Background(), hookDeadline)
	defer cancel()
	raw, err := h.readInput()
	if err != nil {
		d = guard.Decision{Verdict: guard.Ask, Reason: "guard cannot read the hook input"}
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
		d = guard.Decision{Verdict: guard.Ask, Reason: "guard cannot locate the home directory"}
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
	guard.Log{Path: guard.DefaultLogPath(home)}.Record(h.now(), "pre-push", d, dir, prePushCommand(args, input))
	_, _ = fmt.Fprintf(h.stderr, "squadrn guard: %s\n", d.Reason)
	if !strings.Contains(d.Reason, "open a PR") {
		_, _ = fmt.Fprintln(h.stderr, "Push a feature branch and open a pull request instead.")
	}
	return exitError
}

// prePushCommand describes a refused push for the guard log as the git push it amounts to:
// git push <remote> <local ref>:<remote ref>... (<url>), with :<remote ref> for a deletion.
func prePushCommand(args []string, input []byte) string {
	cmd := "git push"
	if len(args) > 0 {
		cmd += " " + args[0]
	}
	updates, err := guard.ParsePrePush(bytes.NewReader(input))
	if err != nil {
		return cmd + " (" + strings.TrimSpace(strings.ReplaceAll(string(input), "\n", "; ")) + ")"
	}
	for _, u := range updates {
		if strings.Trim(u.LocalSHA, "0") == "" {
			cmd += " :" + u.RemoteRef
		} else {
			cmd += " " + u.LocalRef + ":" + u.RemoteRef
		}
	}
	if len(args) > 1 && args[1] != args[0] {
		cmd += " (" + args[1] + ")"
	}
	return cmd
}

func (h hookEnv) gitFail(name string, err error) int {
	_, _ = fmt.Fprintf(h.stderr, "squadrn guard: %s hook failed: %v\n", name, err)
	return exitError
}

// configChangeLog is the audit log of the strict profile's ConfigChange hook.
const configChangeLog = "config-changes.log"

// configChange records a settings change the agent CLI reports (strict profile): one JSON line
// with the time, the config dir, the source and the file. It never blocks the change.
func (h hookEnv) configChange() {
	raw, _ := h.readInput()
	var in struct {
		Source   string `json:"source"`
		FilePath string `json:"file_path"`
	}
	_ = json.Unmarshal(raw, &in)
	home, err := config.Home()
	if err != nil {
		return
	}
	dir := config.DirsFor(home, config.Slug).State
	if os.MkdirAll(dir, 0o700) != nil {
		return
	}
	line, _ := json.Marshal(map[string]string{
		"at": h.now().UTC().Format(time.RFC3339), "config_dir": os.Getenv(hookSpec().ConfigEnv),
		"source": in.Source, "file": in.FilePath,
	})
	f, err := os.OpenFile(filepath.Join(dir, configChangeLog), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer func() { _ = f.Close() }()
	_, _ = f.Write(append(line, '\n'))
}
