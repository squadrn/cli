// Package provider describes the agent CLIs squadrn drives. What differs between providers is
// data in Spec; what needs code (running the CLI, reading its login) is behind Agent, which each
// provider's own package implements (internal/provider/claude).
package provider

import (
	"context"
	"strconv"
	"strings"
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
	// Subcommands of the agent CLI: `squadrn <account> <subcommand>` passes them through, and
	// they cannot be account names.
	Subcommands []string
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

// ExitStatus is returned by Agent.Exec on platforms where the session runs as a child process
// (Windows): the agent CLI exited with Code, which squadrn passes on as its own exit status.
type ExitStatus struct{ Code int }

func (e ExitStatus) Error() string { return "exit status " + strconv.Itoa(e.Code) }

// Runner runs the CLI non-interactively. configDir "" selects the main account. A non-zero exit
// returns whatever stdout was produced together with the error.
type Runner interface {
	Output(ctx context.Context, configDir string, args ...string) ([]byte, error)
}

// Agent drives one provider's CLI.
type Agent interface {
	Runner
	// Exec runs an interactive session in place of squadrn: on Unix it replaces the process; on
	// Windows it runs as a child and returns ExitStatus when the session ends.
	Exec(configDir string, args []string) error
	Auth(ctx context.Context, configDir string) (AuthStatus, error)
	// LoginArgs, WorktreeArgs and ResumeArgs are the CLI arguments for those sessions.
	LoginArgs() []string
	LogoutArgs() []string
	WorktreeArgs(name string) []string
	ResumeArgs(session string) []string
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
	InstallHint:  "curl -fsSL https://claude.ai/install.sh | bash",
	NativeMarker: "/.local/share/claude/versions/",
	Caps:         Caps{Worktree: true, SessionTransfer: true, Plugins: true, SharedMCP: true, UsageReport: true},
}

var registry = []Spec{Claude}

// Default is used when an account ref names no provider and for the "main" shorthand.
const Default = "claude"

func All() []Spec { return append([]Spec(nil), registry...) }

func Get(id string) (Spec, bool) {
	for _, s := range registry {
		if s.ID == id {
			return s, true
		}
	}
	return Spec{}, false
}
