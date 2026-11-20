//go:build windows

package doctor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/squadrn/squadrn/internal/provider"
)

// A command copy listed in commands.json is the installed binary byte for byte, not "a different
// program" (doctor ran as srn.exe and flagged the squadrn.exe alias copy).
func TestSameProgramCopies(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
		return p
	}
	self := write("srn.exe", "binary v1")
	alias := write("squadrn.exe", "binary v1")
	other := write("other.exe", "something else")
	if !sameProgram(self, self) || !sameProgram(alias, self) {
		t.Fatal("an identical copy is the same program")
	}
	if sameProgram(other, self) || sameProgram(filepath.Join(dir, "missing.exe"), self) {
		t.Fatal("a different or missing file is not")
	}
}

func TestVersionNotesOnFailure(t *testing.T) {
	claude := provider.Spec{Binary: "claude"}
	for v, warn := range map[string]bool{
		"2.1.0 (Claude Code)":   true,
		"2.1.294 (Claude Code)": true,
		"2.1.295 (Claude Code)": false,
		"2.2.0 (Claude Code)":   false,
		"3.0.1":                 false,
		"unknown":               false,
	} {
		got := versionNotes(claude, v)
		if (len(got) == 1) != warn || warn && !strings.Contains(got[0].Name, "2.1.295") {
			t.Errorf("%s: %+v", v, got)
		}
	}
	if got := versionNotes(provider.Spec{Binary: "codex"}, "0.1.0"); got != nil {
		t.Errorf("other provider: %+v", got)
	}
}
