//go:build windows

package claude

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"

	"golang.org/x/sys/windows"

	"github.com/squadrn/cli/internal/provider"
)

// lookPath finds the native claude.exe. A claude.cmd or claude.bat (the npm shim) is refused:
// cmd.exe re-parses the arguments it passes on, so prompts and paths would be mangled.
func lookPath() (string, error) {
	if bin, err := exec.LookPath("claude.exe"); err == nil {
		return bin, nil
	}
	if shim, err := exec.LookPath("claude"); err == nil {
		return "", fmt.Errorf("found %s, but sprawl needs the native claude.exe; install it with: irm https://claude.ai/install.ps1 | iex", shim)
	}
	return "", errNotInstalled
}

// run starts bin as a child on the same console and waits for it. Windows has no exec(2).
//
// ctrl-c reaches every process attached to the console. sprawl must survive it so it can
// return the child's status: signal.Notify keeps Go's console handler from exiting (Ignore
// would let the default handler kill sprawl), and claude decides what ctrl-c means.
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
	defer restoreConsole(saveConsole())

	err := cmd.Run()
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return provider.ExitStatus{Code: ee.ExitCode()}
	}
	return err
}

type consoleModes struct {
	in, out       windows.Handle
	inM, outM     uint32
	hasIn, hasOut bool
}

// saveConsole records the console modes, which the child may change (raw input, VT output) and
// leave changed if it crashes.
func saveConsole() consoleModes {
	var m consoleModes
	m.in, m.out = windows.Handle(os.Stdin.Fd()), windows.Handle(os.Stdout.Fd())
	m.hasIn = windows.GetConsoleMode(m.in, &m.inM) == nil
	m.hasOut = windows.GetConsoleMode(m.out, &m.outM) == nil
	return m
}

func restoreConsole(m consoleModes) {
	if m.hasIn {
		_ = windows.SetConsoleMode(m.in, m.inM)
	}
	if m.hasOut {
		_ = windows.SetConsoleMode(m.out, m.outM)
	}
}
