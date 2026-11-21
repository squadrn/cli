//go:build windows

package gemini

import (
	"errors"
	"os"
	"os/exec"
	"os/signal"

	"github.com/squadrn/squadrn/internal/provider"
)

// lookPath finds gemini. npm installs gemini.cmd, which cmd.exe re-parses (quotes or carets in
// prompts may be mangled, ADR 0008), so squadrn runs node with the package's bundle/gemini.js
// instead, and falls back to the shim only when it cannot find them.
func lookPath() (string, []string, error) {
	if shim, err := exec.LookPath("gemini.cmd"); err == nil {
		if node, err := exec.LookPath("node.exe"); err == nil {
			if js := bundleFor(shim); js != "" {
				return node, []string{js}, nil
			}
		}
	}
	for _, name := range []string{"gemini.exe", "gemini.cmd", "gemini"} {
		if bin, err := exec.LookPath(name); err == nil {
			return bin, nil, nil
		}
	}
	return "", nil, errNotInstalled
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
