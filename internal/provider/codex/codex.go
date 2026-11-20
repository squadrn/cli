// Package codex wraps the OpenAI Codex CLI. It is the only package that knows Codex's
// environment and on-disk conventions (CODEX_HOME, config.toml, hooks.json, the app server).
package codex

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/squadrn/squadrn/internal/proc"
	"github.com/squadrn/squadrn/internal/provider"
)

// CLI runs the installed codex binary.
type CLI struct {
	Bin string
	// Home is the user's home dir: the main account is Home/.codex.
	Home string
	// ExtraEnv is appended to every invocation (the git-level guard's GIT_CONFIG_* entries).
	ExtraEnv []string
}

// Find locates codex on PATH (platform: lookPath).
func Find() (CLI, error) {
	bin, err := lookPath()
	if err != nil {
		return CLI{}, err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return CLI{}, err
	}
	return CLI{Bin: bin, Home: home}, nil
}

var errNotInstalled = errors.New("codex is not installed or not on PATH (" + provider.Codex.InstallHint + ")")

// dir is the CODEX_HOME of configDir; "" is the main account.
func (c CLI) dir(configDir string) string {
	if configDir == "" {
		return filepath.Join(c.Home, provider.Codex.MainDir)
	}
	return configDir
}

// env is environ with CODEX_HOME set to configDir (removed for the main account, which uses
// Codex's default ~/.codex) and ExtraEnv applied; ExtraEnv entries replace inherited ones,
// because syscall.Exec, unlike os/exec, does not drop duplicate keys.
func (c CLI) env(configDir string) []string {
	key := provider.Codex.ConfigEnv
	override := map[string]bool{key: true}
	for _, kv := range c.ExtraEnv {
		k, _, _ := strings.Cut(kv, "=")
		override[k] = true
	}
	environ := os.Environ()
	out := make([]string, 0, len(environ)+len(c.ExtraEnv)+1)
	for _, kv := range environ {
		if k, _, _ := strings.Cut(kv, "="); !override[k] {
			out = append(out, kv)
		}
	}
	if configDir != "" {
		out = append(out, key+"="+configDir)
	}
	return append(out, c.ExtraEnv...)
}

// prepare makes sure an account keeps its credentials in its own dir before codex runs there.
func (c CLI) prepare(configDir string) error {
	if configDir == "" {
		return nil
	}
	return EnsureFileStore(filepath.Join(configDir, provider.Codex.Settings))
}

func (c CLI) Output(ctx context.Context, configDir string, args ...string) ([]byte, error) {
	if err := c.prepare(configDir); err != nil {
		return nil, err
	}
	out, err := proc.Output(ctx, proc.Options{Env: c.env(configDir)}, c.Bin, args...)
	if err != nil {
		return out, fmt.Errorf("codex %s: %w", strings.Join(args, " "), err)
	}
	return out, nil
}

// Run runs an interactive codex command as a child and returns when it ends (platform:
// runChild); a non-zero exit is an ExitStatus.
func (c CLI) Run(configDir string, args []string) error {
	if err := c.prepare(configDir); err != nil {
		return err
	}
	argv := append([]string{"codex"}, args...)
	if err := runChild(c.Bin, argv, c.env(configDir)); err != nil {
		var es provider.ExitStatus
		if errors.As(err, &es) {
			return es
		}
		return fmt.Errorf("run %s: %w", c.Bin, err)
	}
	return nil
}

// Exec runs an interactive codex session in place of squadrn (platform: run).
func (c CLI) Exec(configDir string, args []string) error {
	if err := c.prepare(configDir); err != nil {
		return err
	}
	argv := append([]string{"codex"}, args...)
	if err := run(c.Bin, argv, c.env(configDir)); err != nil {
		var es provider.ExitStatus
		if errors.As(err, &es) {
			return es
		}
		return fmt.Errorf("exec %s: %w", c.Bin, err)
	}
	return nil
}

func (CLI) LoginArgs() []string  { return []string{"login"} }
func (CLI) LogoutArgs() []string { return []string{"logout"} }
