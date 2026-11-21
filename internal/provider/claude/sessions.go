package claude

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"

	"github.com/squadrn/squadrn-cli/internal/provider"
)

// Claude Code lists its running sessions in <config dir>/sessions/<pid>.json. The registry is
// undocumented: it is read as a hint, files that do not parse or lack a pid or session are
// skipped, and an entry counts only while its process is alive.

// liveRecord is one registry file.
type liveRecord struct {
	PID       int    `json:"pid"`
	SessionID string `json:"sessionId"`
	Cwd       string `json:"cwd"`
}

// runningSessions returns the sessions listed in configDirs whose process alive reports
// running, sorted by config dir then pid.
func runningSessions(configDirs []string, alive func(pid int) bool) []provider.LiveSession {
	var out []provider.LiveSession
	for i, dir := range configDirs {
		files, _ := filepath.Glob(filepath.Join(dir, "sessions", "*.json"))
		for _, f := range files {
			b, err := os.ReadFile(f)
			if err != nil {
				continue
			}
			var r liveRecord
			if json.Unmarshal(b, &r) != nil || r.PID <= 0 || r.SessionID == "" || !alive(r.PID) {
				continue
			}
			out = append(out, provider.LiveSession{PID: r.PID, SessionID: r.SessionID, Cwd: r.Cwd, Owner: i})
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
