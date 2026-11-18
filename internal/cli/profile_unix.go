//go:build !windows

package cli

// powerShellProfiles: install.sh never writes a PowerShell profile.
func powerShellProfiles() []string { return nil }
