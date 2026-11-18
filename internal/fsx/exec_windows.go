//go:build windows

package fsx

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/windows"
)

// replaceExecutable installs src at dst while dst may be running. Windows refuses to overwrite or
// delete a running image but allows renaming it, so the old file moves aside to
// <dst>.old-<time> and the new one takes its name. CleanOld removes the leftovers once nothing
// runs them. Renames are retried briefly: antivirus scanners hold new files for a moment.
func replaceExecutable(src, dst string) error {
	tmp := dst + ".new-" + stamp()
	if err := CopyFile(src, tmp, 0o755, false); err != nil {
		return err
	}
	if _, err := os.Lstat(dst); err == nil {
		if err := retry(func() error { return os.Rename(dst, dst+".old-"+stamp()) }); err != nil {
			_ = os.Remove(tmp)
			return fmt.Errorf("move %s aside: %w", dst, err)
		}
	}
	if err := retry(func() error { return os.Rename(tmp, dst) }); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("install %s: %w", dst, err)
	}
	return nil
}

func stamp() string { return time.Now().Format("20060102-150405.000000000") }

// retry repeats op while Windows reports the file as in use.
func retry(op func() error) error {
	var err error
	for i := 0; i < 20; i++ {
		if err = op(); err == nil || !busy(err) {
			return err
		}
		time.Sleep(time.Duration(25*(i+1)) * time.Millisecond)
	}
	return err
}

func busy(err error) bool {
	return errors.Is(err, windows.ERROR_SHARING_VIOLATION) || errors.Is(err, windows.ERROR_ACCESS_DENIED) ||
		errors.Is(err, windows.ERROR_LOCK_VIOLATION)
}

// cleanOld deletes executables moved aside by earlier updates; ones still running stay.
func cleanOld(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), ".old-") || strings.Contains(e.Name(), ".new-") {
			_ = os.Remove(filepath.Join(dir, e.Name()))
		}
	}
}
