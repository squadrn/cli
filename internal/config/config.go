// Package config owns sprawl's settings file and the directories sprawl uses on disk.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"

	"sprawling-agents/internal/fsx"
	"sprawling-agents/internal/provider"
)

// Slug names every fixed on-disk identifier: dirs, env vars, plugin id, git config keys.
const Slug = "sprawl"

// DefaultCommand and DefaultAliases are installed when the user picks no names.
const DefaultCommand = "sprawl"

func DefaultAliases() []string { return []string{"sra"} }

// PreviousNames are earlier product names, newest first. Their config, data dirs, plugins and
// git keys are migrated or still honoured; every legacy identifier derives from this list.
var PreviousNames = []string{"squad"}

// DefaultProtectedBranches applies when the config file names none.
func DefaultProtectedBranches() []string { return []string{"main", "master", "develop", "release/*"} }

// schemaVersion is written to every saved config; newer files are refused rather than misread.
const schemaVersion = 1

type Config struct {
	Schema int `toml:"schema,omitempty"`
	// Command is the name sprawl is installed under on PATH; Aliases are extra names.
	Command string   `toml:"command,omitempty"`
	Aliases []string `toml:"aliases,omitempty"`
	// DefaultProvider resolves account refs without a provider (personal → claude/personal).
	DefaultProvider string `toml:"default_provider,omitempty"`
	// SharedMCP lists user-scope MCP servers copied from a provider's main account into new accounts.
	SharedMCP []string `toml:"shared_mcp,omitempty"`
	// ProtectedBranches are branch globs agents must never push to directly. Repositories can only
	// add to this list (git config sprawl.protectedBranches), never replace it.
	ProtectedBranches []string `toml:"protected_branches,omitempty"`
	// GitHooks enables the git-level guard (pre-push) for agent sessions sprawl starts.
	GitHooks *bool `toml:"git_hooks,omitempty"`
	// UpdateCheck enables the daily background check for new releases (a notice, never forced).
	UpdateCheck *bool `toml:"update_check,omitempty"`
	// UpdateChannel is "stable" (default) or "beta" (also pre-releases).
	UpdateChannel string `toml:"update_channel,omitempty"`
}

// CommandName is the configured command, or DefaultCommand.
func (c Config) CommandName() string {
	if c.Command == "" {
		return DefaultCommand
	}
	return c.Command
}

// GitHooksEnabled reports whether the git-level guard is on (the default).
func (c Config) GitHooksEnabled() bool { return c.GitHooks == nil || *c.GitHooks }

func (c Config) UpdateCheckEnabled() bool { return c.UpdateCheck == nil || *c.UpdateCheck }

// Channel is the update channel, "stable" by default.
func (c Config) Channel() string {
	if c.UpdateChannel == "" {
		return "stable"
	}
	return c.UpdateChannel
}

// Dirs are the XDG base directories of one product name, resolved the same way on Linux and
// macOS (macOS conventions such as ~/Library/Caches are deliberately not used).
type Dirs struct {
	Config string // $XDG_CONFIG_HOME/<name>  (~/.config/<name>)
	Data   string // $XDG_DATA_HOME/<name>    (~/.local/share/<name>)
	State  string // $XDG_STATE_HOME/<name>   (~/.local/state/<name>)
	Cache  string // $XDG_CACHE_HOME/<name>   (~/.cache/<name>)
}

func xdg(env, home, fallback string) string {
	if v := os.Getenv(env); filepath.IsAbs(v) {
		return v
	}
	return filepath.Join(home, fallback)
}

// DirsFor resolves the directories of name for the given home.
func DirsFor(home, name string) Dirs {
	return Dirs{
		Config: filepath.Join(xdg("XDG_CONFIG_HOME", home, ".config"), name),
		Data:   filepath.Join(xdg("XDG_DATA_HOME", home, ".local/share"), name),
		State:  filepath.Join(xdg("XDG_STATE_HOME", home, ".local/state"), name),
		Cache:  filepath.Join(xdg("XDG_CACHE_HOME", home, ".cache"), name),
	}
}

