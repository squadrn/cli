package guard

import (
	"regexp"
	"slices"
	"strings"
)

// ghRefWrite matches GitHub API paths that move, merge, rename or unprotect branches.
var ghRefWrite = regexp.MustCompile(`(?i)(^|/)git/refs?(/|\?|$)|/merges(/|\?|$)|/branches/.+/rename|/protection(/|\?|$)`)

// ghCommands are gh's own top-level commands; anything else is an alias or an extension.
var ghCommands = []string{
	"agent-task", "alias", "api", "attestation", "auth", "browse", "cache", "codespace", "completion", "config",
	"copilot", "extension", "ext", "gist", "gpg-key", "help", "issue", "label", "org", "pr", "preview",
	"project", "release", "repo", "ruleset", "run", "search", "secret", "ssh-key", "status", "variable",
	"version", "workflow", "--version", "--help", "-h",
}

// ghValueOpts are gh options that take the next word as their value.
var ghValueOpts = []string{"-R", "--repo", "--hostname"}

// ghOperand returns the first word that is not an option, and the words after it.
func ghOperand(args []arg) (*arg, []arg) {
	for i := 0; i < len(args); i++ {
		v := args[i].val
		switch {
		case args[i].dynamic && !strings.HasPrefix(v, "-"):
			return &args[i], args[i+1:]
		case slices.Contains(ghValueOpts, v):
			i++
		case strings.HasPrefix(v, "-"):
		default:
			return &args[i], args[i+1:]
		}
	}
	return nil, nil
}

func hasBoolFlag(args []arg, name string) bool {
	return slices.ContainsFunc(args, func(a arg) bool {
		return a.val == name || a.val == name+"=true" || a.val == name+"=1"
	})
}

// gh judges GitHub CLI commands that can change branches on the server, where layer 2 cannot
// see them.
func (e *extractor) gh(args []arg) {
	cmd, rest := ghOperand(args)
	if cmd == nil {
		return
	}
	if cmd.dynamic {
		e.flag(Ask, "a gh command known only at runtime cannot be checked")
		return
	}
	op, opRest := ghOperand(rest)
	opVal := ""
	if op != nil {
		opVal = op.val
	}
	switch c := strings.ToLower(cmd.val); c {
	case "api":
		e.ghAPI(rest)
	case "repo":
		switch opVal {
		case "sync":
			if hasBoolFlag(opRest, "--force") {
				e.flag(Deny, "gh repo sync --force overwrites the remote branch")
			} else {
				e.flag(Ask, "gh repo sync updates a branch on GitHub without a pull request")
			}
		case "delete":
			e.flag(Ask, "gh repo delete deletes a repository")
		case "edit":
			if slices.ContainsFunc(opRest, func(a arg) bool { return strings.HasPrefix(a.val, "--default-branch") }) {
				e.flag(Ask, "gh repo edit --default-branch changes which branch is protected by default")
			}
		}
	case "pr":
		if opVal == "merge" && hasBoolFlag(opRest, "--admin") {
			e.flag(Deny, "gh pr merge --admin bypasses branch protection")
		}
	case "ruleset":
		if !slices.Contains([]string{"", "list", "ls", "view", "check"}, opVal) {
			e.flag(Ask, "gh ruleset "+opVal+" changes branch rules")
		}
	case "alias":
		if opVal == "set" || opVal == "import" {
			e.flag(Ask, "gh alias "+opVal+" defines a command that can call the GitHub API or a shell")
		}
	default:
		if !slices.Contains(ghCommands, c) {
			e.flag(Ask, "gh "+cmd.val+" is an alias or extension, so what it does cannot be checked")
		}
	}
}

// ghAPI: reads are allowed; writes to branch refs, merges, renames or protection are denied;
// other writes ask.
func (e *extractor) ghAPI(args []arg) {
	method, fields := "", false
	var path *arg
	for i := 0; i < len(args); i++ {
		a := args[i]
		v := a.val
		next := func() string {
			if i+1 < len(args) {
				i++
				if args[i].dynamic {
					return "\x00"
				}
				return args[i].val
			}
			return ""
		}
		switch {
		case a.dynamic && strings.HasPrefix(v, "-"):
			e.flag(Ask, "a gh api option is only known at runtime")
			return
		case v == "-X" || v == "--method":
			method = next()
		case strings.HasPrefix(v, "--method="):
			method = strings.TrimPrefix(v, "--method=")
		case strings.HasPrefix(v, "-X"):
			method = v[2:]
		case v == "-f" || v == "-F" || v == "--field" || v == "--raw-field" || v == "--input":
			fields = true
			next()
		case strings.HasPrefix(v, "--field=") || strings.HasPrefix(v, "--raw-field=") || strings.HasPrefix(v, "--input=") ||
			strings.HasPrefix(v, "-f") || strings.HasPrefix(v, "-F"):
			fields = true
		case slices.Contains([]string{"-H", "--header", "-q", "--jq", "-t", "--template", "--hostname", "--cache", "-p", "--preview"}, v):
			next()
		case strings.HasPrefix(v, "-"):
		case path == nil:
			path = &args[i]
		}
	}
	if method == "\x00" {
		e.flag(Ask, "the gh api method is only known at runtime")
		return
	}
	method = strings.ToUpper(method)
	if method == "" {
		method = "GET"
		if fields {
			method = "POST"
		}
	}
	if method == "GET" || method == "HEAD" {
		return
	}
	switch {
	case path == nil:
		e.flag(Ask, "gh api "+method+" writes to GitHub")
	case path.dynamic:
		e.flag(Ask, "gh api "+method+" writes to a path known only at runtime")
	case ghRefWrite.MatchString(path.val):
		e.flag(Deny, "gh api "+method+" "+path.val+" changes branches or their protection directly; open a PR")
	default:
		e.flag(Ask, "gh api "+method+" "+path.val+" writes to GitHub")
	}
}
