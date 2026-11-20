//go:build windows

package cli

import "testing"

func TestConsoleSeen(t *testing.T) {
	for _, c := range []struct {
		name    string
		hwnd    uintptr
		visible bool
		class   string
		want    bool
	}{
		{"no window", 0, false, "", false},
		{"hidden console", 1, false, "ConsoleWindowClass", false},
		{"console window", 1, true, "ConsoleWindowClass", true},
		{"pseudo console", 1, false, pseudoConsoleClass, true},
	} {
		if got := consoleSeen(c.hwnd, c.visible, c.class); got != c.want {
			t.Errorf("%s: consoleSeen = %v, want %v", c.name, got, c.want)
		}
	}
}

// The test binary runs without a console window in CI (and under go test's pipes), so no
// prompt may be opened there.
func TestOpenTerminalWithoutAPerson(t *testing.T) {
	if visibleConsole() {
		t.Skip("a visible console is attached")
	}
	if _, _, _, ok := openTerminal(); ok {
		t.Fatal("openTerminal opened a console nobody sees")
	}
}
