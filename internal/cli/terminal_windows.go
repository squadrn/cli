//go:build windows

package cli

import (
	"io"
	"os"

	"golang.org/x/sys/windows"
)

// pathWhere says where the PATH fix goes.
const pathWhere = "add squadrn to your PATH (then open a new terminal)"

var procGetConsoleWindow = windows.NewLazySystemDLL("kernel32.dll").NewProc("GetConsoleWindow")

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

// pseudoConsoleClass is the window class of the hidden stand-in window a pseudo console
// (Windows Terminal, VS Code) gives its programs: a terminal the user sees draws it.
const pseudoConsoleClass = "PseudoConsoleWindow"

// consoleSeen says whether the console a person would answer in can be seen: a console made
// without a window (CREATE_NO_WINDOW, as agents start their shells) or with a hidden one has
// nobody at it, so a prompt there would wait forever. A pseudo console counts as seen.
func consoleSeen(hwnd uintptr, visible bool, class string) bool {
	return hwnd != 0 && (visible || class == pseudoConsoleClass)
}

// visibleConsole reports whether this process's console is one a person sees.
func visibleConsole() bool {
	r, _, _ := procGetConsoleWindow.Call()
	if r == 0 {
		return false
	}
	hwnd := windows.HWND(r)
	buf := make([]uint16, 64)
	n, _ := windows.GetClassName(hwnd, &buf[0], int32(len(buf)))
	return consoleSeen(r, windows.IsWindowVisible(hwnd), windows.UTF16ToString(buf[:n]))
}

// openTerminal opens the console when stdout is not it (a script block whose output is
// captured). Like /dev/tty on Unix it needs a person at it: stdin must be the console and the
// console must be seen. An agent's shell reads a pipe on a hidden console, where nobody would
// ever answer.
func openTerminal() (io.Reader, io.Writer, func(), bool) {
	var mode uint32
	if windows.GetConsoleMode(windows.Handle(os.Stdin.Fd()), &mode) != nil || !visibleConsole() {
		return nil, nil, nil, false
	}
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
