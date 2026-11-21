//go:build !windows

package completion

import "golang.org/x/sys/unix"

// writable: dir exists and this user may create files in it (checked without writing).
func writable(dir string) bool { return unix.Access(dir, unix.W_OK|unix.X_OK) == nil }
