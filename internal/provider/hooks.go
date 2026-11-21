package provider

import (
	"encoding/json"
	"maps"
	"slices"
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
func DropSquadrnHooks(doc map[string]any) { SplitSquadrnHooks(doc) }

// SplitSquadrnHooks is DropSquadrnHooks returning what it removed, per event: each group that
// held squadrn's entries, with only those entries (AddHooks puts them back).
func SplitSquadrnHooks(doc map[string]any) map[string][]any {
	hooks, ok := doc["hooks"].(map[string]any)
	if !ok {
		return nil
	}
	ours := map[string][]any{}
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
			var keep, mine []any
			for _, h := range list {
				if m, ok := h.(map[string]any); ok {
					if cmd, _ := m["command"].(string); SquadrnCommand(cmd) {
						mine = append(mine, h)
						continue
					}
				}
				keep = append(keep, h)
			}
			if len(mine) > 0 {
				c := maps.Clone(group)
				c["hooks"] = mine
				ours[event] = append(ours[event], c)
			}
			if len(keep) == 0 {
				continue
			}
			if len(mine) > 0 {
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
	return ours
}

// AddHooks appends groups to the events of doc's hooks object, after the groups already there;
// a group already there is not added again.
func AddHooks(doc map[string]any, groups map[string][]any) {
	if len(groups) == 0 {
		return
	}
	hooks, ok := doc["hooks"].(map[string]any)
	if !ok {
		hooks = map[string]any{}
		doc["hooks"] = hooks
	}
	for _, event := range slices.Sorted(maps.Keys(groups)) {
		have, _ := hooks[event].([]any)
		for _, g := range groups[event] {
			if !slices.ContainsFunc(have, func(h any) bool { return jsonEqual(h, g) }) {
				have = append(have, g)
			}
		}
		hooks[event] = have
	}
}

func jsonEqual(a, b any) bool {
	x, err1 := json.Marshal(a)
	y, err2 := json.Marshal(b)
	return err1 == nil && err2 == nil && string(x) == string(y)
}
