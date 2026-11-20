package claude

import (
	"time"

	"github.com/squadrn/squadrn/internal/provider"
	"github.com/squadrn/squadrn/internal/usage"
)

var (
	_ provider.Agent          = CLI{}
	_ provider.Worktrees      = CLI{}
	_ provider.SessionStore   = CLI{}
	_ provider.GuardInstaller = CLI{}
	_ provider.MCP            = CLI{}
	_ provider.Relocator      = CLI{}
	_ provider.StatusLine     = StatusLine{}
	_ provider.SettingsFormat = Settings{}
	_ provider.Profiler       = Profiles{}
	_ provider.Seeder         = Profiles{}
	_ provider.HookEvents     = Events{}
)

func init() {
	provider.Register(provider.Claude.ID, provider.Driver{
		New: func(extraEnv []string) (provider.Agent, error) {
			c, err := Find()
			if err != nil {
				return nil, err
			}
			c.ExtraEnv = extraEnv
			return c, nil
		},
		StatusLine: StatusLine{},
		Settings:   Settings{},
		Profiles:   Profiles{},
		Seed:       Profiles{},
		Events:     Events{},
	})
}

// StatusLine is Claude Code's status line (settings.json statusLine.command) and StopFailure hook.
type StatusLine struct{}

func (StatusLine) Command(path string) (string, bool, error) { return StatusLineCommand(path) }
func (StatusLine) SetCommand(path, cmd string) error         { return SetStatusLineCommand(path, cmd) }

func (StatusLine) Wrap(bin, hook, old, home string) (string, error) {
	return WrapStatusLine(bin, hook, old, home)
}

func (StatusLine) Unwrap(cmd, hook string) (string, bool) { return UnwrapStatusLine(cmd, hook) }
func (StatusLine) Key() string                            { return "statusLine" }
func (StatusLine) Shell(cmd string) (string, []string)    { return StatusLineShell(cmd) }

func (StatusLine) Parse(raw []byte, now time.Time) (usage.Snapshot, error) {
	return ParseStatusLine(raw, now)
}

func (StatusLine) ParseLimit(raw []byte, now time.Time, loc *time.Location) (provider.LimitHit, error) {
	f, err := ParseStopFailure(raw, now, loc)
	return provider.LimitHit(f), err
}
