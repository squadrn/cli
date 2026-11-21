package provider

import (
	"maps"
	"strings"
)

// SquadrnCommand reports whether a hook command runs squadrn's hook entry point, from any
// install location and in either platform's form (the plugin's squadrn-hook shim, squadrn.exe).
func SquadrnCommand(cmd string) bool {
	return strings.Contains(cmd, "squadrn-hook") || strings.Contains(cmd, "squadrn.exe")
}

// DropSquadrnHooks removes squadrn's own entries from the hooks object of a settings document
// in the form Claude Code and Gemini CLI share: hooks.<Event>[] = {matcher, hooks: [{command}]}.
// A group left without hooks and an event left without groups are dropped; anything it does not
// recognise is kept as it is.
func DropSquadrnHooks(doc map[string]any) {
	hooks, ok := doc["hooks"].(map[string]any)
	if !ok {
		return
	}
	for event, v := range hooks {
		groups, ok := v.([]any)
		if !ok {
			continue
		}
		var keepGroups []any
		for _, g := range groups {
			group, ok := g.(map[string]any)
			if !ok {
				keepGroups = append(keepGroups, g)
				continue
			}
			list, ok := group["hooks"].([]any)
			if !ok {
				keepGroups = append(keepGroups, g)
				continue
			}
			var keep []any
			for _, h := range list {
				if m, ok := h.(map[string]any); ok {
					if cmd, _ := m["command"].(string); SquadrnCommand(cmd) {
						continue
					}
				}
				keep = append(keep, h)
			}
			if len(keep) == 0 {
				continue
			}
			if len(keep) < len(list) {
				c := maps.Clone(group)
				c["hooks"] = keep
				group = c
			}
			keepGroups = append(keepGroups, group)
		}
		if len(keepGroups) == 0 {
			delete(hooks, event)
		} else {
			hooks[event] = keepGroups
		}
	}
	if len(hooks) == 0 {
		delete(doc, "hooks")
	}
}
