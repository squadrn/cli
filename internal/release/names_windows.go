//go:build windows

package release

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/squadrn/squadrn-cli/internal/fsx"
)

// BinaryName is the executable inside a release and in the data dir.
const BinaryName = "squadrn.exe"

// commandsFile records the command names squadrn installed in BinDir. Windows has no links that
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

// executableName is the file name of command name in the bin dir.
func executableName(name string) string {
	if strings.EqualFold(filepath.Ext(name), ".exe") {
		return name
	}
	return name + ".exe"
}

// CommandName is the command a file in the bin dir provides (srn.exe → srn).
func CommandName(file string) string {
	if strings.EqualFold(filepath.Ext(file), ".exe") {
		return file[:len(file)-len(".exe")]
	}
	return file
}

func exe(dir, name string) string { return filepath.Join(dir, executableName(name)) }

// LinkCommands installs every name in names as a copy of the binary in BinDir (replacing older
// copies even while they run) and removes the copies of names no longer wanted. A file that squadrn
// did not install is moved aside rather than overwritten.
func (l Layout) LinkCommands(names []string, now time.Time) (LinkResult, error) {
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
			backup := fsx.BackupName(dst, now)
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

// Unlink removes the command copies squadrn installed and returns them.
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
	return fsx.MoveAside(path)
}

// CheckInstallDir refuses a data dir whose path cmd.exe would expand or split: squadrn removes
// its own files after an update or uninstall through a detached cmd.exe (ADR 0008).
func CheckInstallDir(dir string) error {
	if i := strings.IndexAny(dir, "%^&!"); i >= 0 {
		return fmt.Errorf("%w: %s contains %q, which cmd.exe would misread when squadrn cleans up after an update or uninstall; set XDG_DATA_HOME to a path without %%, ^, & or ! in it", ErrUnsafeInstallDir, dir, dir[i])
	}
	return nil
}
