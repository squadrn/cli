//go:build !windows

package fsx

// ProtectDir: Unix permissions are set where directories are created (0700/0755).
func ProtectDir(string) error { return nil }

// Protected: mode bits, checked by doctor where they matter, govern access on Unix.
func Protected(string) bool { return true }
