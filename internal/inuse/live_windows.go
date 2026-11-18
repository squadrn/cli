//go:build windows

package inuse

import (
	"errors"
	"strconv"

	"golang.org/x/sys/windows"
)

const stillActive = 259

func open(pid int) (windows.Handle, bool) {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	return h, err == nil
}

func alive(pid int) bool {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if errors.Is(err, windows.ERROR_ACCESS_DENIED) {
		return true // exists, owned by someone else
	} else if err != nil {
		return false
	}
	defer func() { _ = windows.CloseHandle(h) }()
	var code uint32
	return windows.GetExitCodeProcess(h, &code) == nil && code == stillActive
}

// startTime is the process creation time in 100 ns units; "" when it cannot be read.
func startTime(pid int) string {
	h, ok := open(pid)
	if !ok {
		return ""
	}
	defer func() { _ = windows.CloseHandle(h) }()
	var created, exited, kernel, user windows.Filetime
	if windows.GetProcessTimes(h, &created, &exited, &kernel, &user) != nil {
		return ""
	}
	return strconv.FormatInt(created.Nanoseconds(), 10)
}
