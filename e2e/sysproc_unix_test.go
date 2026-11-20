//go:build e2e && !windows

package e2e

import "syscall"

// sysProcAttr starts every command in a new session, without a controlling terminal, so prompts
// that fall back to /dev/tty see none, as in CI. Unix processes get their arguments as a list;
// there is no command line to set.
func sysProcAttr(string, bool) *syscall.SysProcAttr { return &syscall.SysProcAttr{Setsid: true} }
