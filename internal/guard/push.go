package guard

import (
	"context"
	"fmt"
	"strings"
)

// pushOpt describes one `git push` long option.
type pushOpt struct {
	name     string
	takesArg bool // the value may follow as the next word
	optional bool // --opt or --opt=value
	verdict  Verdict
	reason   string
	isDelete bool
	isRepo   bool
}

// pushOpts mirrors git push's long options. git accepts any unambiguous prefix (--forc is
// --force), so options are resolved by prefix exactly the way git does.
var pushOpts = []pushOpt{
	{name: "all", verdict: Deny, reason: "--all pushes every branch, including protected ones"},
	{name: "branches", verdict: Deny, reason: "--branches pushes every branch, including protected ones"},
	{name: "mirror", verdict: Deny, reason: "--mirror overwrites and deletes remote refs"},
	{name: "prune", verdict: Deny, reason: "--prune can delete remote branches, including protected ones"},
	{name: "force", verdict: Deny, reason: "force push is not allowed"},
	{name: "force-with-lease", optional: true, verdict: Deny, reason: "force push is not allowed"},
	{name: "force-if-includes", verdict: Deny, reason: "force push is not allowed"},
	{name: "no-verify", verdict: Deny, reason: "--no-verify skips the git-level guard and repository hooks"},
	{name: "receive-pack", takesArg: true, verdict: Ask, reason: "a custom --receive-pack program can do anything on the remote"},
	{name: "exec", takesArg: true, verdict: Ask, reason: "a custom --exec program can do anything on the remote"},
	{name: "delete", isDelete: true},
	{name: "repo", takesArg: true, isRepo: true},
	{name: "push-option", takesArg: true},
	{name: "recurse-submodules", takesArg: true},
	{name: "signed", optional: true},
	{name: "tags"},
	{name: "follow-tags"},
	{name: "dry-run"},
	{name: "porcelain"},
	{name: "verify"},
	{name: "atomic"},
	{name: "thin"},
	{name: "progress"},
	{name: "quiet"},
	{name: "verbose"},
	{name: "set-upstream"},
	{name: "ipv4"},
	{name: "ipv6"},
	{name: "no-force"},
	{name: "no-force-with-lease"},
	{name: "no-force-if-includes"},
	{name: "no-tags"},
	{name: "no-follow-tags"},
	{name: "no-atomic"},
	{name: "no-thin"},
	{name: "no-progress"},
	{name: "no-signed"},
	{name: "no-recurse-submodules"},
	{name: "no-set-upstream"},
}

// resolvePushOpt finds the option named by an exact name or a unique prefix.
func resolvePushOpt(name string) (pushOpt, bool) {
	var match []pushOpt
	for _, o := range pushOpts {
		if o.name == name {
			return o, true
		}
		if strings.HasPrefix(o.name, name) {
			match = append(match, o)
		}
	}
	if len(match) == 1 {
		return match[0], true
	}
	return pushOpt{}, false
}

// unknownPushOpt judges an unknown or ambiguous long option. git rejects ambiguous prefixes,
// so denying one that could be a denied option (--forc) costs nothing and gives a clear reason.
func unknownPushOpt(name, word string) Decision {
	for _, o := range pushOpts {
		if name != "" && strings.HasPrefix(o.name, name) && o.verdict == Deny {
			return deny(fmt.Sprintf("%s (%s)", o.reason, word))
		}
	}
	return ask(fmt.Sprintf("unrecognised git push option %s", word))
}

// shortPushFlags are git push's single-letter options; o takes a value.
const shortPushFlags = "fduvqn46o"

