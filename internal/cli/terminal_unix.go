//go:build !windows

package cli

// enableVT: Unix terminals interpret ANSI sequences already.
func enableVT() bool { return true }

// pathWhere says where the PATH fix goes.
const pathWhere = "add to your shell rc"
