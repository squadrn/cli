// Command squadrn runs coding agents across multiple accounts and parallel worktrees.
package main

import (
	"os"

	"github.com/squadrn/cli/internal/cli"
)

func main() {
	os.Exit(cli.Run(os.Args[1:]))
}
