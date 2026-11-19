package cli

import (
	"bufio"
	"bytes"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/squadrn/cli/internal/install"
	"github.com/squadrn/cli/internal/release"
)

func TestNamePrompt(t *testing.T) {
	look := func(name string) (string, error) {
		if name == "taken" || name == "tk" {
			return "/usr/bin/" + name, nil
		}
		return "", errors.New("not found")
	}
	check := install.NameCheck{Layout: release.Layout{BinDir: "/home/me/.local/bin"}, Accepted: map[string]bool{}, LookPath: look}
	for _, tt := range []struct {
		name, input string
		cmd         string
		aliases     []string
		forced      []string
		output      []string
	}{
		{"enter keeps both", "\n\n", "sprawl", []string{"sra"}, nil, []string{"Command name [sprawl]: ", "Short alias [sra] ('-' for none): "}},
		{"change both", "ai\nsx,sy\n", "ai", []string{"sx", "sy"}, nil, nil},
		{"dash for no alias", "\n-\n", "sprawl", nil, nil, nil},
		{
			"invalid then valid", "Bad\ncon\nai\nai\nsx\n", "ai",
			[]string{"sx"},
			nil,
			[]string{"invalid command name \"Bad\"", "reserved device name", "the alias must differ from the command name (ai)"},
		},
		{
			"collision no, then yes", "taken\nn\ntaken\ny\ntk\ny\n", "taken",
			[]string{"tk"},
			[]string{"taken", "tk"},
			[]string{"taken is already a command (/usr/bin/taken). Use it anyway? [y/N] "},
		},
		{"alias listed twice", "\nsx,sx\nsx\n", "sprawl", []string{"sx"}, nil, []string{"sx is listed twice"}},
		{"no more input keeps the defaults", "", "sprawl", []string{"sra"}, nil, nil},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var out bytes.Buffer
			p := namePrompt{in: bufio.NewReader(strings.NewReader(tt.input)), out: &out, check: check}
			cmd, aliases, forced := p.run("sprawl", []string{"sra"})
			var f []string
			for n := range forced {
				f = append(f, n)
			}
			slices.Sort(f)
			if cmd != tt.cmd || !slices.Equal(aliases, tt.aliases) || !slices.Equal(f, tt.forced) {
				t.Fatalf("got %s %v %v, want %s %v %v\n%s", cmd, aliases, f, tt.cmd, tt.aliases, tt.forced, out.String())
			}
			for _, w := range tt.output {
				if !strings.Contains(out.String(), w) {
					t.Errorf("output lacks %q:\n%s", w, out.String())
				}
			}
		})
	}
}

func TestRenameSummary(t *testing.T) {
	if got := renameSummary("ai", []string{"sx"}, "sprawl", []string{"sx", "sra"}); got != "command: ai → sprawl (removed: ai, added: sprawl, sra)" {
		t.Fatalf("got %q", got)
	}
	if got := renameSummary("ai", []string{"sx"}, "ai", nil); got != "command: ai → ai (removed: sx, added: none)" {
		t.Fatalf("got %q", got)
	}
	if renameSummary("ai", nil, "ai", nil) != "" {
		t.Fatal("no change must print nothing")
	}
}
