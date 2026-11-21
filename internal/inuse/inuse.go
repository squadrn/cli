// Package inuse records which sessions squadrn started are running in a directory (a worktree,
// an account's config dir), so commands that delete or move it can refuse while one runs.
//
// Each session writes <dir>/.squadrn-inuse/<pid>.json. On Unix squadrn replaces itself with the
// agent CLI, so the recorded pid is the session's own process; on Windows squadrn stays its
// parent until it exits. An entry counts while its pid is alive and, when known, still has the
// recorded start time (pids are reused); other entries are stale and removed when read.
package inuse

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/squadrn/squadrn-cli/internal/config"
	"github.com/squadrn/squadrn-cli/internal/fsx"
	"github.com/squadrn/squadrn-cli/internal/proc"
)

// Dir is the registry directory inside a used directory. It is a fixed identifier.
const Dir = "." + config.Slug + "-inuse"

// Entry is one running session.
type Entry struct {
	PID int `json:"pid"`
	// Started is the process start time as the platform reports it ("" when unknown).
	Started string `json:"started,omitempty"`
	// Account is the account label the session runs as, e.g. claude/personal.
	Account string `json:"account,omitempty"`
	// Worktree names the agent worktree the session works in when it is recorded in the
	// repository root because the worktree did not exist yet (wt <name> creates it).
	Worktree string `json:"worktree,omitempty"`
	// Cwd is where the session works, in the entry of the account's dir (ps lists it).
	Cwd string `json:"cwd,omitempty"`
}

func entryPath(dir, reg string, pid int) string {
	return filepath.Join(dir, reg, strconv.Itoa(pid)+".json")
}

// Acquire records the current process in dir, dropping stale entries first. PID and Started are
// filled in; e supplies Account and Worktree.
func Acquire(dir string, e Entry) error {
	Active(dir)
	e.PID = os.Getpid()
	e.Started = proc.StartTime(e.PID)
	b, err := json.Marshal(e)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(dir, Dir), 0o700); err != nil {
		return err
	}
	return fsx.WriteBytes(entryPath(dir, Dir, e.PID), b, 0o600)
}

// Release removes the current process's entry from dir, and the registry dir once empty.
func Release(dir string) error {
	err := os.Remove(entryPath(dir, Dir, os.Getpid()))
	if errors.Is(err, fs.ErrNotExist) {
		err = nil
	}
	_ = os.Remove(filepath.Join(dir, Dir)) // fails while other entries remain
	return err
}

// Active returns the live entries of dir, sorted by pid. Stale entries are deleted.
func Active(dir string) []Entry {
	out := active(filepath.Join(dir, Dir))
	sort.Slice(out, func(i, j int) bool { return out[i].PID < out[j].PID })
	return out
}

// active reads the live entries of one registry dir, deleting stale ones and the dir once empty.
func active(reg string) []Entry {
	files, err := os.ReadDir(reg)
	if err != nil {
		return nil
	}
	var out []Entry
	for _, f := range files {
		name := f.Name()
		pid, err := strconv.Atoi(strings.TrimSuffix(name, ".json"))
		if err != nil || !strings.HasSuffix(name, ".json") {
			continue // temporary files of a concurrent write, or foreign files
		}
		p := filepath.Join(reg, name)
		var e Entry
		b, err := os.ReadFile(p)
		if err == nil {
			err = json.Unmarshal(b, &e)
		}
		if err != nil || e.PID != pid || !proc.Alive(e.PID, e.Started) {
			_ = os.Remove(p)
			continue
		}
		out = append(out, e)
	}
	if len(out) == 0 {
		_ = os.Remove(reg)
	}
	return out
}

// Forget removes the entries of dir that name account and no longer run, and returns how many
// it removed and the live ones it kept; the registry dir goes once empty.
func Forget(dir, account string) (removed int, live []Entry) {
	reg := filepath.Join(dir, Dir)
	files, err := os.ReadDir(reg)
	if err != nil {
		return 0, nil
	}
	for _, f := range files {
		pid, err := strconv.Atoi(strings.TrimSuffix(f.Name(), ".json"))
		if err != nil || !strings.HasSuffix(f.Name(), ".json") {
			continue
		}
		p := filepath.Join(reg, f.Name())
		var e Entry
		if b, err := os.ReadFile(p); err != nil || json.Unmarshal(b, &e) != nil || e.Account != account {
			continue
		}
		if e.PID == pid && proc.Alive(e.PID, e.Started) {
			live = append(live, e)
			continue
		}
		if os.Remove(p) == nil {
			removed++
		}
	}
	_ = os.Remove(reg) // fails while other entries remain
	return removed, live
}