// decidePush judges one `git push`.
func (g Guard) decidePush(ctx context.Context, c gitCall, facts *factsCache) Decision {
	if c.configEnv || c.overrides["\x00dynamic"] != nil {
		return ask("git config is injected for this push (GIT_CONFIG_*, --exec-path or a runtime -c), so its target cannot be checked")
	}
	if c.overrides["core.hookspath"] != nil && g.GitHooks {
		return deny("overriding core.hooksPath for a push disables the git-level guard")
	}
	var positional []arg
	var repo *arg // --repo: every positional argument is then a refspec
	del := false
	for i := 0; i < len(c.args); i++ {
		a := c.args[i]
		v := a.val
		switch {
		case a.dynamic && strings.HasPrefix(v, "-"):
			return ask("a git push option is only known at runtime")
		case v == "--":
			positional = append(positional, c.args[i+1:]...)
			i = len(c.args)
		case strings.HasPrefix(v, "--"):
			name, _, hasValue := strings.Cut(v[2:], "=")
			o, ok := resolvePushOpt(name)
			if !ok {
				return unknownPushOpt(name, v)
			}
			if o.verdict != Allow {
				return Decision{Verdict: o.verdict, Reason: fmt.Sprintf("%s (%s)", o.reason, v)}
			}
			if o.isDelete {
				del = true
			}
			switch {
			case o.isRepo && hasValue:
				_, val, _ := strings.Cut(v, "=")
				repo = &arg{val: val, dynamic: a.dynamic}
			case o.isRepo && i+1 < len(c.args):
				repo = &c.args[i+1]
				i++
			case o.takesArg && !hasValue && !o.optional:
				i++
			}
		case strings.HasPrefix(v, "-") && len(v) > 1:
			flags := v[1:]
		shortFlags:
			for j := 0; j < len(flags); j++ {
				switch flags[j] {
				case 'f':
					return deny(fmt.Sprintf("force push is not allowed (%s)", v))
				case 'd':
					del = true
				case 'o': // push option: the rest of the word, or the next word
					if j == len(flags)-1 {
						i++
					}
					break shortFlags
				default:
					if !strings.ContainsRune(shortPushFlags, rune(flags[j])) {
						return ask(fmt.Sprintf("unrecognised git push option %s", v))
					}
				}
			}
		default:
			positional = append(positional, a)
		}
	}
	remote, refs := repo, positional
	if repo == nil && len(positional) > 0 {
		remote, refs = &positional[0], positional[1:]
	}
	if remote != nil && remote.dynamic {
		return ask("the git push remote is only known at runtime")
	}
	f, err := facts.get(ctx, c.dir)
	if err != nil {
		return ask("could not read the repository to check this push: " + err.Error())
	}
	protected := protectedSet(g.Protected, f)
	if f.NotRepo {
		// The directory may not exist yet (`cd x; git push …` runs git in the old directory when
		// cd fails), so explicit refspecs are still judged against the user's list.
		return g.decideRefspecs(refs, del, f, protected)
	}
	if len(refs) == 0 {
		return g.decideImplicit(c, remote, f, protected)
	}
	return g.decideRefspecs(refs, del, f, protected)
}

// decideRefspecs judges explicit refspecs.
func (g Guard) decideRefspecs(refs []arg, del bool, f Facts, protected branchMatcher) Decision {
	worst := allow()
	for i := 0; i < len(refs); i++ {
		r := refs[i]
		if r.val == "tag" && !r.dynamic && i+1 < len(refs) {
			i++ // `tag <name>` pushes refs/tags/<name>
			continue
		}
		d := judgeRefspec(r, del, f, protected)
		if d.Verdict > worst.Verdict {
			worst = d
		}
	}
	return worst
}

