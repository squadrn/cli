//go:build windows

package prompt

import "golang.org/x/sys/windows"

// localeDefault applies when no locale variable is set, as usual on Windows: the console's
// output code page (consoleUTF8) decides.
var localeDefault = Unicode

// consoleUTF8 reports whether the console's output code page is UTF-8 (65001).
func consoleUTF8() bool {
	cp, err := windows.GetConsoleOutputCP()
	return err == nil && cp == 65001
}
