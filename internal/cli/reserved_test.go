package cli

import (
	"errors"
	"io"
	"testing"

	"github.com/squadrn/cli/internal/account"
)

// Every top-level command and alias must be a reserved account name: otherwise an account of
// that name would shadow it (`sprawl <word> ...` routes to the subcommand first).
func TestRootCommandsAreReservedAccountNames(t *testing.T) {
	a := &app{cmd: "sprawl", stdin: eofReader{}, stdout: io.Discard, stderr: io.Discard}
	root := a.rootCmd()
	root.InitDefaultHelpCmd()
	root.InitDefaultCompletionCmd()
	for _, c := range root.Commands() {
		for _, name := range append([]string{c.Name()}, c.Aliases...) {
			if err := account.ValidateName(name); !errors.Is(err, account.ErrReserved) && !errors.Is(err, account.ErrInvalidName) {
				t.Errorf("command %q is not reserved as an account name (add it to account.Commands)", name)
			}
		}
	}
}

type eofReader struct{}

func (eofReader) Read([]byte) (int, error) { return 0, io.EOF }
