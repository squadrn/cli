//go:build !windows

package release

import (
	"errors"
	"os"
	"path/filepath"
	"time"
)

// BinaryName is the executable inside a release and in the data dir.
const BinaryName = "sprawl"

// LinkCommands makes every name in names a link to the binary in BinDir and removes links of
// ours (to the binary, or to any path in ours) whose name is no longer wanted. A file that is
// not ours is moved aside instead of overwritten.
func (l Layout) LinkCommands(names []string, ours []string, now time.Time) (LinkResult, error) {
	var res LinkResult
	if err := os.MkdirAll(l.BinDir, 0o755); err != nil {
		return res, err
	}
	isOurs := func(target string) bool {
		if target == l.Binary() {
			return true
		}
		for _, o := range ours {
			if target == o {
				return true
			}
		}
		return false
	}
	want := map[string]bool{}
	for _, n := range names {
		want[n] = true
		link := filepath.Join(l.BinDir, n)
		target, err := os.Readlink(link)
		if err == nil && target == l.Binary() {
			continue
		}
		if _, lerr := os.Lstat(link); lerr == nil {
			if err == nil && isOurs(target) {
				if err := os.Remove(link); err != nil {
					return res, err
				}
			} else {
				backup := link + ".bak-" + now.Format("20060102-150405")
				if err := os.Rename(link, backup); err != nil {
					return res, err
				}
				res.BackedUp = append(res.BackedUp, backup)
			}
		}
		if err := os.Symlink(l.Binary(), link); err != nil {
			return res, err
		}
		res.Created = append(res.Created, link)
	}
	entries, err := os.ReadDir(l.BinDir)
	if err != nil {
		return res, err
	}
	for _, e := range entries {
		if want[e.Name()] || e.Type()&os.ModeSymlink == 0 {
			continue
		}
		link := filepath.Join(l.BinDir, e.Name())
		if target, err := os.Readlink(link); err == nil && isOurs(target) {
			if err := os.Remove(link); err != nil {
				return res, err
			}
			res.Removed = append(res.Removed, link)
		}
	}
	return res, nil
}

// Unlink removes the command links in BinDir that point at the binary and returns them.
func (l Layout) Unlink() ([]string, error) {
	entries, err := os.ReadDir(l.BinDir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	var removed []string
	for _, e := range entries {
		link := filepath.Join(l.BinDir, e.Name())
		if target, err := os.Readlink(link); err == nil && target == l.Binary() {
			if err := os.Remove(link); err != nil {
				return removed, err
			}
			removed = append(removed, link)
		}
	}
	return removed, nil
}
