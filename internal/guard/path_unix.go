//go:build !windows

package guard

// nativePath: Unix paths need no translation.
func nativePath(p string) string { return p }