// Home returns the user's home directory.
func Home() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("locate home directory: %w", err)
	}
	return home, nil
}

// Path returns the config file location.
func Path() (string, error) {
	home, err := Home()
	if err != nil {
		return "", err
	}
	return filepath.Join(DirsFor(home, Slug).Config, "config.toml"), nil
}

// LegacyPaths are the config files of previous product names, newest first.
func LegacyPaths() ([]string, error) {
	home, err := Home()
	if err != nil {
		return nil, err
	}
	paths := make([]string, len(PreviousNames))
	for i, n := range PreviousNames {
		paths[i] = filepath.Join(DirsFor(home, n).Config, "config.toml")
	}
	return paths, nil
}

// Load reads the config (a missing file yields defaults) and applies SPRAWL_SHARED_MCP.
// Unknown keys and invalid values are errors so mistakes never silently change behaviour.
func Load() (Config, error) {
	path, err := Path()
	if err != nil {
		return Config{}, err
	}
	return load(path)
}

func load(path string) (Config, error) {
	c, err := Read(path)
	if err != nil {
		return Config{}, err
	}
	// An empty variable counts as unset, so a stray `export SPRAWL_SHARED_MCP=` cannot wipe the list.
	if v := os.Getenv("SPRAWL_SHARED_MCP"); strings.TrimSpace(v) != "" {
		c.SharedMCP = strings.Fields(v)
	}
	if len(c.ProtectedBranches) == 0 {
		c.ProtectedBranches = DefaultProtectedBranches()
	}
	if c.DefaultProvider == "" {
		c.DefaultProvider = provider.Default
	}
	return c, nil
}

// EnvOverrides maps the settings an environment variable currently overrides to that variable.
func EnvOverrides() map[string]string {
	out := map[string]string{}
	if strings.TrimSpace(os.Getenv("SPRAWL_SHARED_MCP")) != "" {
		out["shared_mcp"] = "SPRAWL_SHARED_MCP"
	}
	return out
}

// Read parses one config file as written, without defaults or environment overrides.
func Read(path string) (Config, error) {
	var c Config
	md, err := toml.DecodeFile(path, &c)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return c, nil
	case err != nil:
		return Config{}, fmt.Errorf("config %s: %w", path, err)
	}
	if keys := md.Undecoded(); len(keys) > 0 {
		return Config{}, fmt.Errorf("config %s: unknown key %q", path, keys[0].String())
	}
	if c.Schema > schemaVersion {
		return Config{}, fmt.Errorf("config %s: schema %d is newer than this sprawl understands (%d); upgrade sprawl", path, c.Schema, schemaVersion)
	}
	if err := c.validate(); err != nil {
		return Config{}, fmt.Errorf("config %s: %w", path, err)
	}
	return c, nil
}

func (c Config) validate() error {
	if c.Command != "" {
		if err := ValidateCommand(c.Command); err != nil {
			return fmt.Errorf("command: %w", err)
		}
	}
	for _, a := range c.Aliases {
		if err := ValidateCommand(a); err != nil {
			return fmt.Errorf("aliases: %w", err)
		}
	}
	if c.DefaultProvider != "" {
		if _, ok := provider.Get(c.DefaultProvider); !ok {
			return fmt.Errorf("%w: unknown default_provider %q", ErrInvalidValue, c.DefaultProvider)
		}
	}
	if c.UpdateChannel != "" && c.UpdateChannel != "stable" && c.UpdateChannel != "beta" {
		return fmt.Errorf("%w: update_channel %q is not stable or beta", ErrInvalidValue, c.UpdateChannel)
	}
	for _, b := range c.ProtectedBranches {
		if strings.TrimSpace(b) == "" {
			return fmt.Errorf("%w: protected_branches has an empty entry", ErrInvalidValue)
		}
	}
	return nil
}

