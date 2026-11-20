//go:build !windows

package codex

import (
	"errors"
	"os"
	"os/exec"
	"os/signal"
	"syscall"

	"github.com/squadrn/squadrn/internal/provider"
)

func lookPath() (string, error) {
	bin, err := exec.LookPath("codex")
	if err != nil {
		return "", errNotInstalled
	}
	return bin, nil
}

// run replaces the current process with bin: codex inherits the terminal, signals and exit
// status directly.
func run(bin string, argv, env []string) error {
	return syscall.Exec(bin, argv, env)
}

// runChild runs bin as a child on the terminal and waits for it. ctrl-c is the child's to handle:
// squadrn ignores it meanwhile.
func runChild(bin string, argv, env []string) error {
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
