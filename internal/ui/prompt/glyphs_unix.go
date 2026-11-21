//go:build !windows

package prompt

// localeDefault applies when no locale variable is set: the C locale, which is not UTF-8.
var localeDefault = ASCII

func consoleUTF8() bool { return true }