// Save writes c to path atomically, stamped with the current schema version.
func Save(path string, c Config) error {
	c.Schema = schemaVersion
	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Encode(c); err != nil {
		return err
	}
	return fsx.WriteBytes(path, buf.Bytes(), 0o644)
}

// Keys lists the settings `config get|set` accepts.
var Keys = []string{"command", "aliases", "default_provider", "shared_mcp", "protected_branches", "git_hooks", "update_check", "update_channel"}

var (
	ErrUnknownKey   = errors.New("unknown config key")
	ErrInvalidValue = errors.New("invalid config value")
)

// Get renders one setting of c (with defaults applied) as text.
func (c Config) Get(key string) (string, error) {
	switch key {
	case "command":
		return c.CommandName(), nil
	case "aliases":
		return strings.Join(c.Aliases, ","), nil
	case "default_provider":
		return c.DefaultProvider, nil
	case "shared_mcp":
		return strings.Join(c.SharedMCP, ","), nil
	case "protected_branches":
		return strings.Join(c.ProtectedBranches, ","), nil
	case "git_hooks":
		return strconv.FormatBool(c.GitHooksEnabled()), nil
	case "update_check":
		return strconv.FormatBool(c.UpdateCheckEnabled()), nil
	case "update_channel":
		return c.Channel(), nil
	}
	return "", fmt.Errorf("%w %q (known: %s)", ErrUnknownKey, key, strings.Join(Keys, ", "))
}

// Set parses value into key. Lists are comma or space separated; "" clears them.
func (c *Config) Set(key, value string) error {
	list := func() []string {
		return strings.FieldsFunc(value, func(r rune) bool { return r == ',' || r == ' ' })
	}
	switch key {
	case "command":
		c.Command = value
	case "aliases":
		c.Aliases = list()
	case "default_provider":
		c.DefaultProvider = value
	case "shared_mcp":
		c.SharedMCP = list()
	case "protected_branches":
		c.ProtectedBranches = list()
	case "git_hooks", "update_check":
		b, err := strconv.ParseBool(value)
		if err != nil {
			return fmt.Errorf("%w: %s %q is not true or false", ErrInvalidValue, key, value)
		}
		if key == "git_hooks" {
			c.GitHooks = &b
		} else {
			c.UpdateCheck = &b
		}
	case "update_channel":
		c.UpdateChannel = value
	default:
		return fmt.Errorf("%w %q (known: %s)", ErrUnknownKey, key, strings.Join(Keys, ", "))
	}
	return c.validate()
}

var ErrInvalidCommand = errors.New("invalid command name")

var commandPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,15}$`)

// unusableCommands are shell builtins/keywords and tools sprawl relies on.
var unusableCommands = []string{
	"alias", "bg", "bind", "break", "builtin", "case", "cd", "command", "continue", "declare", "do",
	"done", "echo", "elif", "else", "esac", "eval", "exec", "exit", "export", "false", "fc", "fg", "fi",
	"for", "function", "getopts", "hash", "if", "in", "jobs", "kill", "let", "local", "printf", "pwd",
	"read", "readonly", "return", "select", "set", "shift", "source", "test", "then", "time", "trap",
	"true", "type", "typeset", "ulimit", "umask", "unalias", "unset", "until", "wait", "while",
	"claude", "codex", "gemini", "opencode", "git", "tmux", "gh", "sa",
}

// ValidateCommand checks a command or alias name. Collisions with other programs on PATH are
// checked at install time, where the user can confirm them.
func ValidateCommand(name string) error {
	if !commandPattern.MatchString(name) {
		return fmt.Errorf("%w %q (expected %s)", ErrInvalidCommand, name, commandPattern)
	}
	if slices.Contains(unusableCommands, name) {
		return fmt.Errorf("%w %q: it is a shell builtin or a tool sprawl relies on", ErrInvalidCommand, name)
	}
	return nil
}
