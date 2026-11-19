package cli

import (
	"os"
	"strings"

	"github.com/squadrn/cli/internal/fsx"
)

// profileLine is the line install.ps1 adds to a PowerShell profile to load completion. It
// checks the file first, so the profile keeps working after sprawl is uninstalled.
func profileLine(completion string) string {
	q := "'" + strings.ReplaceAll(completion, "'", "''") + "'"
	return "if (Test-Path " + q + ") { . " + q + " }"
}

// withoutProfileLines removes sprawl's completion lines for completion from a profile script:
// the current guarded form and the unguarded one earlier installers wrote. Other lines stay
// byte for byte.
func withoutProfileLines(content, completion string) (string, bool) {
	ours := map[string]bool{profileLine(completion): true, `. "` + completion + `"`: true}
	lines := strings.SplitAfter(content, "\n")
	kept := lines[:0]
	removed := false
	for _, l := range lines {
		if ours[strings.TrimSpace(l)] {
			removed = true
			continue
		}
		kept = append(kept, l)
	}
	return strings.Join(kept, ""), removed
}

// removeProfileLines takes sprawl's completion lines out of the user's PowerShell profiles.
func (a *app) removeProfileLines() {
	for _, p := range powerShellProfiles() {
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		content, removed := withoutProfileLines(string(b), a.powerShellCompletionPath())
		if !removed {
			continue
		}
		if err := fsx.WriteBytes(p, []byte(content), 0o644); err != nil {
			a.warnf("could not remove sprawl's completion line from %s: %v", p, err)
			continue
		}
		a.out("  removed completion from %s\n", a.tilde(p))
	}
}
