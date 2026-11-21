// Package gemini wraps Google's Gemini CLI. It is the only package that knows Gemini's
// environment and on-disk conventions (GEMINI_CLI_HOME, <home>/.gemini, settings.json with
// comments, oauth_creds.json), ADR 0013.
package gemini

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/squadrn/squadrn-cli/internal/fsx"
	"github.com/squadrn/squadrn-cli/internal/proc"
	"github.com/squadrn/squadrn-cli/internal/provider"
)

// encryptedStorageEnv makes Gemini keep Google tokens in a keychain entry every home shares;
// squadrn removes it so tokens stay in the account's own oauth_creds.json.
const encryptedStorageEnv = "GEMINI_FORCE_ENCRYPTED_FILE_STORAGE"

// CLI runs the installed gemini.
type CLI struct {
	// Bin is the program to run and Prefix its first arguments: gemini itself, or on Windows
	// node with the package's bundle/gemini.js (gemini.cmd would let cmd.exe re-parse prompts).
	Bin    string
	Prefix []string
	// Home is the user's home dir: the main account is Home/.gemini.
	Home string
	// ExtraEnv is appended to every invocation (the git-level guard's GIT_CONFIG_* entries).
	ExtraEnv []string
}

// Find locates gemini on PATH (platform: lookPath).
func Find() (CLI, error) {
	bin, prefix, err := lookPath()
	if err != nil {
		return CLI{}, err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return CLI{}, err
	}
	return CLI{Bin: bin, Prefix: prefix, Home: home}, nil
}

var errNotInstalled = errors.New("gemini is not installed or not on PATH (" + provider.Gemini.InstallHint + ")")

// state is the CLI's state dir for configDir (a GEMINI_CLI_HOME; "" is the main account).
func (c CLI) state(configDir string) string {
	if configDir == "" {
		return filepath.Join(c.Home, provider.Gemini.MainDir)
	}
	return filepath.Join(configDir, provider.Gemini.StateDir)
}

// env is environ with GEMINI_CLI_HOME set to configDir (removed for the main account, which
// uses ~/.gemini), the encrypted-storage switch removed, and ExtraEnv applied; ExtraEnv entries
// replace inherited ones, because syscall.Exec, unlike os/exec, does not drop duplicate keys.
func (c CLI) env(configDir string) []string {
	key := provider.Gemini.ConfigEnv
	drop := map[string]bool{key: true, encryptedStorageEnv: true}
	for _, kv := range c.ExtraEnv {
		k, _, _ := strings.Cut(kv, "=")
		drop[k] = true
	}
	environ := os.Environ()
	out := make([]string, 0, len(environ)+len(c.ExtraEnv)+1)
	for _, kv := range environ {
		if k, _, _ := strings.Cut(kv, "="); !drop[k] {
			out = append(out, kv)
		}
	}
	if configDir != "" {
		out = append(out, key+"="+configDir)
	}
	return append(out, c.ExtraEnv...)
}

func (c CLI) args(args []string) []string {
	return append(append([]string(nil), c.Prefix...), args...)
}

func (c CLI) Output(ctx context.Context, configDir string, args ...string) ([]byte, error) {
	out, err := proc.Output(ctx, proc.Options{Env: c.env(configDir)}, c.Bin, c.args(args)...)
	if err != nil {
		return out, fmt.Errorf("gemini %s: %w", strings.Join(args, " "), err)
	}
	return out, nil
}

// Run runs an interactive gemini as a child and returns when it ends (platform: runChild); a
// non-zero exit is an ExitStatus.
func (c CLI) Run(configDir string, args []string) error {
	if err := runChild(c.Bin, c.argv(c.withPolicy(configDir, args)), c.env(configDir)); err != nil {
		var es provider.ExitStatus
		if errors.As(err, &es) {
			return es
		}
		return fmt.Errorf("run %s: %w", c.Bin, err)
	}
	return nil
}

// Exec runs an interactive gemini session in place of squadrn (platform: run).
func (c CLI) Exec(configDir string, args []string) error {
	if err := run(c.Bin, c.argv(c.withPolicy(configDir, args)), c.env(configDir)); err != nil {
		var es provider.ExitStatus
		if errors.As(err, &es) {
			return es
		}
		return fmt.Errorf("exec %s: %w", c.Bin, err)
	}
	return nil
}

func (c CLI) argv(args []string) []string {
	return append([]string{provider.Gemini.Binary}, c.args(args)...)
}

// withPolicy puts --policy <the profile's policy file> before args of a session (not of a
// subcommand such as mcp), when the account has one.
func (c CLI) withPolicy(configDir string, args []string) []string {
	if configDir == "" || (len(args) > 0 && slices.Contains(provider.Gemini.Subcommands, args[0])) {
		return args
	}
	p := filepath.Join(c.state(configDir), PolicyFile)
	if !fsx.IsFile(p) {
		return args
	}
	return append([]string{"--policy", p}, args...)
}

// WorktreeArgs: `gemini --worktree <name>` creates .gemini/worktrees/<name> on branch <name>
// (experimental.worktrees, set by every profile).
func (CLI) WorktreeArgs(name string) []string { return []string{"--worktree", name} }

// LoginArgs: Gemini has no login command. The login is a session (the first run asks how to
// sign in); --skip-trust keeps the folder-trust dialog out of it, for this run only.
func (CLI) LoginArgs() []string { return []string{"--skip-trust"} }

// LogoutArgs: none; logging out moves the credential files to the trash (LogoutFiles).
func (CLI) LogoutArgs() []string { return nil }

// LoginHint is printed before the login session, which does not end by itself.
func (CLI) LoginHint() string {
	return "finish the Google login in Gemini CLI, then type /quit (over SSH: NO_BROWSER=true prints a URL instead)"
}

// LogoutFiles are the account's credential files.
func (c CLI) LogoutFiles(configDir string) []string {
	out := make([]string, len(provider.Gemini.Credentials))
	for i, f := range provider.Gemini.Credentials {
		out[i] = filepath.Join(c.state(configDir), f)
	}
	return out
}
