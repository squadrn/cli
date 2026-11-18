package claude

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// HasPlugin reports whether plugin id (name@marketplace) is installed in a config dir, read from
// Claude Code's plugin registry without running claude.
func HasPlugin(configDir, id string) bool {
	b, err := os.ReadFile(filepath.Join(configDir, "plugins", "installed_plugins.json"))
	if err != nil {
		return false
	}
	var reg struct {
		Plugins map[string]json.RawMessage `json:"plugins"`
	}
	if json.Unmarshal(b, &reg) != nil {
		return false
	}
	_, ok := reg.Plugins[id]
	return ok
}
