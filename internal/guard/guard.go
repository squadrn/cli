// Package guard decides whether a command an agent wants to run may push with git.
//
// It works in two layers. Decide (layer 1, the agent CLI's pre-tool hook) parses the shell
// command and judges every git push it can see; anything it cannot fully understand is asked
// about rather than allowed. DecidePrePush (layer 2, git's pre-push hook) sees the exact refs
// git is about to update, so it also catches pushes layer 1 cannot see (scripts, aliases,
// interpreters). Server-side branch protection remains the authoritative control.
package guard

import (
	"context"
	"fmt"

	"github.com/squadrn/cli/internal/config"
)

type Verdict int

const (
	Allow Verdict = iota
	Ask
	Deny
)

func (v Verdict) String() string {
	switch v {
	case Allow:
		return "allow"
	case Ask:
		return "ask"
	case Deny:
		return "deny"
	}
	return fmt.Sprintf("verdict(%d)", int(v))
}

type Decision struct {
	Verdict Verdict
	Reason  string
}

type Guard struct {
	// Protected is the user's protected branch list; repositories can only add to it.
	Protected []string
	Git       Git
	Home      string
	// GitHooks is set when the git-level guard is active; overriding core.hooksPath for a push
	// would switch it off, so that is denied.
	GitHooks bool
	// Names are the command names sprawl is installed under besides the defaults (configured
	// command and aliases); agents may not change the guard's policy through them.
	Names []string
	// Dirs are sprawl's own directories; zero means derived from Home. Writes to the config file
	// or the git hooks are denied, other writes into them ask.
	Dirs config.Dirs
}

// Decide judges a full command line run from cwd. The most severe verdict wins. ctx bounds all
// git queries; when it expires, or anything else fails, the answer is Ask, never Allow.
func (g Guard) Decide(ctx context.Context, cmd, cwd string) Decision {
	e := g.newExtractor(ctx)
	if err := e.script(cmd, cwd, 0); err != nil {
		return ask("the command could not be parsed, so a git push in it cannot be checked")
	}
	return g.verdict(ctx, e)
}

func (g Guard) newExtractor(ctx context.Context) *extractor {
	dirs := g.Dirs
	if dirs.Config == "" && g.Home != "" {
		dirs = config.DirsFor(g.Home, config.Slug)
	}
	names := map[string]bool{config.DefaultCommand: true, config.Slug + "-hook": true}
	for _, n := range append(config.DefaultAliases(), g.Names...) {
		names[cmdBase(n)] = true
	}
	return &extractor{ctx: ctx, home: g.Home, facts: newFactsCache(g.Git), dirs: dirs, names: names}
}

// verdict combines what the analysis found: the most severe finding or push decision wins, and
// an expired deadline asks.
func (g Guard) verdict(ctx context.Context, e *extractor) Decision {
	worst := allow()
	for _, f := range e.findings {
		if f.verdict > worst.Verdict {
			worst = Decision{Verdict: f.verdict, Reason: f.reason}
		}
	}
	for _, c := range e.calls {
		if worst.Verdict == Deny {
			break
		}
		if d := g.decidePush(ctx, c, e.facts); d.Verdict > worst.Verdict {
			worst = d
		}
	}
	if worst.Verdict != Deny && ctx.Err() != nil {
		return ask("the guard ran out of time; confirm this command manually")
	}
	return worst
}

func allow() Decision { return Decision{Verdict: Allow} }

func deny(reason string) Decision { return Decision{Verdict: Deny, Reason: reason} }

func ask(reason string) Decision { return Decision{Verdict: Ask, Reason: reason} }
