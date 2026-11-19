//go:build !windows

package release

import (
	"errors"
	"os"
	"path/filepath"
	"time"
)

// BinaryName is the executable inside a release and in the data dir.
const BinaryName = "squadrn"

// executableName is the file name of command name in the bin dir.
func executableName(name string) string { return name }

// LinkCommands makes every name in names a link to the binary in BinDir and removes links to the
// binary whose name is no longer wanted. A file that is not such a link is moved aside instead
// of overwritten.
func (l Layout) LinkCommands(names []string, now time.Time) (LinkResult, error) {
	var res LinkResult
	if err := os.MkdirAll(l.BinDir, 0o755); err != nil {
		return res, err
	}
	want := map[string]bool{}
	for _, n := range names {
		want[n] = true
		link := filepath.Join(l.BinDir, n)
		target, err := os.Readlink(link)
		if err == nil && target == l.Binary() {
			continue
		}
		if _, err := os.Lstat(link); err == nil {
			backup := link + ".bak-" + now.Format("20060102-150405")
			if err := os.Rename(link, backup); err != nil {
				return res, err
			}
			res.BackedUp = append(res.BackedUp, backup)
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
		if target, err := os.Readlink(link); err == nil && target == l.Binary() {
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

// CheckInstallDir: shell-form hooks quote the path, so any directory works.
func CheckInstallDir(string) error { return nil }
