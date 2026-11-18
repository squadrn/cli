//go:build windows

package claude

import (
	"errors"
	"os"
	"os/exec"
	"os/signal"

	"golang.org/x/sys/windows"

	"sprawling-agents/internal/provider"
)

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
