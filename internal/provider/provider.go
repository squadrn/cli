// Package provider describes the agent CLIs squadrn drives. What differs between providers is
// data in Spec; what needs code is behind Agent and the optional capability interfaces, which
// each provider's own package implements and registers (internal/provider/claude), ADR 0011.
package provider

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/squadrn/squadrn/internal/usage"
)

type Caps struct {
	// Worktree: the CLI creates git worktrees itself (claude --worktree).
	Worktree bool
	// SessionTransfer: conversations can be copied between accounts and resumed (swap).
	SessionTransfer bool
	// Plugins: the squadrn plugin (git push guard, notifications) can be installed.
	Plugins bool
	// SharedMCP: user-scope MCP servers can be copied from the main account.
	SharedMCP bool
	// UsageReport: the CLI reports usage limits to squadrn (status line, limit hook).
	UsageReport bool
}

type Spec struct {
	ID     string // "claude"; the provider part of an account ref (claude/personal)
	Name   string // product name for messages
	Binary string // CLI name on PATH
	// MainDir is the main account's config dir relative to home; other accounts live in
	// DirPrefix+<name>, e.g. ~/.claude and ~/.claude-personal.
	MainDir   string
	DirPrefix string
	// ConfigEnv selects an account's config dir. It must be unset for the main account.
	ConfigEnv string
	// MCPConfig is the main account's file holding user-scope MCP servers, relative to home.
	MCPConfig string
	// Settings is the main account's settings file, relative to MainDir.
	Settings string
	// Shared are linked from the main account into every other account. SharedPerEntry dirs are
	// linked entry by entry, skipping the listed entries (managed per account by the CLI).
	Shared         []string
	SharedPerEntry map[string][]string
	// WorktreeDir is where the CLI creates worktrees, relative to the repository root.
	WorktreeDir string
	// Subcommands of the agent CLI: `squadrn [<account>] <subcommand>` passes them through. They
	// are valid account names: an account of that name is matched first (ADR 0011).
	Subcommands []string
	// SessionID is the form of a conversation's ID (Caps.SessionTransfer).
	SessionID *regexp.Regexp
	// Plugin is the squadrn plugin's ID in the CLI.
	Plugin string
	// InstallHint installs the CLI; NativeMarker is a path fragment of its self-updating build.
	InstallHint  string
	NativeMarker string
	Caps         Caps
}

// AuthStatus is an account's login as the CLI reports it.
type AuthStatus struct {
	LoggedIn bool   `json:"loggedIn"`
	Email    string `json:"email"`
	OrgName  string `json:"orgName"`
	// SubscriptionType is the plan ("pro", "max", …); empty for API billing or older CLIs.
	SubscriptionType string `json:"subscriptionType,omitempty"`
	AuthMethod       string `json:"authMethod,omitempty"`
}

// Metered: logged in without a subscription, billed per use (no usage limits to track).
func (s AuthStatus) Metered() bool {
	m := strings.ToLower(s.AuthMethod)
	return s.LoggedIn && s.SubscriptionType == "" && (strings.Contains(m, "api") || strings.Contains(m, "key"))
}

// Session is a stored conversation. Owner is the index of the config dir holding it, in the list
// given to the lookup.
type Session struct {
	ID      string
	Path    string
	ModTime time.Time
	Owner   int
}

// LiveSession is a running session of the agent CLI; Owner as in Session.
type LiveSession struct {
	PID       int
	SessionID string
	Cwd       string
	Owner     int
}

var (
	ErrNoSession    = errors.New("no conversation found")
	ErrBadSessionID = errors.New("not a session ID (expected a UUID)")
	// ErrDiverged: the target account holds a version of the conversation that is not a prefix
	// of the source's, so copying would lose what was said there.
	ErrDiverged = errors.New("the conversation differs in the target account")
)

// ExitStatus is returned by Agent.Exec on platforms where the session runs as a child process
// (Windows): the agent CLI exited with Code, which squadrn passes on as its own exit status.
type ExitStatus struct{ Code int }

func (e ExitStatus) Error() string { return "exit status " + strconv.Itoa(e.Code) }

// Runner runs the CLI non-interactively. configDir "" selects the main account. A non-zero exit
// returns whatever stdout was produced together with the error.
type Runner interface {
	Output(ctx context.Context, configDir string, args ...string) ([]byte, error)
}

