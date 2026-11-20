package worktree

import (
	"fmt"
	"os"
	"path/filepath"
)

type installer struct {
	lockfile string
	argv     []string
}

// installers are checked in order; the first lockfile present decides the package manager.
var installers = []installer{
	{"pnpm-lock.yaml", []string{"pnpm", "install", "--frozen-lockfile", "--prefer-offline"}},
	{"bun.lock", []string{"bun", "install", "--frozen-lockfile"}},
	{"bun.lockb", []string{"bun", "install", "--frozen-lockfile"}},
	{"yarn.lock", []string{"yarn", "install", "--frozen-lockfile"}},
	{"package-lock.json", []string{"npm", "ci", "--prefer-offline"}},
}

// Lockfiles are the lockfiles deps recognises, in the order they are looked for.
func Lockfiles() []string {
	out := make([]string, len(installers))
	for i, in := range installers {
		out[i] = in.lockfile
	}
	return out
}

// InstallCommand returns the dependency install command for the lockfile in dir.
func InstallCommand(dir string) ([]string, error) {
	for _, in := range installers {
		if _, err := os.Stat(filepath.Join(dir, in.lockfile)); err == nil {
			return in.argv, nil
		}
	}
	return nil, fmt.Errorf("no supported lockfile in %s (pnpm, bun, yarn, npm)", dir)
}
