//go:build windows

package fsx

import (
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

// underProfile: the user profile's ACL already keeps other users out.
func underProfile(dir string) bool {
	profile := os.Getenv("USERPROFILE")
	if profile == "" {
		return false
	}
	rel, err := filepath.Rel(strings.ToLower(filepath.Clean(profile)), strings.ToLower(filepath.Clean(dir)))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// ProtectDir gives dir, when it lies outside the user profile (XDG variables, another drive), a
// protected DACL: full control for the current user, SYSTEM and Administrators only, so other
// local users cannot plant executables sprawl or its hooks run.
func ProtectDir(dir string) error {
	if underProfile(dir) {
		return nil
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return err
	}
	sd, err := windows.SecurityDescriptorFromString("D:P(A;OICI;FA;;;" + user.User.Sid.String() + ")(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)")
	if err != nil {
		return err
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return err
	}
	return windows.SetNamedSecurityInfo(dir, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil)
}

// Protected reports whether dir is inside the profile or carries a protected DACL.
func Protected(dir string) bool {
	if underProfile(dir) {
		return true
	}
	sd, err := windows.GetNamedSecurityInfo(dir, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return false
	}
	control, _, err := sd.Control()
	return err == nil && control&windows.SE_DACL_PROTECTED != 0
}
