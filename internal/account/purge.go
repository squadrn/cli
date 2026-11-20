package account

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// ErrLinkedDir: an account dir that is a symbolic link or junction is not deleted, since its
// contents live elsewhere.
var ErrLinkedDir = errors.New("account dir is a link")

// PurgeError lists what Purge could not delete; the dir and its Marker stay, so the account can
// still be named to finish the removal.
type PurgeError struct {
	Dir    string
	Failed []error
}

func (e *PurgeError) Error() string {
	return fmt.Sprintf("cannot delete %d item(s) in %s, first: %v", len(e.Failed), e.Dir, e.Failed[0])
}

func (e *PurgeError) Unwrap() []error { return e.Failed }

// Purge deletes a secondary account dir permanently. Links inside it (symbolic links, junctions)
// are removed themselves and never followed, so nothing outside the dir is touched. Read-only
// entries are made writable first. Marker goes last: after a partial failure the account still
// exists and Purge can run again.
func (s Store) Purge(a Account) error {
	if err := s.CheckPurge(a); err != nil {
		return err
	}
	var failed []error
	clearTree(a.Dir, Marker, &failed)
	if len(failed) > 0 {
		return &PurgeError{Dir: a.Dir, Failed: failed}
	}
	for _, p := range []string{filepath.Join(a.Dir, Marker), a.Dir} {
		if err := removeEntry(p); err != nil {
			return &PurgeError{Dir: a.Dir, Failed: []error{err}}
		}
	}
	return nil
}

// CheckPurge reports why Purge would refuse a: a main account, or a dir that is a link.
func (s Store) CheckPurge(a Account) error {
	if a.IsMain() {
		return fmt.Errorf("%w %q: refusing to remove a provider's default account", ErrReserved, a.Label())
	}
	fi, err := os.Lstat(a.Dir)
	if err != nil {
		return err
	}
	if fi.Mode().Type() != fs.ModeDir {
		return fmt.Errorf("%w: %s (%s); remove the link and its target by hand", ErrLinkedDir, a.Dir, fi.Mode().Type())
	}
	return nil
}

// clearTree empties dir (a real directory, never a link), keeping the entry named keep.
func clearTree(dir, keep string, failed *[]error) {
	// A read-only directory (0500) cannot lose entries; it is about to be deleted anyway.
	if fi, err := os.Lstat(dir); err == nil && fi.Mode().Type() == fs.ModeDir && fi.Mode().Perm()&0o200 == 0 {
		_ = os.Chmod(dir, fi.Mode().Perm()|0o700)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		*failed = append(*failed, err)
		return
	}
	for _, e := range entries {
		if e.Name() == keep {
			continue
		}
		p := filepath.Join(dir, e.Name())
		// Only a plain directory is entered; links, junctions and other reparse points are
		// removed as entries. Lstat, not the listing's type, decides.
		if fi, err := os.Lstat(p); err == nil && fi.Mode().Type() == fs.ModeDir {
			n := len(*failed)
			clearTree(p, "", failed)
			if len(*failed) > n {
				continue
			}
		}
		if err := removeEntry(p); err != nil {
			*failed = append(*failed, err)
		}
	}
}

// osRemove is replaced by tests to simulate a file another program holds open.
var osRemove = os.Remove

// removeEntry removes one file, link or empty directory. os.Remove clears the Windows read-only
// attribute itself; a file another program holds open fails with the sharing violation.
func removeEntry(p string) error {
	err := osRemove(p)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}
