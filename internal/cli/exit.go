package cli

import (
	"errors"
	"fmt"

	"github.com/squadrn/squadrn-cli/internal/account"
	"github.com/squadrn/squadrn-cli/internal/config"
	"github.com/squadrn/squadrn-cli/internal/grid"
	"github.com/squadrn/squadrn-cli/internal/install"
	"github.com/squadrn/squadrn-cli/internal/provider"
	"github.com/squadrn/squadrn-cli/internal/release"
	"github.com/squadrn/squadrn-cli/internal/ui"
	"github.com/squadrn/squadrn-cli/internal/worktree"
)

// Exit codes: 0 ok, 1 runtime error (or "no" at a prompt), 2 usage or invalid input,
// 130 cancelled by the user (ctrl-c, esc). A child's own status is passed through (deps).
const (
	exitOK        = 0
	exitError     = 1
	exitUsage     = 2
	exitCancelled = 130
)

type usageError struct{ msg string }

func (e usageError) Error() string { return e.msg }

func usagef(format string, a ...any) error { return usageError{fmt.Sprintf(format, a...)} }

var (
	// errQuiet fails the command (exit 1) after it already explained itself.
	errQuiet = errors.New("")
	// errAborted is a "no" at a confirmation prompt.
	errAborted = errors.New("aborted")
)

// childExit carries the exit status of a program squadrn ran in the foreground.
type childExit struct{ code int }

func (e childExit) Error() string { return fmt.Sprintf("exit status %d", e.code) }

func exitCode(err error) int {
	var ue usageError
	var ce childExit
	var es provider.ExitStatus
	switch {
	case err == nil:
		return exitOK
	case errors.As(err, &ce):
		return ce.code
	case errors.As(err, &es):
		return es.Code
	case errors.Is(err, ui.ErrCancelled):
		return exitCancelled
	case errors.As(err, &ue), errors.Is(err, account.ErrInvalidName), errors.Is(err, account.ErrReserved),
		errors.Is(err, account.ErrAmbiguous), errors.Is(err, account.ErrUnknownProvider),
		errors.Is(err, config.ErrInvalidCommand), errors.Is(err, config.ErrUnknownKey), errors.Is(err, config.ErrInvalidValue),
		errors.Is(err, install.ErrNameTaken), errors.Is(err, install.ErrDuplicateName), errors.Is(err, release.ErrUnsafeInstallDir),
		errors.Is(err, worktree.ErrInvalidName), errors.Is(err, grid.ErrInvalidSpec):
		return exitUsage
	default:
		return exitError
	}
}

// silent errors were already reported (or need no message).
func silent(err error) bool {
	var ce childExit
	var es provider.ExitStatus
	return errors.Is(err, errQuiet) || errors.Is(err, ui.ErrCancelled) || errors.As(err, &ce) || errors.As(err, &es)
}
