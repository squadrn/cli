// Package grid runs one Claude agent per tmux pane, each in its own worktree and account.
package grid

import (
	"errors"
	"fmt"
	"strings"

	"sprawling-agents/internal/worktree"
)

var ErrInvalidSpec = errors.New("invalid agent spec")

// Agent is one pane: an account running Claude in worktree Name.
type Agent struct {
	Account string
	Name    string
}

// Assign turns specs ("name" or "<account>:name", account as typed) into agents. Unprefixed names use defaultAccount,
// or round-robin over spread when it is non-empty. A worktree may appear only once.
func Assign(specs []string, defaultAccount string, spread []string) ([]Agent, error) {
	agents := make([]Agent, 0, len(specs))
	seen := map[string]bool{}
	next := 0
	for _, spec := range specs {
		acct, name, prefixed := strings.Cut(spec, ":")
		switch {
		case prefixed:
		case len(spread) > 0:
			acct, name = spread[next%len(spread)], spec
			next++
		default:
			acct, name = defaultAccount, spec
		}
		if err := worktree.ValidateName(name); err != nil {
			return nil, err
		}
		if seen[name] {
			return nil, fmt.Errorf("%w: worktree %q given twice; two agents would edit the same checkout", ErrInvalidSpec, name)
		}
		seen[name] = true
		agents = append(agents, Agent{Account: acct, Name: name})
	}
	return agents, nil
}
