//go:build !windows

package account

import "os"

// link makes dst share src: a symbolic link.
func link(src, dst string) error { return os.Symlink(src, dst) }

// linksTo reports whether dst is the link sprawl made to src.
func linksTo(dst, src string) bool {
	target, err := os.Readlink(dst)
	return err == nil && target == src
}

// linkTarget is where the symbolic link dst points.
func linkTarget(dst string) (string, bool) {
	t, err := os.Readlink(dst)
	return t, err == nil
}

// LinksTo reports whether dst in an account shares src from the main account.
func LinksTo(dst, src string) bool { return linksTo(dst, src) }

// refreshCopies has nothing to refresh: shared items are symbolic links on Unix.
func refreshCopies(string, map[string]string) []string { return nil }
