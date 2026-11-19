//go:build windows

package cli

import (
	"io"
	"os"

	"golang.org/x/sys/windows"
)

// pathWhere says where the PATH fix goes.
const pathWhere = "add squadrn to your PATH (then open a new terminal)"

// enableVT turns on ANSI sequence processing for stdout and stderr (Windows Terminal has it on;
// the classic console needs it). false means colors and the picker's redraw must stay off.
func enableVT() bool {
	ok := true
	for _, f := range []*os.File{os.Stdout, os.Stderr} {
		h := windows.Handle(f.Fd())
		var mode uint32
		if windows.GetConsoleMode(h, &mode) != nil {
			continue // not a console: redirected output, nothing to enable
		}
		if windows.SetConsoleMode(h, mode|windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING) != nil {
			ok = false
		}
	}
	return ok
}

// openTerminal opens the console when stdin or stdout are not it (a script block fed by gh).
func openTerminal() (io.Reader, io.Writer, func(), bool) {
	in, err := os.OpenFile("CONIN$", os.O_RDWR, 0)
	if err != nil {
		return nil, nil, nil, false
	}
	out, err := os.OpenFile("CONOUT$", os.O_RDWR, 0)
	if err != nil {
		_ = in.Close()
		return nil, nil, nil, false
	}
	return in, out, func() { _ = in.Close(); _ = out.Close() }, true
}
