//go:build windows

package codex

import (
	"errors"
	"os"
	"os/exec"
	"os/signal"

	"github.com/squadrn/squadrn-cli/internal/provider"
)

// lookPath finds codex. npm installs codex.cmd, which cmd.exe re-parses: prompts with quotes or
// carets may be mangled (ADR 0008).
func lookPath() (string, error) {
	for _, name := range []string{"codex.exe", "codex.cmd", "codex"} {
		if bin, err := exec.LookPath(name); err == nil {
			return bin, nil
		}
	}
	return "", errNotInstalled
}

// runChild runs bin as a child on the console and waits for it; on Windows that is run.
func runChild(bin string, argv, env []string) error { return run(bin, argv, env) }

// run starts bin as a child on the same console and waits for it. Windows has no exec(2);
// ctrl-c is the child's to handle, squadrn survives it to return the child's status.
func run(bin string, argv, env []string) error {
	cmd := exec.Command(bin, argv[1:]...)
	cmd.Env = env
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)
	defer signal.Stop(sig)
	go func() {
		for range sig { //nolint:revive // drained on purpose: the child handles ctrl-c
		}
	}()
	err := cmd.Run()
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return provider.ExitStatus{Code: ee.ExitCode()}
	}
	return err
}
