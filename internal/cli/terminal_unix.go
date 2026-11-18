//go:build !windows

package cli

// enableVT: Unix terminals interpret ANSI sequences already.
func enableVT() bool { return true }
