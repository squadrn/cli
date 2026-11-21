package gemini

import (
	"github.com/squadrn/squadrn-cli/internal/provider"
)

var (
	_ provider.Agent             = CLI{}
	_ provider.FileLogout        = CLI{}
	_ provider.LoginHinter       = CLI{}
	_ provider.CredentialStore   = CLI{}
	_ provider.Diagnoser         = CLI{}
	_ provider.SettingsFormat    = Settings{}
	_ provider.PrivateSettings   = Settings{}
	_ provider.MainFilter        = Settings{}
	_ provider.CommentedSettings = Settings{}
	_ provider.GuardInstaller    = CLI{}
	_ provider.GuardHook         = Guard{}
	_ provider.HookEvents        = Events{}
	_ provider.Profiler          = Profiles{}
	_ provider.ProfileFiler      = Profiles{}
	_ provider.MCP               = CLI{}
	_ provider.MCPLister         = CLI{}
	_ provider.Worktrees         = CLI{}
	_ provider.Truster           = CLI{}
)

func init() {
	provider.Register(provider.Gemini.ID, provider.Driver{
		New: func(extraEnv []string) (provider.Agent, error) {
			c, err := Find()
			if err != nil {
				return nil, err
			}
			c.ExtraEnv = extraEnv
			return c, nil
		},
		Settings: Settings{},
		Guard:    Guard{},
		Events:   Events{},
		Profiles: Profiles{},
	})
}
