// Package fsx holds filesystem primitives shared across packages: atomic writes, copies and
// directory swaps, lock files, path comparison, directory ACLs (Windows) and replacing an
// executable that may be running. Writers never leave a half-written file in place, and
// concurrent writers never share a temporary name.
package fsx

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// WriteFile atomically replaces path with the contents of r. The directory is created with
// mode 0o700 when missing.
func WriteFile(path string, r io.Reader, perm fs.FileMode) (err error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = tmp.Close()
			_ = os.Remove(tmp.Name())
		}
	}()
	if _, err := io.Copy(tmp, r); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := tmp.Chmod(perm); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// WriteBytes atomically replaces path with b.
func WriteBytes(path string, b []byte, perm fs.FileMode) error {
	return WriteFile(path, bytes.NewReader(b), perm)
}

// AppendLine appends b and a newline to path in one write, creating the file (and its
// directory, mode 0o700) when missing. Lines appended concurrently by separate writers do not
// interleave on local filesystems.
func AppendLine(path string, b []byte, perm fs.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, perm)
	if err != nil {
		return err
	}
	_, werr := f.Write(append(append([]byte(nil), b...), '\n'))
	return errors.Join(werr, f.Close())
}

// CopyFile copies a regular file atomically, keeping its permission bits (masked by permMask)
// and, when keepTimes is set, its modification time.
func CopyFile(src, dst string, permMask fs.FileMode, keepTimes bool) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	fi, err := in.Stat()
	if err != nil {
		return err
	}
	if !fi.Mode().IsRegular() {
		return fmt.Errorf("copy %s: not a regular file", src)
	}
	if err := WriteFile(dst, in, fi.Mode().Perm()&permMask); err != nil {
		return err
	}
	if keepTimes {
		return os.Chtimes(dst, fi.ModTime(), fi.ModTime())
	}
	return nil
}

// CopyTree copies directories and regular files from src into dst. Anything else (symlinks,
// devices) is an error, so a tree can never smuggle a link out of its root.
func CopyTree(src, dst string, permMask fs.FileMode, keepTimes bool) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		switch {
		case d.IsDir():
			return os.MkdirAll(target, 0o755&permMask|0o700)
		case d.Type().IsRegular():
			return CopyFile(p, target, permMask, keepTimes)
		default:
			return fmt.Errorf("copy %s: unsupported file type", p)
		}
	})
}

// ReplaceDir makes dst a copy of src. The new tree is staged next to dst and swapped in; if the
// swap fails the previous dst is restored, so dst is never left missing.
func ReplaceDir(src, dst string, permMask fs.FileMode) error {
	staging, old := dst+".new", dst+".old"
	_ = os.RemoveAll(staging)
	_ = os.RemoveAll(old)
	if err := CopyTree(src, staging, permMask, false); err != nil {
		_ = os.RemoveAll(staging)
		return err
	}
	hadOld := true
	if err := os.Rename(dst, old); errors.Is(err, fs.ErrNotExist) {
		hadOld = false
	} else if err != nil {
		_ = os.RemoveAll(staging)
		return err
	}
	if err := os.Rename(staging, dst); err != nil {
		if hadOld {
			_ = os.Rename(old, dst)
		}
		_ = os.RemoveAll(staging)
		return err
	}
	return os.RemoveAll(old)
}

// MoveDir replaces dst with the directory src by renaming it. Transient failures (a scanner
// holding a file on Windows) are retried for about two seconds.
func MoveDir(src, dst string) error {
	if _, err := os.Stat(src); err != nil {
		return err
	}
	var err error
	for i := range 8 {
		if i > 0 {
			time.Sleep(time.Duration(50*i) * time.Millisecond)
		}
		if err = os.RemoveAll(dst); err != nil {
			continue
		}
		if err = os.Rename(src, dst); err == nil {
			return nil
		}
	}
	return err
}

// CleanStale removes leftovers of an interrupted ReplaceDir for dst.
func CleanStale(dst string) {
	_ = os.RemoveAll(dst + ".new")
	if _, err := os.Stat(dst); errors.Is(err, fs.ErrNotExist) {
		_ = os.Rename(dst+".old", dst) // a swap died between the two renames: restore
	}
	_ = os.RemoveAll(dst + ".old")
}

func IsDir(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

func IsFile(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.Mode().IsRegular()
}

// SameFile reports whether a and b are the same file after following links (symbolic links,
// and on Windows hard links and junctions, which path comparison cannot see).
func SameFile(a, b string) bool {
	fa, err := os.Stat(a)
	if err != nil {
		return false
	}
	fb, err := os.Stat(b)
	if err != nil {
		return false
	}
	return os.SameFile(fa, fb)
}

// BackupName is where an unrelated file at path is moved before squadrn takes its name.
func BackupName(path string, now time.Time) string {
	return path + ".bak-" + now.Format("20060102-150405")
}

// newGrace is how old a staged <name>.new-* file must be before CleanOld treats it as abandoned;
// a younger one may belong to an update running right now.
const newGrace = 10 * time.Minute

// Leftovers lists the files in dir that ReplaceExecutable left behind for one of names: every
// <name>.old-*, and <name>.new-* older than newGrace. Names compare case-insensitively (Windows).
func Leftovers(dir string, names []string, now time.Time) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		kind, ok := leftoverKind(e.Name(), names)
		if !ok {
			continue
		}
		if kind == ".new-" {
			info, err := e.Info()
			if err != nil || now.Sub(info.ModTime()) < newGrace {
				continue
			}
		}
		out = append(out, filepath.Join(dir, e.Name()))
	}
	return out
}

// leftoverKind reports whether file is <name>.old-<stamp> or <name>.new-<stamp> for one of names.
func leftoverKind(file string, names []string) (string, bool) {
	lower := strings.ToLower(file)
	for _, n := range names {
		n = strings.ToLower(n)
		for _, kind := range []string{".old-", ".new-"} {
			if strings.HasPrefix(lower, n+kind) && len(lower) > len(n)+len(kind) {
				return kind, true
			}
		}
	}
	return "", false
}
