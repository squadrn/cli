package fsx

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// LockedError reports that another process holds a lock.
type LockedError struct {
	Path string
	PID  int
}

func (e LockedError) Error() string {
	return fmt.Sprintf("another squadrn process (pid %d) holds %s", e.PID, e.Path)
}

// unreadableGrace is how long a lock file without a readable pid is trusted: its owner may be
// between creating and writing it.
const unreadableGrace = time.Minute

// Lock takes an exclusive lock by creating path with this process's pid. A lock whose process
// is gone (or that stays unreadable longer than a minute) is stale and taken over. The returned
// function releases it.
func Lock(path string) (unlock func(), err error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	for range 3 {
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err == nil {
			_, werr := fmt.Fprintf(f, "%d\n", os.Getpid())
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
		pid, stale := lockState(path, time.Now())
		if !stale {
			return nil, LockedError{Path: path, PID: pid}
		}
		if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return nil, err
		}
	}
	return nil, fmt.Errorf("cannot take the lock %s", path)
}

// lockState reads the pid in a lock file and whether the lock is stale.
func lockState(path string, now time.Time) (pid int, stale bool) {
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return 0, true
	}
	pid, perr := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil || perr != nil || pid <= 0 {
		info, serr := os.Stat(path)
		return 0, serr == nil && now.Sub(info.ModTime()) > unreadableGrace
	}
	return pid, pid != os.Getpid() && !processAlive(pid)
}
