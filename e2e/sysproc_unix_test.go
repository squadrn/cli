//go:build e2e && !windows

package e2e

import "os/exec"

// setCmdLine: Unix processes get their arguments as a list; there is no command line to set.
func setCmdLine(*exec.Cmd, string) {}
