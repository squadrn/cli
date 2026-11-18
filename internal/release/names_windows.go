//go:build windows

package release

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"sprawling-agents/internal/fsx"
)

// BinaryName is the executable inside a release and in the data dir.
const BinaryName = "sprawl.exe"

// commandsFile records the command names sprawl installed in BinDir. Windows has no links that
// survive replacing a running executable, so each name is a copy of the binary (ADR 0008), and
// "ours" is what this manifest lists, not a link target.
const commandsFile = "commands.json"

func (l Layout) commandsPath() string { return filepath.Join(l.DataDir, commandsFile) }

func (l Layout) readCommands() []string {
	var names []string
	if b, err := os.ReadFile(l.commandsPath()); err == nil {
		_ = json.Unmarshal(b, &names)
	}
	return names
}

func exe(dir, name string) string {
	if strings.EqualFold(filepath.Ext(name), ".exe") {
		return filepath.Join(dir, name)
	}
	return filepath.Join(dir, name+".exe")
}

// LinkCommands installs every name in names as a copy of the binary in BinDir (replacing older
// copies even while they run) and removes the copies of names no longer wanted. A file that sprawl
// did not install is moved aside rather than overwritten. ours is unused on Windows (no previous
// product shipped there).
func (l Layout) LinkCommands(names []string, _ []string, now time.Time) (LinkResult, error) {
	var res LinkResult
	if err := os.MkdirAll(l.BinDir, 0o755); err != nil {
		return res, err
	}
	if err := fsx.ProtectDir(l.BinDir); err != nil {
		return res, err
	}
	prev := l.readCommands()
	for _, n := range names {
		dst := exe(l.BinDir, n)
		if _, err := os.Lstat(dst); err == nil && !slices.Contains(prev, n) && !fsx.SameFile(dst, l.Binary()) {
			backup := dst + ".bak-" + now.Format("20060102-150405")
			if err := os.Rename(dst, backup); err != nil {
				return res, err
			}
			res.BackedUp = append(res.BackedUp, backup)
		}
		if err := fsx.ReplaceExecutable(l.Binary(), dst); err != nil {
			return res, err
		}
		if !slices.Contains(prev, n) {
			res.Created = append(res.Created, dst)
		}
	}
	for _, n := range prev {
		if slices.Contains(names, n) {
			continue
		}
		dst := exe(l.BinDir, n)
		if err := removeRunning(dst); err != nil {
			return res, err
		}
		res.Removed = append(res.Removed, dst)
	}
	b, err := json.Marshal(names)
	if err != nil {
		return res, err
	}
	return res, fsx.WriteBytes(l.commandsPath(), b, 0o644)
}

// Unlink removes the command copies sprawl installed and returns them.
func (l Layout) Unlink() ([]string, error) {
	var removed []string
	for _, n := range l.readCommands() {
		dst := exe(l.BinDir, n)
		if err := removeRunning(dst); err != nil {
			return removed, err
		}
		removed = append(removed, dst)
	}
	return removed, nil
}

// removeRunning deletes path, or moves it aside when it is running (fsx.CleanOld deletes it later).
func removeRunning(path string) error {
	err := os.Remove(path)
	switch {
	case err == nil, errors.Is(err, fs.ErrNotExist):
		return nil
	}
	return os.Rename(path, path+".old-"+time.Now().Format("20060102-150405.000000000"))
}
