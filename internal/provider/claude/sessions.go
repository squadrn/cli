package claude

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
)

// Claude Code lists its running sessions in <config dir>/sessions/<pid>.json. The registry is
// undocumented: it is read as a hint, files that do not parse or lack a pid or session are
// skipped, and an entry counts only while its process is alive.

// LiveSession is one running Claude Code session.
type LiveSession struct {
	PID       int    `json:"pid"`
	SessionID string `json:"sessionId"`
	Cwd       string `json:"cwd"`
	// Owner is the index of the config dir (in the list given to RunningSessions) listing it.
	Owner int `json:"-"`
}

// RunningSessions returns the sessions listed in configDirs whose process alive reports
// running, sorted by config dir then pid.
func RunningSessions(configDirs []string, alive func(pid int) bool) []LiveSession {
	var out []LiveSession
	for i, dir := range configDirs {
		files, _ := filepath.Glob(filepath.Join(dir, "sessions", "*.json"))
		for _, f := range files {
			b, err := os.ReadFile(f)
			if err != nil {
				continue
			}
			var s LiveSession
			if json.Unmarshal(b, &s) != nil || s.PID <= 0 || s.SessionID == "" || !alive(s.PID) {
				continue
			}
			s.Owner = i
			out = append(out, s)
		}
	}
	sort.SliceStable(out, func(a, b int) bool {
		if out[a].Owner != out[b].Owner {
			return out[a].Owner < out[b].Owner
		}
		return out[a].PID < out[b].PID
	})
	return out
}
