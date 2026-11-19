// Package inuse records which sessions sprawl started are running in a directory (a worktree,
// an account's config dir), so commands that delete or move it can refuse while one runs.
//
// Each session writes <dir>/.sprawl-inuse/<pid>.json. On Unix sprawl replaces itself with the
// agent CLI, so the recorded pid is the session's own process; on Windows sprawl stays its
// parent until it exits. An entry counts while its pid is alive and, when known, still has the
// recorded start time (pids are reused); other entries are stale and removed when read.
package inuse

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/squadrn/cli/internal/fsx"
)

// Dir is the registry directory inside a used directory. It is a fixed identifier.
const Dir = ".sprawl-inuse"

// Entry is one running session.
type Entry struct {
	PID int `json:"pid"`
	// Started is the process start time as the platform reports it ("" when unknown).
	Started string `json:"started,omitempty"`
	// Account is the account label the session runs as, e.g. claude/personal.
	Account string `json:"account,omitempty"`
	// Exe is the sprawl binary that started the session.
	Exe string `json:"exe,omitempty"`
	// Worktree names the agent worktree the session works in when it is recorded in the
	// repository root because the worktree did not exist yet (wt <name> creates it).
	Worktree string `json:"worktree,omitempty"`
}

func entryPath(dir string, pid int) string {
	return filepath.Join(dir, Dir, strconv.Itoa(pid)+".json")
}

// Acquire records the current process in dir, dropping stale entries first. PID, Started and
// Exe are filled in; e supplies Account and Worktree.
func Acquire(dir string, e Entry) error {
	Active(dir)
	e.PID = os.Getpid()
	e.Started = startTime(e.PID)
	if exe, err := os.Executable(); err == nil {
		e.Exe = exe
	}
	b, err := json.Marshal(e)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(dir, Dir), 0o700); err != nil {
		return err
	}
	return fsx.WriteBytes(entryPath(dir, e.PID), b, 0o600)
}

// Release removes the current process's entry from dir, and the registry dir once empty.
func Release(dir string) error {
	err := os.Remove(entryPath(dir, os.Getpid()))
	if errors.Is(err, fs.ErrNotExist) {
		err = nil
	}
	_ = os.Remove(filepath.Join(dir, Dir)) // fails while other entries remain
	return err
}

// Active returns the live entries of dir, sorted by pid. Stale entries are deleted.
func Active(dir string) []Entry {
	reg := filepath.Join(dir, Dir)
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
		if err != nil || e.PID != pid || !Live(e.PID, e.Started) {
			_ = os.Remove(p)
			continue
		}
		out = append(out, e)
	}
	if len(out) == 0 {
		_ = os.Remove(reg)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].PID < out[j].PID })
	return out
}

// Live reports whether pid runs and, when started is known, is the same process.
func Live(pid int, started string) bool {
	if pid <= 0 || !alive(pid) {
		return false
	}
	if started == "" {
		return true
	}
	now := startTime(pid)
	return now == "" || now == started
}

// ErrLocked means another live process holds the lock.
var ErrLocked = errors.New("locked by another process")

// Lock takes an exclusive lock file at path, waiting up to wait for a live holder to let go. A
// lock whose holder is gone is taken over. unlock removes it.
func Lock(path string, wait time.Duration) (unlock func(), err error) {
	deadline := time.Now().Add(wait)
	self := lockOwner{PID: os.Getpid(), Started: startTime(os.Getpid())}
	content, err := json.Marshal(self)
	if err != nil {
		return nil, err
	}
	for {
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err == nil {
			_, werr := f.Write(content)
			cerr := f.Close()
			if werr != nil || cerr != nil {
				_ = os.Remove(path)
				return nil, errors.Join(werr, cerr)
			}
			return func() { _ = os.Remove(path) }, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return nil, err
		}
		b, rerr := os.ReadFile(path)
		var owner lockOwner
		// An empty or unreadable file is being written right now: held until the deadline.
		switch {
		case errors.Is(rerr, fs.ErrNotExist):
			continue // released between the two calls
		case rerr == nil && json.Unmarshal(b, &owner) == nil && !Live(owner.PID, owner.Started):
			// The holder died without unlocking. Remove the file only if it is still the one read.
			if cur, err := os.ReadFile(path); err == nil && string(cur) == string(b) {
				_ = os.Remove(path)
			}
			continue
		}
		if time.Now().After(deadline) {
			holder := ""
			if owner.PID > 0 {
				holder = fmt.Sprintf(" (pid %d)", owner.PID)
			}
			return nil, fmt.Errorf("%s: %w%s", path, ErrLocked, holder)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

type lockOwner struct {
	PID     int    `json:"pid"`
	Started string `json:"started,omitempty"`
}
