//go:build windows

package cli

import (
	"bytes"
	"os"

	"github.com/squadrn/squadrn-cli/internal/config"
	"github.com/squadrn/squadrn-cli/internal/doctor"
	"github.com/squadrn/squadrn-cli/internal/fsx"
)

// completionShells are the shells completion install and uninstall take.
var completionShells = []string{"powershell"}

// writeCompletions writes one PowerShell completion script for every name, which install.ps1
// offers to load from the user's PowerShell profile.
func (a *app) writeCompletions(names, _ []string) {
	if !a.cfg.CompletionEnabled() {
		return
	}
	if a.writePowerShellCompletion(names) == nil {
		a.out("  PowerShell completion → %s (install.ps1 -AddToProfile loads it)\n", a.tilde(a.powerShellCompletionPath()))
	}
}

func (a *app) writePowerShellCompletion(names []string) error {
	use := a.root.Use
	defer func() { a.root.Use = use }()
	var buf bytes.Buffer
	for _, n := range names {
		a.root.Use = n
		if err := a.root.GenPowerShellCompletionWithDesc(&buf); err != nil {
			return err
		}
		buf.WriteString("\n")
	}
	return fsx.WriteBytes(a.powerShellCompletionPath(), buf.Bytes(), 0o644)
}

// removeCompletions: the PowerShell script is one file for every name, removed with the install.
func (a *app) removeCompletions(string) {}

// removeCompletionBlocks: PowerShell profiles are handled by removeProfileLines.
func (a *app) removeCompletionBlocks() {}

// completionCheck: the execution policy check covers PowerShell completion.
func (a *app) completionCheck() (doctor.Check, bool) { return doctor.Check{}, false }

func (a *app) installCompletion(shell string, _ bool) error {
	if shell != "" && shell != "powershell" {
		return usagef("tab completion for %q is not supported on Windows; PowerShell only", shell)
	}
	if err := a.setCompletion(config.CompletionAuto); err != nil {
		return err
	}
	if err := a.writePowerShellCompletion(a.names()); err != nil {
		return err
	}
	a.out("PowerShell completion → %s\n", a.tilde(a.powerShellCompletionPath()))
	a.out("PowerShell loads it once your profile ($PROFILE) has this line (install.ps1 -AddToProfile adds it):\n  %s\n", profileLine(a.powerShellCompletionPath()))
	return nil
}

func (a *app) uninstallCompletion(shell string) error {
	if shell != "" && shell != "powershell" {
		return usagef("tab completion for %q is not supported on Windows; PowerShell only", shell)
	}
	a.removeProfileLines()
	if err := os.Remove(a.powerShellCompletionPath()); err == nil {
		a.out("  removed %s\n", a.tilde(a.powerShellCompletionPath()))
	}
	if err := a.setCompletion(config.CompletionOff); err != nil {
		return err
	}
	a.out("tab completion is off (config completion = off; %s completion install turns it on)\n", a.cmd)
	return nil
}
