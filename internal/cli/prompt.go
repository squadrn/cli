package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/squadrn/cli/internal/install"
)

// namePrompt asks for the command name and aliases at install time (self-install --interactive).
type namePrompt struct {
	in    *bufio.Reader
	out   io.Writer
	check install.NameCheck
	eof   bool
}

func (p *namePrompt) ask(format string, args ...any) string {
	_, _ = fmt.Fprintf(p.out, format, args...)
	line, err := p.in.ReadString('\n')
	if err != nil && (errors.Is(err, io.EOF) || line == "") {
		p.eof = true
	}
	return strings.TrimSpace(line)
}

func (p *namePrompt) say(format string, args ...any) {
	_, _ = fmt.Fprintf(p.out, "  "+format+"\n", args...)
}

// accept validates name and, when another program has it, asks whether to use it anyway.
// forced collects the names confirmed that way.
func (p *namePrompt) accept(name string, forced map[string]bool) bool {
	if err := p.check.Valid(name); err != nil {
		p.say("%v", err)
		p.say("use lowercase letters, digits and -, starting with a letter (max 16)")
		return false
	}
	if path := p.check.Clash(name); path != "" && !p.check.Force && !forced[name] {
		if yn := strings.ToLower(p.ask("  %s is already a command (%s). Use it anyway? [y/N] ", name, path)); yn != "y" && yn != "yes" {
			return false
		}
		forced[name] = true
	}
	return true
}

// run asks for both names; Enter keeps the default (the current names, else sprawl and sra).
// Without more input (EOF) the defaults are returned as they are, for the install to check.
func (p *namePrompt) run(command string, aliases []string) (string, []string, map[string]bool) {
	forced := map[string]bool{}
	for !p.eof {
		answer := p.ask("Command name [%s]: ", command)
		if answer == "" {
			answer = command
		}
		if p.accept(answer, forced) {
			command = answer
			break
		}
	}
	shown := strings.Join(aliases, ",")
	if shown == "" {
		shown = "-"
	}
	for !p.eof {
		answer := p.ask("Short alias [%s] ('-' for none): ", shown)
		if answer == "" {
			answer = shown
		}
		if answer == "-" {
			return command, nil, forced
		}
		list, ok := p.aliases(answer, command, forced)
		if ok {
			return command, list, forced
		}
	}
	if slices.Contains(aliases, command) {
		aliases = slices.DeleteFunc(slices.Clone(aliases), func(a string) bool { return a == command })
	}
	return command, aliases, forced
}

func (p *namePrompt) aliases(answer, command string, forced map[string]bool) ([]string, bool) {
	var list []string
	for _, a := range strings.Split(answer, ",") {
		a = strings.TrimSpace(a)
		switch {
		case a == "":
			continue
		case a == command:
			p.say("the alias must differ from the command name (%s)", command)
			return nil, false
		case slices.Contains(list, a):
			p.say("%s is listed twice", a)
			return nil, false
		case !p.accept(a, forced):
			return nil, false
		}
		list = append(list, a)
	}
	return list, true
}

// renameSummary describes a change of command names: "command: ai → sprawl (removed: ai, sx,
// added: sprawl, sra)"; "" when nothing changed.
func renameSummary(oldCmd string, oldAliases []string, newCmd string, newAliases []string) string {
	before := append([]string{oldCmd}, oldAliases...)
	after := append([]string{newCmd}, newAliases...)
	var removed, added []string
	for _, n := range before {
		if !slices.Contains(after, n) {
			removed = append(removed, n)
		}
	}
	for _, n := range after {
		if !slices.Contains(before, n) {
			added = append(added, n)
		}
	}
	if len(removed) == 0 && len(added) == 0 {
		return ""
	}
	none := func(l []string) string {
		if len(l) == 0 {
			return "none"
		}
		return strings.Join(l, ", ")
	}
	return fmt.Sprintf("command: %s → %s (removed: %s, added: %s)", oldCmd, newCmd, none(removed), none(added))
}
