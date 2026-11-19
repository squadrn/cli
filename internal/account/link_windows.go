//go:build windows

package account

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"golang.org/x/sys/windows"

	"github.com/squadrn/cli/internal/fsx"
)

// Without Developer Mode (or admin rights) Windows refuses symbolic links. link then falls back
// to what needs no privilege: a junction for a directory and a copy for a file. Copies are
// recorded with their hash so a later refresh never overwrites edits made in the account
// (refreshCopies). Hard links are not used: editors and Claude Code save by replacing the file,
// which silently breaks a hard link, while a recorded copy is refreshed or reported.
var symlink = os.Symlink

// copiesFile records the shared files that are copies: name -> sha256 at copy time.
const copiesFile = ".squadrn-copies.json"

func link(src, dst string) error {
	err := symlink(src, dst)
	if err == nil || !errors.Is(err, windows.ERROR_PRIVILEGE_NOT_HELD) {
		return err
	}
	fi, err := os.Stat(src)
	if err != nil {
		return err
	}
	if fi.IsDir() {
		return junction(src, dst)
	}
	return copyShared(src, dst)
}

// linksTo reports whether dst is squadrn's link to src: a symbolic link or junction pointing at
// it, a hard link to it, or a recorded copy that nobody has edited.
func linksTo(dst, src string) bool {
	if target, err := os.Readlink(dst); err == nil {
		return samePath(target, src)
	}
	if fi, err := os.Lstat(dst); err != nil || !fi.Mode().IsRegular() {
		return false
	}
	if fsx.SameFile(dst, src) {
		return true
	}
	sum, ok := readCopies(filepath.Dir(dst))[filepath.Base(dst)]
	return ok && sum == hashFile(dst)
}

// linkTarget is where the symbolic link or junction dst points.
func linkTarget(dst string) (string, bool) {
	t, err := os.Readlink(dst)
	if err != nil {
		return "", false
	}
	return strings.TrimPrefix(strings.TrimPrefix(t, `\\?\`), `\??\`), true
}

// LinksTo reports whether dst in an account shares src from the main account.
func LinksTo(dst, src string) bool { return linksTo(dst, src) }

func samePath(a, b string) bool {
	clean := func(p string) string {
		p = strings.TrimPrefix(strings.TrimPrefix(p, `\\?\`), `\??\`)
		return strings.ToLower(filepath.Clean(p))
	}
	return clean(a) == clean(b)
}

func copyShared(src, dst string) error {
	if err := fsx.CopyFile(src, dst, 0o644, true); err != nil {
		return err
	}
	copies := readCopies(filepath.Dir(dst))
	copies[filepath.Base(dst)] = hashFile(dst)
	return writeCopies(filepath.Dir(dst), copies)
}

// refreshCopies updates copies of shared files in dir whose source in main changed. A copy that
// was edited in the account is left alone and returned, so the caller can report it.
func refreshCopies(dir string, sources map[string]string) (conflicts []string) {
	copies := readCopies(dir)
	changed := false
	for name, sum := range copies {
		dst, src := filepath.Join(dir, name), sources[name]
		if src == "" {
			continue
		}
		current, want := hashFile(dst), hashFile(src)
		switch {
		case current == want:
		case current != sum:
			conflicts = append(conflicts, dst)
		default:
			if fsx.CopyFile(src, dst, 0o644, true) == nil {
				copies[name] = want
				changed = true
			}
		}
	}
	if changed {
		_ = writeCopies(dir, copies)
	}
	return conflicts
}

func readCopies(dir string) map[string]string {
	m := map[string]string{}
	if b, err := os.ReadFile(filepath.Join(dir, copiesFile)); err == nil {
		_ = json.Unmarshal(b, &m)
	}
	return m
}

func writeCopies(dir string, m map[string]string) error {
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return fsx.WriteBytes(filepath.Join(dir, copiesFile), b, 0o600)
}

func hashFile(p string) string {
	f, err := os.Open(p)
	if err != nil {
		return ""
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return ""
	}
	return hex.EncodeToString(h.Sum(nil))
}

// junction makes dst a directory junction (mount point) to src; it needs no privilege.
func junction(src, dst string) error {
	target, err := filepath.Abs(src)
	if err != nil {
		return err
	}
	if err := os.Mkdir(dst, 0o755); err != nil {
		return err
	}
	if err := setMountPoint(dst, target); err != nil {
		_ = os.Remove(dst)
		return fmt.Errorf("junction %s: %w", dst, err)
	}
	return nil
}

func setMountPoint(dir, target string) error {
	subst, err := windows.UTF16FromString(`\??\` + target)
	if err != nil {
		return err
	}
	printName, err := windows.UTF16FromString(target)
	if err != nil {
		return err
	}
	// REPARSE_DATA_BUFFER (MountPointReparseBuffer): header, four uint16 name offsets/lengths,
	// then "substitute name\0print name\0" in UTF-16.
	substBytes, printBytes := (len(subst)-1)*2, (len(printName)-1)*2
	pathBuf := slices.Concat(subst, printName)
	dataLen := 8 + len(pathBuf)*2
	buf := make([]byte, 8+dataLen)
	put16 := func(off, v int) { buf[off], buf[off+1] = byte(v), byte(v>>8) }
	put32 := func(off int, v uint32) {
		buf[off], buf[off+1], buf[off+2], buf[off+3] = byte(v), byte(v>>8), byte(v>>16), byte(v>>24)
	}
	put32(0, windows.IO_REPARSE_TAG_MOUNT_POINT)
	put16(4, dataLen)
	put16(8, 0)             // substitute name offset
	put16(10, substBytes)   // substitute name length
	put16(12, substBytes+2) // print name offset (after the substitute's NUL)
	put16(14, printBytes)   // print name length
	for i, c := range pathBuf {
		put16(16+2*i, int(c))
	}

	p, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		return err
	}
	h, err := windows.CreateFile(p, windows.GENERIC_WRITE, 0, nil, windows.OPEN_EXISTING,
		windows.FILE_FLAG_OPEN_REPARSE_POINT|windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return err
	}
	defer func() { _ = windows.CloseHandle(h) }()
	var n uint32
	return windows.DeviceIoControl(h, windows.FSCTL_SET_REPARSE_POINT, &buf[0], uint32(len(buf)), nil, 0, &n, nil)
}
