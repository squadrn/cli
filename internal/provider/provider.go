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

	"github.com/squadrn/squadrn/internal/notify"
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
	// UsageQuery: squadrn reads usage limits from the CLI on request (RateLimiter).
	UsageQuery bool
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
	// StateDir is where the CLI keeps its state inside a secondary account's dir, for a CLI whose
	// ConfigEnv names a home rather than the state dir itself (Gemini: GEMINI_CLI_HOME holds
	// .gemini/). "" means the account dir is the state dir; main's dir always is.
	StateDir string
	// MCPConfig is the main account's file holding user-scope MCP servers, relative to home.
	MCPConfig string
	// Settings is an account's settings file, relative to its state dir; secondary accounts get
	// their own copy, merged from main's (Driver.Settings).
	Settings string
	// Shared are linked from the main account into every other account's state dir.
	// SharedPerEntry dirs are linked entry by entry, skipping the listed entries (managed per
	// account by the CLI).
	Shared         []string
	SharedPerEntry map[string][]string
	// WorktreeDir is where the CLI creates worktrees, relative to the repository root.
	WorktreeDir string
	// Subcommands of the agent CLI: `squadrn [<account>] <subcommand>` passes them through. They
	// are valid account names: an account of that name is matched first (ADR 0011).
	Subcommands []string
	// SessionID is the form of a conversation's ID (Caps.SessionTransfer).
	SessionID *regexp.Regexp
	// Plugin is the squadrn plugin's ID in the CLI; "" when the guard is a hook squadrn writes
	// itself. GuardOff then says why such a hook does not run ("" for "not trusted by <binary>").
	Plugin   string
	GuardOff string
	// NoGitHooks: the CLI turns git hooks off in the shell commands it runs (Gemini sets
	// core.hooksPath=""), so its sessions have no git pre-push layer.
	NoGitHooks bool
	// PaneTitleStates map the start of the terminal title the CLI sets to the agent's state, for
	// `ps` when no hook reported one (Gemini: ui.dynamicWindowTitle).
	PaneTitleStates map[string]string
	// Credentials are an account's credential files relative to its state dir (none when the CLI
	// keeps them elsewhere only); other accounts' profiles deny reading them.
	Credentials []string
	// ProfileSummaries describe the profiles where the shared summaries do not fit this CLI.
	ProfileSummaries map[string]string
	// MinVersion is the oldest CLI version squadrn supports ("" for no floor); doctor warns below.
	MinVersion string
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

// RateLimiter reads an account's usage limits from the CLI on request (Caps.UsageQuery).
type RateLimiter interface {
	RateLimits(ctx context.Context, configDir string) (usage.Snapshot, error)
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
	// Key is the settings key holding the status line (one path for SettingsFormat).
	Key() string
	// Shell is how the CLI runs a status line command.
	Shell(cmd string) (bin string, argv []string)
	// Parse reads the status line input; ParseLimit the hook input of a turn that failed.
	Parse(raw []byte, now time.Time) (usage.Snapshot, error)
	ParseLimit(raw []byte, now time.Time, loc *time.Location) (LimitHit, error)
}

// SettingsFormat reads and writes the CLI's settings file (Spec.Settings) as a generic document,
// so squadrn can merge an account's own copy from main's (ADR 0011). It needs no CLI binary.
type SettingsFormat interface {
	Decode(b []byte) (map[string]any, error)
	Encode(doc map[string]any) ([]byte, error)
	// Deep are the top-level objects merged one level down (Claude: permissions, env, hooks).
	Deep() []string
}

// PrivateSettings is a SettingsFormat with top-level keys that stay per account: main's values
// are never merged into an account's copy (Codex: MCP servers, hook trust).
type PrivateSettings interface {
	Private() []string
}

// MainFilter is a SettingsFormat that drops parts of main's settings before they are merged
// into an account's copy: squadrn's own hook entries, which each account gets (or not) from its
// profile, so removing the guard from one account sticks (ADR 0013). It edits doc in place.
type MainFilter interface {
	FromMain(doc map[string]any)
}

// CommentedSettings is a SettingsFormat whose files may hold comments that Encode drops. Squadrn
// never rewrites such a file when it has them (main's settings); an account's own copy is
// written without them.
type CommentedSettings interface {
	HasComments(b []byte) bool
}

// FileLogout is an Agent whose CLI has no logout command (LogoutArgs is nil): logging out moves
// the files LogoutFiles lists, those that exist in the account, to the trash.
type FileLogout interface {
	LogoutFiles(configDir string) []string
}

// LoginHinter is an Agent whose login is an interactive session that does not end by itself:
// squadrn prints LoginHint before it starts.
type LoginHinter interface {
	LoginHint() string
}

