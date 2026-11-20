//go:build !windows

package cli

import (
	"io"
	"os"
)

// enableVT: Unix terminals interpret ANSI sequences already.
func enableVT() bool { return true }

// pathWhere says where the PATH fix goes.
const pathWhere = "add to your shell rc"

// visibleConsole: a Unix terminal is one a person sees (an agent's shell has none).
func visibleConsole() bool { return true }

// openTerminal opens the controlling terminal when stdin or stdout are not one (curl | sh).
func openTerminal() (io.Reader, io.Writer, func(), bool) {
	f, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return nil, nil, nil, false
	}
	return f, f, func() { _ = f.Close() }, true
}
