package cli

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/squadrn/squadrn-cli/internal/account"
	"github.com/squadrn/squadrn-cli/internal/install"
	"github.com/squadrn/squadrn-cli/internal/ui"
)

func table(interactive bool) routeTable {
	return routeTable{
		isAccount:   func(s string) bool { return s == "personal" || s == "claude/personal" || s == "main" },
		subcommands: map[string]bool{"account": true, "wt": true, "version": true, "completion": true, "doctor": true},
		passthrough: func(_, s string) bool { return s == "mcp" || s == "auth" },
		suggestions: func() []string { return []string{"account", "wt", "version", "completion", "doctor", "personal"} },
		interactive: interactive,
		cmd:         "squadrn",
	}
}

func TestParseRoute(t *testing.T) {
	tests := []struct {
		name        string
		args        []string
		interactive bool
		want        route
	}{
		{"bare interactive picks", nil, true, route{pick: true}},
		{"bare non-interactive runs main", nil, false, route{}},
		{"-a without args runs the account", []string{"-a", "x"}, true, route{account: "x"}},
		{"positional account", []string{"personal"}, true, route{account: "personal"}},
		{"label account with agent flags", []string{"claude/personal", "-p", "hi"}, false, route{account: "claude/personal", args: []string{"-p", "hi"}}},
		{"-a account", []string{"-a", "x", "--resume", "id"}, false, route{account: "x", args: []string{"--resume", "id"}}},
		{"--account=", []string{"--account=x"}, false, route{account: "x"}},
		{"account then subcommand", []string{"personal", "wt", "feat-a"}, false, route{account: "personal", sub: "wt", args: []string{"feat-a"}}},
		{"subcommand", []string{"account", "ls"}, false, route{sub: "account", args: []string{"ls"}}},
		{"subcommand wins over account", []string{"doctor"}, false, route{sub: "doctor", args: []string{}}},
		{"prompt after --", []string{"--", "fix", "the", "bug"}, false, route{args: []string{"fix", "the", "bug"}}},
		{"account prompt after --", []string{"personal", "--", "doctor"}, false, route{account: "personal", args: []string{"doctor"}}},
		{"quoted prompt", []string{"fix the bug"}, false, route{args: []string{"fix the bug"}}},
		{"agent subcommand passes through", []string{"mcp", "list"}, false, route{args: []string{"mcp", "list"}}},
		{"account agent subcommand", []string{"personal", "auth", "status"}, false, route{account: "personal", args: []string{"auth", "status"}}},
		{"--help is squadrn help", []string{"--help"}, false, route{sub: "help", args: []string{}}},
		{"account --help", []string{"personal", "-h"}, false, route{account: "personal", sub: "help", args: []string{}}},
		{"--version", []string{"--version"}, false, route{sub: "version", args: []string{}}},
		{"--version --json", []string{"--version", "--json"}, false, route{sub: "version", args: []string{"--json"}}},
		{"globals after the account", []string{"personal", "-C", "/r", "--no-color", "wt", "x"}, false, route{account: "personal", dir: "/r", noColor: true, sub: "wt", args: []string{"x"}}},
		{"--dir after the account, then agent flags", []string{"personal", "--dir=/r", "-p", "hi"}, false, route{account: "personal", dir: "/r", args: []string{"-p", "hi"}}},
		{"globals stop at agent args", []string{"personal", "-p", "-C", "x"}, false, route{account: "personal", args: []string{"-p", "-C", "x"}}},
		{"globals stop at --", []string{"personal", "--", "-C", "x"}, false, route{account: "personal", args: []string{"-C", "x"}}},
		{"account --version is the agent's", []string{"personal", "--version"}, false, route{account: "personal", args: []string{"--version"}}},
		{"completion", []string{"completion", "zsh"}, false, route{sub: "completion", args: []string{"zsh"}}},
		{"globals", []string{"--no-color", "-C", "/r", "-a", "personal", "wt", "x"}, false, route{account: "personal", dir: "/r", noColor: true, sub: "wt", args: []string{"x"}}},
		{"-C joined", []string{"-C/r", "doctor"}, false, route{dir: "/r", sub: "doctor", args: []string{}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseRoute(tt.args, table(tt.interactive))
			if err != nil {
				t.Fatal(err)
			}
			if got.account != tt.want.account || got.sub != tt.want.sub || got.pick != tt.want.pick ||
				got.dir != tt.want.dir || got.noColor != tt.want.noColor || !slices.Equal(got.args, tt.want.args) {
				t.Fatalf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestParseRouteRejects(t *testing.T) {
	for args, want := range map[string]string{
		"-a":              "-a requires a value",
		"--account=":      "--account requires",
		"-C":              "-C requires a value",
		"--dir=":          "--dir requires a value",
		"personal -a x":   "the account is given twice (personal and -a x)",
		"acount ls":       `unknown command or account "acount" (did you mean account?)`,
		"fix the":         `to give the agent a prompt: squadrn -- "fix"`,
		"personal acount": `unknown command or account "acount"`,
		"persnal":         `did you mean personal?`,
		"zzzzzzzzzz":      `to create an account:       squadrn account add zzzzzzzzzz`,
	} {
		_, err := parseRoute(strings.Fields(args), table(false))
		var ue usageError
		if !errors.As(err, &ue) || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: %v, want %q", args, err, want)
		}
	}
}

func TestSuggest(t *testing.T) {
	c := []string{"account", "grid", "gc", "doctor"}
	if got := suggest("acount", c); !slices.Equal(got, []string{"account"}) {
		t.Errorf("suggest(acount) = %v", got)
	}
	if got := suggest("g", c); !slices.Equal(got, []string{"gc"}) {
		t.Errorf("suggest(g) = %v", got)
	}
	if got := suggest("docter", c); !slices.Equal(got, []string{"doctor"}) {
		t.Errorf("suggest(docter) = %v", got)
	}
}

func TestExitCodes(t *testing.T) {
	for err, want := range map[error]int{
		nil:                  exitOK,
		usagef("x"):          exitUsage,
		ui.ErrCancelled:      exitCancelled,
		errAborted:           exitError,
		childExit{code: 3}:   3,
		account.ErrReserved:  exitUsage,
		install.ErrNameTaken: exitUsage,
		errors.New("boom"):   exitError,
	} {
		if got := exitCode(err); got != want {
			t.Errorf("exitCode(%v) = %d, want %d", err, got, want)
		}
	}
	if !silent(childExit{1}) || !silent(errQuiet) || silent(errAborted) {
		t.Error("silent")
	}
}
