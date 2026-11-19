//go:build windows

package fsx

import (
	"path/filepath"
	"strings"
)

// equalPath compares case-insensitively and ignores the \\?\ and \??\ prefixes link targets
// carry.
func equalPath(a, b string) bool {
	clean := func(p string) string {
		return filepath.Clean(strings.TrimPrefix(strings.TrimPrefix(p, `\\?\`), `\??\`))
	}
	return strings.EqualFold(clean(a), clean(b))
}