// Notice is something doctor reports about a provider's CLI or accounts. Warn marks a problem;
// otherwise it is shown as information.
type Notice struct {
	Warn      bool
	Text, Fix string
}

// Diagnoser adds provider-specific checks to doctor.
type Diagnoser interface {
	// Diagnose reports on what the installed CLI needs (a runtime) and the user's environment,
	// once, with the install checks.
	Diagnose(ctx context.Context) []Notice
	// AccountNotices reports what to know about one account (configDir "" is main).
	AccountNotices(configDir string) []Notice
}

// Profiles an account is created with (ADR 0011); DefaultProfile when none is chosen.
const (
	ProfileMinimal  = "minimal"
	ProfileStandard = "standard"
	ProfileStrict   = "strict"
	DefaultProfile  = ProfileStandard
)

// ProfileNames lists the profiles in order, with what each one does in a few words.
var ProfileNames = []struct{ Name, Summary string }{
	{ProfileMinimal, "secrets denied"},
	{ProfileStandard, "secrets denied, push guard, 30-day history"},
	{ProfileStrict, "sandboxed, reads limited to the project, 14-day history"},
}

// Options are the opt-in toggles a profile can be combined with.
var Options = []struct{ Name, Summary string }{
	{"privacy", "telemetry and error reporting off"},
	{"no-attribution", "no AI attribution in commits and pull requests"},
}

// ProfileSummary is the one-line description of a profile; "" for an unknown one.
func ProfileSummary(name string) string {
	for _, p := range ProfileNames {
		if p.Name == name {
			return p.Summary
		}
	}
	return ""
}

// ProfileSummary is the one-line description of a profile for this provider: its own when it
// has one (ProfileSummaries), else the shared one.
func (s Spec) ProfileSummary(name string) string {
	if v, ok := s.ProfileSummaries[name]; ok {
		return v
	}
	return ProfileSummary(name)
}

// ValidOption reports whether name is a known toggle.
func ValidOption(name string) bool {
	for _, o := range Options {
		if o.Name == name {
			return true
		}
	}
	return false
}

// ProfileContext is what a profile needs to know about this machine, as paths in the CLI's own
// rule syntax (~/ for home, / separators).
type ProfileContext struct {
	// OtherCredentials are the credential files of every other account, of every provider.
	OtherCredentials []string
	// OwnDirs are squadrn's data and config dirs, which agents must not edit.
	OwnDirs []string
	// Hook is the squadrn binary for profile hooks (the strict audit hook); Home the user's home.
	Hook string
	Home string
	// Notify: squadrn's notifications are on, so the CLI's own are turned off.
	Notify bool
}

// Profiler builds the settings a profile and options add to main's (a SettingsFormat document).
type Profiler interface {
	Profile(name string, options []string, ctx ProfileContext) (map[string]any, error)
}

// Seeder copies the allowlisted onboarding state from main into a new account dir, so the CLI
// does not run its first-time setup again; home is the user's home.
type Seeder interface {
	Seed(home, dir string) error
}

// Driver is the code behind a Spec, registered by the provider's own package.
type Driver struct {
	// New finds the CLI and returns its driver; extraEnv is added to every invocation (the
	// git-level guard's GIT_CONFIG_* entries).
	New func(extraEnv []string) (Agent, error)
	// StatusLine is nil when the provider has none.
	StatusLine StatusLine
	// Settings is nil when accounts do not get their own settings file.
	Settings SettingsFormat
	// Profiles is nil when the provider has no profiles; Seed when there is nothing to seed.
	Profiles Profiler
	Seed     Seeder
	// Events is nil when the CLI reports nothing squadrn notifies about.
	Events HookEvents
	// Guard reads the pre-tool hook of a CLI whose payload or reply differs from Claude Code's
	// (Gemini); nil means Claude Code's form.
	Guard GuardHook
}

// Shells a pre-tool hook's command runs in.
const (
	ShellSh         = "sh"
	ShellPowerShell = "powershell"
)

// ToolCall is the shell command a pre-tool hook judges. Shell is ShellSh or ShellPowerShell;
// "" means the tool is not a shell, which the guard cannot judge.
type ToolCall struct {
	Command, Dir, Shell string
	// Tool names the tool for messages.
	Tool string
}

// GuardHook is the pre-tool hook of a CLI: how its payload names the command and how it reads a
// block. It needs no CLI binary: it runs on every tool call.
type GuardHook interface {
	ParseTool(raw []byte) (ToolCall, error)
	// Block is the reply that stops the call: what to print on stdout and stderr, and the exit
	// code (Gemini: exit 2 with the reason on stderr).
	Block(reason string) (stdout, stderr string, code int)
}

