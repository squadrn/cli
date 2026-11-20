// Package proc runs external programs under a deadline that also holds for their children, and
// answers whether a process still runs. exec.CommandContext alone kills only the direct child,
// so a grandchild holding stdout open would block Output; here cancellation kills the whole
// process tree (a process group on Unix, a job object on Windows) and WaitDelay bounds the pipe
// drain.
package proc

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// waitDelay bounds how long Wait drains pipes after the process group was killed.
const waitDelay = 250 * time.Millisecond

type Options struct {
	Dir   string
	Env   []string // nil inherits the current environment
	Stdin io.Reader
	// Stdout, when set, also receives stdout as it is produced (a client reading replies to what
	// it writes on Stdin).
	Stdout io.Writer
}

// Output runs name with args and returns stdout. On failure the error carries the trimmed
// stderr, and whatever stdout was produced is still returned (some CLIs print JSON and exit 1).
func Output(ctx context.Context, opts Options, name string, args ...string) ([]byte, error) {
	cmd := command(ctx, opts, name, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if opts.Stdout != nil {
		cmd.Stdout = io.MultiWriter(&stdout, opts.Stdout)
	}
	err := run(cmd)
	if err != nil {
		if ctx.Err() != nil {
			err = fmt.Errorf("%w (%w)", ctx.Err(), err)
		}
		// Callers name the operation; the error adds the program and what it printed.
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			err = fmt.Errorf("%s: %w: %s", filepath.Base(name), err, msg)
		} else {
			err = fmt.Errorf("%s: %w", filepath.Base(name), err)
		}
	}
	return stdout.Bytes(), err
}

// command prepares a command whose cancellation kills its whole process tree.
func command(ctx context.Context, opts Options, name string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = opts.Dir
	cmd.Env = opts.Env
	cmd.Stdin = opts.Stdin
	setup(cmd) // platform: the whole process tree dies on cancel
	cmd.WaitDelay = waitDelay
	return cmd
}

// ExitCode extracts a process exit status from err, or -1 when err is not an exit error.
func ExitCode(err error) int {
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	return -1
}

// ShellQuote renders args as one POSIX shell command line, each word in single quotes.
func ShellQuote(args ...string) string {
	quoted := make([]string, len(args))
	for i, a := range args {
		quoted[i] = "'" + strings.ReplaceAll(a, "'", `'\''`) + "'"
	}
	return strings.Join(quoted, " ")
}
