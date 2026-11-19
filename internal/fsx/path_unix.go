//go:build !windows

package fsx

import "path/filepath"

func equalPath(a, b string) bool { return filepath.Clean(a) == filepath.Clean(b) }
