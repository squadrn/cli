package cli

import (
	"fmt"
	"slices"
	"strings"
)

// route is how one invocation is dispatched:
//
//	squadrn                                    pick an account interactively
//	squadrn [globals] [<account>] <subcommand> ...
//	squadrn [globals] [<account>] [agent flags | agent subcommand | -- args...]
//
// globals: -a/--account <account>, -C/--dir <dir>, --no-color, before or right after the account. Subcommands win over account names; a
// word that is neither (nor an agent subcommand) is a usage error, so a typo never starts an
// agent with the typo as its prompt. Prompts go after "--".
type route struct {
	account string // raw account input; "" means main
	dir     string // -C
	noColor bool
	sub     string // squadrn subcommand; "" runs the agent CLI with args
	args    []string
	pick    bool
}

type routeTable struct {
	isAccount func(string) bool
	// isUnmanaged reports a dir named like an account that is not one yet (account adopt).
	isUnmanaged func(string) bool
	subcommands map[string]bool
	// passthrough reports agent CLI subcommands (claude mcp …).
	passthrough func(string) bool
	// suggestions are offered for an unknown word.
	suggestions func() []string
	interactive bool
	cmd         string
}

func parseRoute(args []string, t routeTable) (route, error) {
	var r route
	rest, err := r.globals(args, false)
	if err != nil {
		return r, err
	}
	if len(rest) == 0 {
		r.pick = r.account == "" && t.interactive
		return r, nil
	}
	if done := r.dispatch(rest, t); done {
		return r, nil
	}
	if r.account == "" && t.isAccount(rest[0]) {
		r.account = rest[0]
		// Globals may also follow the account (squadrn personal -C ~/repo wt x); after them come
		// the subcommand or the agent's own arguments.
		if rest, err = r.globals(rest[1:], true); err != nil {
			return r, err
		}
		if len(rest) == 0 || r.dispatch(rest, t) {
			return r, nil
		}
	}
	if v := rest[0]; t.passthrough(v) || strings.ContainsAny(v, " \t\n") {
		r.args = rest // an agent subcommand, or a quoted prompt
		return r, nil
	}
	return r, unknownWord(rest[0], t)
}

// globals consumes squadrn's global flags at the start of args and returns the rest. After the
// account name (afterAccount), naming an account again is an error.
func (r *route) globals(args []string, afterAccount bool) ([]string, error) {
	rest := args
	for len(rest) > 0 {
		v := rest[0]
		name, value, inline := strings.Cut(v, "=")
		switch {
		case !inline && (v == "-a" || v == "--account" || v == "-C" || v == "--dir"):
			if len(rest) < 2 || rest[1] == "" {
				return nil, usagef("%s requires a value", v)
			}
			name, value, rest = v, rest[1], rest[2:]
		case inline && (name == "--account" || name == "--dir"):
			if value == "" {
				return nil, usagef("%s requires a value", name)
			}
			rest = rest[1:]
		case strings.HasPrefix(v, "-C") && len(v) > 2:
			name, value, rest = "-C", strings.TrimPrefix(v[2:], "="), rest[1:]
		case v == "--no-color":
			r.noColor, rest = true, rest[1:]
			continue
		default:
			return rest, nil
		}
		switch name {
		case "-C", "--dir":
			r.dir = value
		default:
			if afterAccount {
				return nil, usagef("the account is given twice (%s and %s %s)", r.account, name, value)
			}
			r.account = value
		}
	}
	return rest, nil
}

// dispatch handles what can follow the globals or the account; false means rest[0] is a word
// still to be classified.
func (r *route) dispatch(rest []string, t routeTable) bool {
	switch v := rest[0]; {
	case v == "--":
		r.args = rest[1:]
	case v == "-h" || v == "--help":
		r.sub, r.args = "help", rest[1:]
	case v == "--version" && r.account == "":
		r.sub, r.args = "version", rest[1:]
	case t.subcommands[v]:
		r.sub, r.args = v, rest[1:]
	case strings.HasPrefix(v, "-"):
		r.args = rest // agent flags: -p, --resume, …
	default:
		return false
	}
	return true
}

func unknownWord(word string, t routeTable) error {
	msg := fmt.Sprintf("unknown command or account %q", word)
	if s := suggest(word, t.suggestions()); len(s) > 0 {
		msg += fmt.Sprintf(" (did you mean %s?)", strings.Join(s, " or "))
	}
	if t.isUnmanaged != nil && t.isUnmanaged(word) {
		return usagef("%s\n  to give the agent a prompt: %s -- %q\n  to make the existing dir an account: %s account adopt %s", msg, t.cmd, word, t.cmd, word)
	}
	return usagef("%s\n  to give the agent a prompt: %s -- %q\n  to create an account:       %s account add %s", msg, t.cmd, word, t.cmd, word)
}

// suggest returns up to two candidates close to word: a shared prefix, or a small edit distance
// (1 for words of up to three letters, else 2).
func suggest(word string, candidates []string) []string {
	maxDist := 2
	if len(word) <= 3 {
		maxDist = 1
	}
	var out []string
	for _, c := range candidates {
		if c == word || slices.Contains(out, c) {
			continue
		}
		if (len(word) >= 2 && strings.HasPrefix(c, word)) || levenshtein(word, c) <= maxDist {
			out = append(out, c)
		}
		if len(out) == 2 {
			break
		}
	}
	return out
}

func levenshtein(a, b string) int {
	prev := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur := make([]int, len(b)+1)
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[len(b)]
}