// judgeRefspec applies git's refspec rules to one argument.
func judgeRefspec(r arg, del bool, f Facts, protected branchMatcher) Decision {
	if r.dynamic {
		return ask(fmt.Sprintf("refspec %q is only known at runtime (variable, glob or brace expansion)", r.val))
	}
	spec := r.val
	if strings.HasPrefix(spec, "+") {
		return deny(fmt.Sprintf("force refspec %q is not allowed", spec))
	}
	if spec == ":" || spec == "" {
		return deny("the matching refspec \":\" pushes every matching branch, including protected ones")
	}
	if strings.Contains(spec, "*") {
		return deny(fmt.Sprintf("wildcard refspec %q can update protected branches", spec))
	}
	src, dst, hasColon := strings.Cut(spec, ":")
	switch {
	case hasColon && src == "":
		del = true
	case !hasColon && del:
		dst = src
	case !hasColon:
		if strings.ContainsAny(src, "~^") || strings.Contains(src, "@{") {
			return ask(fmt.Sprintf("refspec %q has no explicit destination", spec))
		}
		dst = src
	}
	if dst == "" {
		return ask(fmt.Sprintf("refspec %q has no destination", spec))
	}
	if dst == "HEAD" || dst == "@" {
		if f.Branch == "" {
			return ask("HEAD is detached, so the push destination cannot be determined")
		}
		dst = f.Branch
	}
	branch, isBranch := branchName(dst)
	if !isBranch || !protected.match(branch) {
		return allow()
	}
	if del {
		return deny(fmt.Sprintf("deleting protected branch %q is not allowed", branch))
	}
	return deny(fmt.Sprintf("direct push to protected branch %q is not allowed; open a PR", branch))
}

// branchName resolves a destination the way git does for pushes: refs/heads/x and heads/x are
// branch x, other refs/ namespaces (tags, Gerrit's refs/for/…) are not branches.
func branchName(dst string) (string, bool) {
	switch {
	case strings.HasPrefix(dst, "refs/heads/"):
		return strings.TrimPrefix(dst, "refs/heads/"), true
	case strings.HasPrefix(dst, "refs/"):
		return "", false
	case strings.HasPrefix(dst, "heads/"):
		return strings.TrimPrefix(dst, "heads/"), true
	}
	return dst, true
}

// decideImplicit judges `git push [remote]` without refspecs from the configuration git uses:
// remote.<r>.push, push.default and the branch's upstream.
func (g Guard) decideImplicit(c gitCall, remote *arg, f Facts, protected branchMatcher) Decision {
	cfg := func(key string) []string {
		if v, ok := c.overrides[key]; ok {
			return v
		}
		return f.Config[key]
	}
	remoteName := ""
	if remote != nil {
		remoteName = remote.val
	} else if f.Branch != "" {
		if r := cfg("branch." + strings.ToLower(f.Branch) + ".pushremote"); len(r) > 0 {
			remoteName = r[len(r)-1]
		} else if r := cfg("remote.pushdefault"); len(r) > 0 {
			remoteName = r[len(r)-1]
		} else if r := cfg("branch." + strings.ToLower(f.Branch) + ".remote"); len(r) > 0 {
			remoteName = r[len(r)-1]
		}
	}
	if remoteName == "" {
		remoteName = "origin"
	}
	if specs := cfg("remote." + strings.ToLower(remoteName) + ".push"); len(specs) > 0 {
		refs := make([]arg, len(specs))
		for i, s := range specs {
			refs[i] = arg{val: s}
		}
		return g.decideRefspecs(refs, false, f, protected)
	}
	mode := "simple"
	if m := cfg("push.default"); len(m) > 0 {
		mode = strings.ToLower(m[len(m)-1])
	}
	if mode == "matching" {
		return deny("push.default=matching pushes every matching branch, including protected ones")
	}
	if mode == "nothing" || f.Branch == "" {
		return allow() // git refuses the push on its own
	}
	if protected.match(f.Branch) {
		return deny(fmt.Sprintf("current branch %q is protected; push a feature branch and open a PR", f.Branch))
	}
	if merge := cfg("branch." + strings.ToLower(f.Branch) + ".merge"); len(merge) > 0 && (mode == "upstream" || mode == "tracking") {
		if b, ok := branchName(merge[len(merge)-1]); ok && protected.match(b) {
			return deny(fmt.Sprintf("this push goes to the upstream branch %q, which is protected", b))
		}
	}
	return allow()
}
