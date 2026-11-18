//go:build windows

package cli

import (
	"os"

	"golang.org/x/sys/windows"
)

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
