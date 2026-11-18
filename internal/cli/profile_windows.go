//go:build windows

package cli

import (
	"path/filepath"

	"golang.org/x/sys/windows"
)

// powerShellProfiles are the current user's profile scripts of PowerShell 7 and Windows
// PowerShell 5.1 (all hosts and the console host), where install.ps1 may load completion.
func powerShellProfiles() []string {
	docs, err := windows.KnownFolderPath(windows.FOLDERID_Documents, windows.KF_FLAG_DEFAULT)
	if err != nil {
		return nil
	}
	var out []string
	for _, dir := range []string{"PowerShell", "WindowsPowerShell"} {
		for _, f := range []string{"profile.ps1", "Microsoft.PowerShell_profile.ps1"} {
			out = append(out, filepath.Join(docs, dir, f))
		}
	}
	return out
}
