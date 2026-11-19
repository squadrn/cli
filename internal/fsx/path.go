package fsx

import (
	"path/filepath"
	"strings"
)

// Real resolves symbolic links in p. Parts that do not exist are kept as written below the
// longest existing parent, which is resolved.
func Real(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	if parent := filepath.Dir(p); parent != p {
		return filepath.Join(Real(parent), filepath.Base(p))
	}
	return p
}

// SamePath reports whether a and b name the same path, as written or after resolving links.
func SamePath(a, b string) bool {
	if equalPath(a, b) {
		return true
	}
	ra, err1 := filepath.EvalSymlinks(a)
	rb, err2 := filepath.EvalSymlinks(b)
	return err1 == nil && err2 == nil && equalPath(ra, rb)
}

// Within reports whether path is dir or inside it, after resolving links. Nothing is within a
// file system root, so a root never claims every path.
func Within(path, dir string) bool {
	if path == "" || dir == "" {
		return false
	}
	if SamePath(path, dir) {
		return true
	}
	path, dir = Real(path), filepath.Clean(Real(dir))
	rel, err := filepath.Rel(dir, path)
	return err == nil && rel != "." && !strings.HasPrefix(rel, "..") && !filepath.IsAbs(rel) && filepath.Dir(dir) != dir
}
