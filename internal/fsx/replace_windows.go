//go:build windows

package fsx

import (
	"errors"
	"fmt"
	"os"
	"time"

	"golang.org/x/sys/windows"
)

// ReplaceExecutable installs src at dst while dst may be running. Windows refuses to overwrite or
// delete a running image but allows renaming it, so the old file moves aside to
// <dst>.old-<time> and the new one takes its name. CleanOld removes the leftovers once nothing
// runs them. Renames are retried briefly: antivirus scanners hold new files for a moment.
func ReplaceExecutable(src, dst string) error {
	tmp := dst + ".new-" + stamp()
	if err := CopyFile(src, tmp, 0o755, false); err != nil {
		return err
	}
	old := ""
	if _, err := os.Lstat(dst); err == nil {
		old = asideName(dst)
		if err := retry(func() error { return rename(dst, old) }); err != nil {
			_ = os.Remove(tmp)
			return fmt.Errorf("move %s aside: %w", dst, err)
		}
	}
	if err := retry(func() error { return rename(tmp, dst) }); err != nil {
		_ = os.Remove(tmp)
		if old != "" {
			// Put the previous executable back, or the command would be gone.
			if rerr := retry(func() error { return rename(old, dst) }); rerr != nil {
				return fmt.Errorf("install %s: %w (and restoring it from %s failed: %w)", dst, err, old, rerr)
			}
		}
		return fmt.Errorf("install %s: %w", dst, err)
	}
	return nil
}

// rename is os.Rename; tests replace it to inject failures.
var rename = os.Rename

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

// CleanOld deletes the executables ReplaceExecutable moved aside in dir; ones still running stay.
// Only leftovers of names (file names such as squadrn.exe) are touched: dir may be a bin dir
// other programs share.
func CleanOld(dir string, names []string) {
	for _, p := range Leftovers(dir, names, time.Now()) {
		_ = os.Remove(p)
	}
}
