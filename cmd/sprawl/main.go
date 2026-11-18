// Command sprawl runs coding agents across multiple accounts and parallel worktrees.
package main

import (
	"os"

	"sprawling-agents/internal/cli"
)

func main() {
	os.Exit(cli.Run(os.Args[1:]))
}
