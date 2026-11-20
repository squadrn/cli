package codex

import (
	"github.com/squadrn/squadrn/internal/provider"
)

var (
	_ provider.Agent           = CLI{}
	_ provider.GuardInstaller  = CLI{}
	_ provider.MCP             = CLI{}
	_ provider.MCPLister       = CLI{}
	_ provider.Relocator       = CLI{}
	_ provider.CredentialStore = CLI{}
	_ provider.SettingsFormat  = Settings{}
	_ provider.PrivateSettings = Settings{}
	_ provider.Profiler        = Profiles{}
)

func init() {
	provider.Register(provider.Codex.ID, provider.Driver{
		New: func(extraEnv []string) (provider.Agent, error) {
			c, err := Find()
			if err != nil {
				return nil, err
			}
			c.ExtraEnv = extraEnv
			return c, nil
		},
		Settings: Settings{},
		Profiles: Profiles{},
	})
}
