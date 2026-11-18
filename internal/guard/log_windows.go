//go:build windows

package guard

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

// openLog opens the log for appending without following a reparse point (symlink, junction),
// and refuses a reparse point or a hard-linked file: a planted link cannot redirect the writes.
// The check is made on the open handle, so there is no race between checking and opening.
func openLog(path string) (*os.File, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	h, err := windows.CreateFile(p, windows.FILE_APPEND_DATA|windows.SYNCHRONIZE,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_ALWAYS,
		windows.FILE_ATTRIBUTE_NORMAL|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return nil, err
	}
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(h, &info); err != nil {
		_ = windows.CloseHandle(h)
		return nil, err
	}
	if info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 || info.NumberOfLinks > 1 {
		_ = windows.CloseHandle(h)
		return nil, errors.New("guard log is a link; refusing to write through it")
	}
	return os.NewFile(uintptr(h), path), nil
}
