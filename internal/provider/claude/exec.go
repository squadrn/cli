// Package claude wraps the claude CLI. It is the only package that knows Claude Code's
// environment and on-disk conventions, so a change there is absorbed here.
package claude

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"sprawling-agents/internal/proc"
	"sprawling-agents/internal/provider"
)

const configDirEnv = "CLAUDE_CONFIG_DIR"

var _ provider.Agent = CLI{}

// CLI runs the installed claude binary.
type CLI struct {
	Bin string
	// ExtraEnv is appended to every invocation (e.g. the git-level guard's GIT_CONFIG_* entries).
	ExtraEnv []string
}

// Find locates claude on PATH.
func Find() (CLI, error) {
	bin, err := exec.LookPath("claude")
	if err != nil {
		return CLI{}, errors.New("claude is not installed or not on PATH (https://claude.ai/install.sh)")
	}
	return CLI{Bin: bin}, nil
}

// Env returns environ with CLAUDE_CONFIG_DIR set to configDir, or removed when configDir is "".
// The default account must run with the variable unset: Claude Code keys its credential
// store to the variable, so an explicit ~/.claude reads a different (empty) login.
func Env(environ []string, configDir string) []string {
	out := make([]string, 0, len(environ)+1)
	for _, kv := range environ {
		if !strings.HasPrefix(kv, configDirEnv+"=") {
			out = append(out, kv)
		}
	}
	if configDir != "" {
		out = append(out, configDirEnv+"="+configDir)
	}
	return out
}

func (c CLI) Output(ctx context.Context, configDir string, args ...string) ([]byte, error) {
	out, err := proc.Output(ctx, proc.Options{Env: c.env(configDir)}, c.Bin, args...)
	if err != nil {
		return out, fmt.Errorf("claude %s: %w", strings.Join(args, " "), err)
	}
	return out, nil
}

// env is environ for configDir with ExtraEnv applied; ExtraEnv entries replace inherited ones,
// because syscall.Exec, unlike os/exec, does not drop duplicate keys.
func (c CLI) env(configDir string) []string {
	base := Env(os.Environ(), configDir)
	if len(c.ExtraEnv) == 0 {
		return base
	}
	override := map[string]bool{}
	for _, kv := range c.ExtraEnv {
		k, _, _ := strings.Cut(kv, "=")
		override[k] = true
	}
	out := make([]string, 0, len(base)+len(c.ExtraEnv))
	for _, kv := range base {
		if k, _, _ := strings.Cut(kv, "="); !override[k] {
			out = append(out, kv)
		}
	}
	return append(out, c.ExtraEnv...)
}

func (c CLI) Auth(ctx context.Context, configDir string) (provider.AuthStatus, error) {
	return Auth(ctx, c, configDir)
}

func (CLI) LoginArgs() []string  { return []string{"auth", "login"} }
func (CLI) LogoutArgs() []string { return []string{"auth", "logout"} }

func (CLI) WorktreeArgs(name string) []string { return []string{"--worktree", name} }

func (CLI) ResumeArgs(session string) []string { return []string{"--resume", session} }

// Exec runs an interactive claude session in place of sprawl (platform: run).
func (c CLI) Exec(configDir string, args []string) error {
	argv := append([]string{"claude"}, args...)
	if err := run(c.Bin, argv, c.env(configDir)); err != nil {
		return fmt.Errorf("exec %s: %w", c.Bin, err)
	}
	return nil
}
