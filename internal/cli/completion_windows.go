//go:build windows

package cli

import (
	"bytes"

	"github.com/squadrn/squadrn-cli/internal/fsx"
)

// writeCompletions writes one PowerShell completion script for every name, which install.ps1
// offers to load from the user's PowerShell profile.
func (a *app) writeCompletions(names, _ []string) {
	use := a.root.Use
	defer func() { a.root.Use = use }()
	var buf bytes.Buffer
	for _, n := range names {
		a.root.Use = n
		if err := a.root.GenPowerShellCompletionWithDesc(&buf); err != nil {
			return
		}
		buf.WriteString("\n")
	}
	if fsx.WriteBytes(a.powerShellCompletionPath(), buf.Bytes(), 0o644) == nil {
		a.out("  PowerShell completion → %s (install.ps1 -AddToProfile loads it)\n", a.tilde(a.powerShellCompletionPath()))
	}
}