// Launcher starts the CLI.
type Launcher interface {
	Runner
	// Exec runs an interactive session in place of squadrn: on Unix it replaces the process; on
	// Windows it runs as a child and returns ExitStatus when the session ends.
	Exec(configDir string, args []string) error
	// Run runs an interactive command (a login) as a child and returns when it ends.
	Run(configDir string, args []string) error
}

// Authenticator reads and changes an account's login.
type Authenticator interface {
	Auth(ctx context.Context, configDir string) (AuthStatus, error)
	LoginArgs() []string
	LogoutArgs() []string
}

// Agent drives one provider's CLI: what every provider has. Everything else is an optional
// capability below, found with a type assertion.
type Agent interface {
	Launcher
	Authenticator
}

// Worktrees: the CLI creates git worktrees itself (Caps.Worktree).
type Worktrees interface {
	WorktreeArgs(name string) []string
}

// SessionStore finds stored conversations and moves them between accounts of one provider
// (Caps.SessionTransfer), looked up across the given config dirs.
type SessionStore interface {
	ResumeArgs(session string) []string
	FindSession(configDirs []string, id string) (Session, error)
	// LatestSessionIn is the most recent conversation started in dir.
	LatestSessionIn(configDirs []string, dir string) (Session, error)
	RunningSessions(configDirs []string, alive func(pid int) bool) []LiveSession
	// SessionCwd is the directory s was started in; "" when unknown.
	SessionCwd(s Session) string
	// TransferSession copies s into targetConfigDir so that account can resume it. Unless force,
	// a copy there must be a prefix of s (else ErrDiverged).
	TransferSession(s Session, targetConfigDir string, force bool) error
	// UnavailableModel is the model s uses when target's plan may not include it; "" otherwise.
	UnavailableModel(s Session, target AuthStatus) string
}

// GuardInstaller installs the squadrn plugin (Caps.Plugins): the layer-1 push guard and the
// notification hooks, from the local marketplace dir. InstallPlugin reports whether it
// installed it (false: it was there already).
type GuardInstaller interface {
	InstallPlugin(ctx context.Context, configDir, marketplace string) (bool, error)
	UpdatePlugin(ctx context.Context, configDir string) error
	RemovePlugin(ctx context.Context, configDir string) error
	PluginState(ctx context.Context, configDir string) (installed, enabled bool, err error)
	EnablePluginArgs() []string
}

// MCP manages user-scope MCP servers (Caps.SharedMCP); config is a server's JSON definition.
type MCP interface {
	HasMCP(ctx context.Context, configDir, name string) bool
	AddMCP(ctx context.Context, configDir, name string, config []byte) error
}

// MCPLister lists an account's user-scope MCP servers, each as a JSON definition in Claude Code's
// mcpServers form, for a CLI that keeps them elsewhere than a JSON file (Spec.MCPConfig "").
type MCPLister interface {
	ListMCP(ctx context.Context, configDir string) (map[string]json.RawMessage, error)
}

// CredentialStore checks that an account keeps its credentials in its own config dir (a CLI
// that could store them elsewhere, e.g. the OS keyring). nil means they are.
type CredentialStore interface {
	CheckCredentials(configDir string) error
}

// Relocator fixes absolute config-dir paths the CLI recorded, after the dir moved.
type Relocator interface {
	Relocate(oldDir, newDir string) error
}

// LimitHit is a turn that stopped on an error, as the CLI's hook reports it.
type LimitHit struct {
	Error   string
	Details string
	// Limited: the error is a usage limit, and Observation records it.
	Limited     bool
	Observation usage.Snapshot
}

// StatusLine is a CLI that runs a status line command with its usage limits on stdin
// (Caps.UsageReport). It needs no CLI binary: the hooks use it on every refresh.
type StatusLine interface {
	// Command is the status line command in a settings file; ok is false when there is none.
	Command(settingsPath string) (cmd string, ok bool, err error)
	// SetCommand sets it ("" removes the status line).
	SetCommand(settingsPath, cmd string) error
	// Wrap is a command running bin with hook, then old; Unwrap reverses it.
	Wrap(bin, hook, old, home string) (string, error)
	Unwrap(cmd, hook string) (old string, ours bool)
	// Shell is how the CLI runs a status line command.
	Shell(cmd string) (bin string, argv []string)
	// Parse reads the status line input; ParseLimit the hook input of a turn that failed.
	Parse(raw []byte, now time.Time) (usage.Snapshot, error)
	ParseLimit(raw []byte, now time.Time, loc *time.Location) (LimitHit, error)
}

