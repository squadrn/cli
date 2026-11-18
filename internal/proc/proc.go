// Package proc runs external programs under a deadline that also holds for their children.
//
// exec.CommandContext alone kills only the direct child: a grandchild that keeps stdout open
// (a git wrapper, a node CLI) blocks Output until it exits on its own. Here the child leads a
// new process group, cancellation kills the whole group, and WaitDelay bounds the pipe drain.
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
}

// Output runs name with args and returns stdout. On failure the error carries the trimmed
// stderr, and whatever stdout was produced is still returned (some CLIs print JSON and exit 1).
func Output(ctx context.Context, opts Options, name string, args ...string) ([]byte, error) {
	cmd := Command(ctx, opts, name, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
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

// Command prepares a command whose cancellation kills its whole process tree.
func Command(ctx context.Context, opts Options, name string, args ...string) *exec.Cmd {
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
