package guard

import (
	"slices"
	"strings"
)

// gitConfigValueOpts are git config options that take the next word as their value.
var gitConfigValueOpts = []string{"-f", "--file", "--blob", "--type", "--default", "--comment", "--value", "--url"}

// gitConfig judges `git config`: defining aliases or programs git runs asks, and writing
// settings git push reads marks later pushes on the same command line as uncertain.
func (e *extractor) gitConfig(args []arg) {
	var positional []arg
	mode := ""
	for i := 0; i < len(args); i++ {
		a := args[i]
		v := a.val
		switch {
		case a.dynamic && strings.HasPrefix(v, "-"):
			e.flag(Ask, "a git config option is only known at runtime")
			return
		case v == "--":
			positional = append(positional, args[i+1:]...)
			i = len(args)
		case !strings.HasPrefix(v, "-") || v == "-" || a.dynamic:
			positional = append(positional, a)
		case slices.Contains(gitConfigValueOpts, v):
			i++
		case slices.Contains([]string{"--get", "--get-all", "--get-regexp", "--get-urlmatch", "--get-color", "--get-colorbool", "-l", "--list"}, v):
			mode = "get"
		case v == "--add" || v == "--replace-all" || v == "--append":
			mode = "set"
		case v == "--unset" || v == "--unset-all":
			mode = "unset"
		case v == "-e" || v == "--edit":
			mode = "edit"
		case v == "--rename-section":
			mode = "rename-section"
		case v == "--remove-section":
			mode = "remove-section"
		}
	}
	if mode == "" && len(positional) > 0 && !positional[0].dynamic {
		switch positional[0].val {
		case "set", "unset", "get", "list", "edit", "rename-section", "remove-section":
			mode, positional = positional[0].val, positional[1:]
		}
	}
	switch mode {
	case "get", "list", "remove-section":
		return
	case "edit":
		e.flag(Ask, "git config --edit opens an editor that can change any setting")
		return
	case "rename-section":
		e.flag(Ask, "git config --rename-section can turn a section into aliases or programs git runs")
		return
	case "":
		if len(positional) < 2 {
			return // reads one value
		}
		mode = "set"
	}
	if len(positional) == 0 {
		return
	}
	key := positional[0]
	if key.dynamic {
		e.flag(Ask, "git config writes a setting known only at runtime")
		return
	}
	k := strings.ToLower(key.val)
	if pushKey(k) {
		e.pushConfigChanged = true
	}
	if mode != "set" {
		return
	}
	switch {
	case strings.HasPrefix(k, "alias."):
		e.flag(Ask, "git config "+key.val+" defines a git alias, which can run any command")
	case commandKey(k):
		e.flag(Ask, "git config "+key.val+" sets a program git runs, which cannot be checked")
	}
}

// splitKey splits section.subsection.name; subsection may contain dots.
func splitKey(k string) (section, sub, name string) {
	first, last := strings.Index(k, "."), strings.LastIndex(k, ".")
	if first < 0 {
		return k, "", ""
	}
	section, name = k[:first], k[last+1:]
	if last > first {
		sub = k[first+1 : last]
	}
	return section, sub, name
}

// commandKey reports git settings (lower-cased) whose value is a program, a shell command, an
// alias or more configuration: git runs them, so they can do anything, including a push.
func commandKey(k string) bool {
	section, sub, name := splitKey(k)
	switch section {
	case "alias", "filter", "pager", "includeif":
		return true
	case "core":
		return slices.Contains([]string{"fsmonitor", "sshcommand", "pager", "editor", "askpass", "hookspath", "gitproxy", "alternaterefscommand"}, name)
	case "sequence":
		return name == "editor"
	case "include":
		return name == "path"
	case "credential":
		return name == "helper"
	case "diff":
		return name == "command" || name == "textconv" || sub == "" && name == "external"
	case "merge":
		return name == "driver"
	case "gpg":
		return name == "program" || name == "defaultkeycommand"
	case "protocol":
		return name == "allow"
	case "remote":
		return name == "uploadpack" || name == "receivepack" || name == "vcs"
	case "uploadpack":
		return name == "packobjectshook"
	case "difftool", "mergetool":
		return name == "cmd"
	case "browser", "man":
		return name == "cmd" || name == "path"
	case "sendemail":
		return strings.HasSuffix(name, "cmd")
	case "interactive":
		return name == "difffilter"
	}
	return false
}

// pushKey reports settings (lower-cased) that decide where a git push goes.
func pushKey(k string) bool {
	section, sub, name := splitKey(k)
	switch section {
	case "push":
		return name == "default"
	case "remote":
		return name == "push" || name == "mirror" || sub == "" && name == "pushdefault"
	case "branch":
		return name == "merge" || name == "remote" || name == "pushremote"
	case "url":
		return name == "pushinsteadof" || name == "insteadof"
	}
	return false
}

// setsUpstream reports git branch/checkout/switch options that set a branch's upstream.
func setsUpstream(args []arg) bool {
	for _, a := range args {
		v := a.val
		if v == "-u" || v == "-t" || v == "--track" || strings.HasPrefix(v, "--track=") ||
			strings.HasPrefix(v, "--set-upstream-to") || v == "--set-upstream" {
			return true
		}
	}
	return false
}

// subtree judges `git subtree push <repo> <ref>`: a push of a split commit to <ref>.
func (e *extractor) subtree(c gitCall) {
	if len(c.args) == 0 || c.args[0].val != "push" {
		return
	}
	var positional []arg
	args := c.args[1:]
	for i := 0; i < len(args); i++ {
		v := args[i].val
		switch {
		case args[i].dynamic && strings.HasPrefix(v, "-"):
			e.flag(Ask, "a git subtree option is only known at runtime")
			return
		case slices.Contains([]string{"-P", "--prefix", "-b", "--branch", "-m", "--message", "--onto"}, v):
			i++
		case strings.HasPrefix(v, "-") && v != "-":
		default:
			positional = append(positional, args[i])
		}
	}
	if len(positional) < 2 {
		e.flag(Ask, "git subtree push without a remote and a branch cannot be checked")
		return
	}
	ref := positional[1]
	if strings.HasPrefix(ref.val, "+") {
		e.flag(Deny, "force refspec "+ref.val+" is not allowed")
		return
	}
	c.sub = "push"
	c.args = []arg{positional[0], {val: "HEAD:" + ref.val, dynamic: ref.dynamic}}
	e.calls = append(e.calls, c)
}