// Driver is the code behind a Spec, registered by the provider's own package.
type Driver struct {
	// New finds the CLI and returns its driver; extraEnv is added to every invocation (the
	// git-level guard's GIT_CONFIG_* entries).
	New func(extraEnv []string) (Agent, error)
	// StatusLine is nil when the provider has none.
	StatusLine StatusLine
}

var Claude = Spec{
	ID:        "claude",
	Name:      "Claude Code",
	Binary:    "claude",
	MainDir:   ".claude",
	DirPrefix: ".claude-",
	ConfigEnv: "CLAUDE_CONFIG_DIR",
	MCPConfig: ".claude.json",
	Settings:  "settings.json",
	Shared:    []string{"settings.json", "CLAUDE.md", "agents", "hooks", "commands", "output-styles", "keybindings.json"},
	// skills/synced is managed per account by claude.ai.
	SharedPerEntry: map[string][]string{"skills": {"synced"}},
	WorktreeDir:    ".claude/worktrees",
	Subcommands: []string{
		"agents", "attach", "auth", "auto-mode", "doctor", "gateway", "import", "install", "logs", "mcp",
		"migrate-installer", "plugin", "plugins", "purge", "respawn", "setup-token", "update",
	},
	SessionID:    regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`),
	Plugin:       "squadrn@squadrn",
	InstallHint:  "curl -fsSL https://claude.ai/install.sh | bash",
	NativeMarker: "/.local/share/claude/versions/",
	Caps:         Caps{Worktree: true, SessionTransfer: true, Plugins: true, SharedMCP: true, UsageReport: true},
}

// Codex is the OpenAI Codex CLI. Accounts are CODEX_HOME dirs; credentials are kept in each one's
// auth.json (cli_auth_credentials_store = "file"), never in the OS keyring, so they stay per
// account. Conversations cannot be transferred (the transcript layout is not documented) and
// `codex --worktree` takes no name, so neither swap nor wt applies (ADR 0011).
var Codex = Spec{
	ID:        "codex",
	Name:      "Codex CLI",
	Binary:    "codex",
	MainDir:   ".codex",
	DirPrefix: ".codex-",
	ConfigEnv: "CODEX_HOME",
	Settings:  "config.toml",
	Shared:    []string{"AGENTS.md", "rules"},
	// skills/.system holds the skills Codex installs and updates itself in every CODEX_HOME.
	SharedPerEntry: map[string][]string{"skills": {".system"}},
	Subcommands: []string{
		"a", "agents", "app", "app-server", "apply", "archive", "cloud", "debug", "delete", "e", "exec",
		"exec-server", "features", "fork", "login", "logout", "mcp", "migrate-nfs-db", "migrate-rollouts",
		"plugin", "queue", "remote-control", "resume", "review", "sandbox", "unarchive",
	},
	SessionID:   regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`),
	InstallHint: "npm install -g @openai/codex",
	Caps:        Caps{Plugins: true, SharedMCP: true},
}

var registry = []Spec{Claude, Codex}

// Default is used when an account ref names no provider and for the "main" shorthand.
const Default = "claude"

func All() []Spec { return append([]Spec(nil), registry...) }

var (
	driversMu sync.RWMutex
	drivers   = map[string]Driver{}
)

// Register attaches the driver of a provider in the registry; provider packages call it from
// init, and internal/provider/all imports them all.
func Register(id string, d Driver) {
	if _, ok := Get(id); !ok {
		panic("provider: register unknown provider " + id)
	}
	driversMu.Lock()
	defer driversMu.Unlock()
	drivers[id] = d
}

// DriverOf is the registered driver of a provider.
func DriverOf(id string) (Driver, bool) {
	driversMu.RLock()
	defer driversMu.RUnlock()
	d, ok := drivers[id]
	return d, ok
}

func Get(id string) (Spec, bool) {
	for _, s := range registry {
		if s.ID == id {
			return s, true
		}
	}
	return Spec{}, false
}