// HookEvents turns the payload of the CLI's notification hooks into an event (ADR 0012). An
// event with no Kind is not a notification (a Stop hook while background tasks run).
type HookEvents interface {
	ParseEvent(raw []byte) (notify.Event, error)
}

// HookReplier is HookEvents of a CLI that expects output from some of those hooks (Codex
// reads a Stop hook's stdout as JSON): HookReply is what the hook prints for raw, "" for nothing.
type HookReplier interface {
	HookReply(raw []byte) string
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
	// settings.json is not shared: each account has its own copy, merged from main's (ADR 0011).
	Shared: []string{"CLAUDE.md", "agents", "hooks", "commands", "output-styles", "rules", "keybindings.json"},
	// skills/synced is managed per account by claude.ai.
	SharedPerEntry: map[string][]string{"skills": {"synced"}},
	WorktreeDir:    ".claude/worktrees",
	Subcommands: []string{
		"agents", "attach", "auth", "auto-mode", "doctor", "gateway", "import", "install", "logs", "mcp",
		"migrate-installer", "plugin", "plugins", "purge", "respawn", "setup-token", "update",
	},
	SessionID:    regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`),
	Plugin:       "squadrn@squadrn",
	Credentials:  []string{".credentials.json"},
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
	Credentials: []string{"auth.json"},
	ProfileSummaries: map[string]string{
		ProfileMinimal:  "credentials per account, secret env vars not passed",
		ProfileStandard: "workspace-write sandbox, no network, asks before leaving it",
		ProfileStrict:   "read-only workspace; use standard for daily work",
	},
	InstallHint: "npm install -g @openai/codex",
	Caps:        Caps{Plugins: true, SharedMCP: true, UsageQuery: true},
}

// geminiProfiles: the Gemini profiles (policy files, the guard hook) come in a later phase of
// ADR 0013; until then every profile only keeps credentials per account.
const geminiProfiles = "credentials per account, push guard hook; other Gemini profile settings are not available yet"

// Gemini is Google's Gemini CLI (ADR 0013). GEMINI_CLI_HOME names a home: an account is
// ~/.gemini-<name>, holding the CLI's state in its .gemini/ (StateDir); main is ~/.gemini, run
// with the variable unset. Gemini has no login or logout command: auth is file-based.
var Gemini = Spec{
	ID:        "gemini",
	Name:      "Gemini CLI",
	Binary:    "gemini",
	MainDir:   ".gemini",
	DirPrefix: ".gemini-",
	ConfigEnv: "GEMINI_CLI_HOME",
	StateDir:  ".gemini",
	Settings:  "settings.json",
	// Never shared: credentials, installation_id, projects.json, tmp/, history/, the trust files
	// (the CLI rewrites them), policies/ (would apply to every account) and extensions/.
	Shared:         []string{"GEMINI.md", "commands", "agents"},
	SharedPerEntry: map[string][]string{"skills": nil},
	Subcommands:    []string{"mcp", "extensions", "extension", "skills", "skill", "hooks", "hook", "gemma", "models"},
	// gemini-credentials.json is the encrypted fallback store; its key derives from host and
	// user, so any account could decrypt another's.
	Credentials: []string{"oauth_creds.json", "google_accounts.json", "gemini-credentials.json"},
	ProfileSummaries: map[string]string{
		ProfileMinimal:  geminiProfiles,
		ProfileStandard: geminiProfiles,
		ProfileStrict:   geminiProfiles,
	},
	MinVersion:  "0.70.0",
	InstallHint: "npm install -g @google/gemini-cli",
	// The guard is a BeforeTool hook squadrn writes into each account's settings.json.
	GuardOff:   "turned off or changed (hooksConfig, matcher or command)",
	NoGitHooks: true,
	// Gemini titles its terminal "✋ Action Required", "⏲ Working…" or "◇ Ready".
	PaneTitleStates: map[string]string{"✋": "input", "⏲": "working", "◇": "ready"},
	Caps:            Caps{Plugins: true, SharedMCP: true},
}

var registry = []Spec{Claude, Codex, Gemini}

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

var versionPattern = regexp.MustCompile(`(?:^|[^0-9.])(\d+\.\d+(?:\.\d+)?(?:[-+][0-9A-Za-z.-]+)?)`)

// ParseVersion finds the version in a CLI's --version output ("2.1.329 (Claude Code)",
// "codex-cli 0.171.0"); "" when there is none.
func ParseVersion(out []byte) string {
	if m := versionPattern.FindStringSubmatch(string(out)); m != nil {
		return m[1]
	}
	return ""
}
