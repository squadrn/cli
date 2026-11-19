package fsx

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/squadrn/squadrn/internal/proc"
)

// LockedError reports that another process holds a lock.
type LockedError struct {
	Path string
	PID  int
}

func (e LockedError) Error() string {
	if e.PID <= 0 {
		return "another squadrn process holds " + e.Path
	}
	return fmt.Sprintf("another squadrn process (pid %d) holds %s", e.PID, e.Path)
}

// unreadableGrace is how long a lock file without a readable owner is trusted: its owner may be
// between creating and writing it.
const unreadableGrace = time.Minute

type lockOwner struct {
	PID     int    `json:"pid"`
	Started string `json:"started,omitempty"`
}

// Lock takes an exclusive lock by creating path with this process's pid and start time, waiting
// up to wait for a live holder to let go. A lock whose process is gone (or that stays
// unreadable longer than a minute) is stale and taken over. The returned function releases it.
func Lock(path string, wait time.Duration) (unlock func(), err error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	content, err := json.Marshal(lockOwner{PID: os.Getpid(), Started: proc.StartTime(os.Getpid())})
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(wait)
	for {
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err == nil {
			_, werr := f.Write(content)
			if cerr := f.Close(); werr == nil {
				werr = cerr
			}
			if werr != nil {
				_ = os.Remove(path)
				return nil, werr
			}
			return func() { _ = os.Remove(path) }, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return nil, err
		}
		b, owner, stale := lockState(path, time.Now())
		if stale {
			// Remove the file only if it is still the one judged stale.
			if cur, err := os.ReadFile(path); err == nil && string(cur) == string(b) {
				_ = os.Remove(path)
			}
			continue
		}
		if time.Now().After(deadline) {
			return nil, LockedError{Path: path, PID: owner.PID}
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// lockState reads a lock file and reports whether it is stale.
func lockState(path string, now time.Time) (content []byte, owner lockOwner, stale bool) {
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, owner, true
	}
	if err != nil || json.Unmarshal(b, &owner) != nil || owner.PID <= 0 {
		info, serr := os.Stat(path)
		return b, lockOwner{}, serr == nil && now.Sub(info.ModTime()) > unreadableGrace
	}
	return b, owner, owner.PID != os.Getpid() && !proc.Alive(owner.PID, owner.Started)
}
