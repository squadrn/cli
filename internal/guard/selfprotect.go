package guard

import (
	"path/filepath"
	"slices"
	"strings"

	"mvdan.cc/sh/v3/syntax"

	"github.com/squadrn/cli/internal/config"
	"github.com/squadrn/cli/internal/githook"
)

// policyKeys are the settings that weaken the guard; only the user changes them, in a terminal.
var policyKeys = []string{"protected_branches", "git_hooks"}

// self denies `squadrn config set protected_branches|git_hooks …` (under any of its names).
func (e *extractor) self(args []arg) {
	key := -1
	for i, a := range args {
		if slices.ContainsFunc(policyKeys, func(k string) bool { return strings.Contains(strings.ToLower(a.val), k) }) {
			key = i
			break
		}
	}
	if key < 0 {
		return
	}
	if indexOf(args[:key], "config") >= 0 && indexOf(args[:key], "set") >= 0 {
		e.flag(Deny, "agents may not change the push guard's policy ("+args[key].val+"); the user changes it in a terminal")
		return
	}
	if slices.ContainsFunc(args, func(a arg) bool { return a.dynamic }) {
		e.flag(Ask, "a squadrn command built at runtime may change the push guard's policy")
	}
}

// pathVerdict judges a write to p: the config file and the git hooks are denied, anything else
// in squadrn's directories (or those of a previous product name) asks.
func (e *extractor) pathVerdict(p arg, dir string) (Verdict, string) {
	if e.dirs.Config == "" {
		return Allow, ""
	}
	const (
		denied = "writing to squadrn's config file or git hooks would weaken the push guard"
		asked  = "a write into squadrn's own directories; confirm it does not weaken the push guard"
	)
	if p.dynamic {
		v := strings.ToLower(strings.ReplaceAll(p.val, `\`, "/"))
		for _, n := range append([]string{config.Slug}, config.PreviousNames...) {
			if strings.Contains(v, n+"/config.toml") || strings.Contains(v, n+"/git-hooks") {
				return Deny, denied
			}
		}
		for _, n := range append([]string{config.Slug}, config.PreviousNames...) {
			if strings.Contains(v, "/"+n) {
				return Ask, asked
			}
		}
		return Allow, ""
	}
	target := e.resolve(dir, strings.ReplaceAll(p.val, `\`, "/"))
	for _, d := range append([]config.Dirs{e.dirs}, e.legacyDirs...) {
		for _, guarded := range []string{filepath.Join(d.Config, "config.toml"), githook.Dir(d.Data)} {
			if within(target, guarded) || within(guarded, target) && (within(target, d.Config) || within(target, d.Data)) {
				return Deny, denied
			}
		}
	}
	for _, d := range append([]config.Dirs{e.dirs}, e.legacyDirs...) {
		for _, sub := range []string{d.Config, d.Data, d.State} {
			if within(target, sub) {
				return Ask, asked
			}
		}
	}
	return Allow, ""
}

// within reports p equal to or below dir (case-insensitive: macOS and Windows file systems).
func within(p, dir string) bool {
	p, dir = strings.ToLower(filepath.Clean(p)), strings.ToLower(filepath.Clean(dir))
	return p == dir || strings.HasPrefix(p, dir+string(filepath.Separator))
}

func (e *extractor) writeTo(p arg, dir string) {
	if v, reason := e.pathVerdict(p, dir); v != Allow {
		e.flag(v, reason)
	}
}

// redirects judges output redirections of a statement.
func (e *extractor) redirects(redirs []*syntax.Redirect, dir string) {
	for _, r := range redirs {
		switch r.Op {
		case syntax.RdrOut, syntax.AppOut, syntax.RdrAll, syntax.AppAll, syntax.RdrClob, syntax.RdrInOut:
			if r.Word != nil {
				e.writeTo(wordArg(r.Word), dir)
			}
		}
	}
}

func isFileWriter(base string) bool {
	switch base {
	case "tee", "sed", "gsed", "cp", "install", "ln", "rsync", "mv", "rm", "rmdir", "unlink", "shred", "truncate",
		"chmod", "chown", "chgrp", "chflags", "chattr", "touch", "dd", "xattr", "setfacl":
		return true
	}
	return false
}

// fileWrite judges commands that write, move, delete or change the mode of files.
func (e *extractor) fileWrite(base string, args []arg, dir string) {
	var operands []arg
	target := -1
	for i := 0; i < len(args); i++ {
		v := args[i].val
		switch {
		case base == "dd" && strings.HasPrefix(v, "of="):
			e.writeTo(arg{val: strings.TrimPrefix(v, "of="), dynamic: args[i].dynamic}, dir)
		case (v == "-t" || v == "--target-directory") && i+1 < len(args):
			target = len(operands)
			operands = append(operands, args[i+1])
			i++
		case strings.HasPrefix(v, "--target-directory="):
			target = len(operands)
			operands = append(operands, arg{val: strings.TrimPrefix(v, "--target-directory="), dynamic: args[i].dynamic})
		case strings.HasPrefix(v, "-") && !args[i].dynamic && v != "-":
		default:
			operands = append(operands, args[i])
		}
	}
	switch base {
	case "sed", "gsed":
		if !slices.ContainsFunc(args, func(a arg) bool {
			return strings.HasPrefix(a.val, "--in-place") || strings.HasPrefix(a.val, "-") && !strings.HasPrefix(a.val, "--") && strings.Contains(a.val, "i")
		}) {
			return
		}
	case "cp", "install", "ln", "rsync":
		// Only the destination is written.
		switch {
		case target >= 0:
			operands = operands[target : target+1]
		case len(operands) > 0:
			operands = operands[len(operands)-1:]
		}
	case "dd":
		return
	}
	for _, o := range operands {
		e.writeTo(o, dir)
	}
}

// psRedirects judges PowerShell output redirections: > file, >> file, 2> file, *>file.
func (e *extractor) psRedirects(words []psWord, dir string) {
	for i, w := range words {
		j := strings.LastIndex(w.val, ">")
		if j < 0 || w.dynamic && !strings.HasPrefix(w.val, ">") {
			continue
		}
		target := psWord{val: w.val[j+1:], dynamic: w.dynamic}
		if target.val == "" {
			if i+1 >= len(words) {
				continue
			}
			target = words[i+1]
		}
		if strings.HasPrefix(target.val, "&") {
			continue // 2>&1
		}
		e.writeTo(arg(target), dir)
	}
}

func psFileWriter(name string) bool {
	switch name {
	case "set-content", "sc", "add-content", "ac", "out-file", "clear-content", "clc", "copy-item", "copy", "cp", "cpi",
		"move-item", "move", "mv", "mi", "remove-item", "ri", "rm", "del", "erase", "rd", "rmdir", "rename-item", "ren",
		"rni", "new-item", "ni", "set-itemproperty", "sp", "set-acl", "tee-object", "tee":
		return true
	}
	return false
}

// psFileWrite judges cmdlets that write, move or delete files. Copies only write their
// destination; the others touch every path they name.
func (e *extractor) psFileWrite(name string, words []psWord, dir string) {
	var positional []psWord
	var dest *psWord
	for i := 0; i < len(words); i++ {
		v := strings.ToLower(words[i].val)
		switch {
		case !words[i].dynamic && len(v) > 2 && i+1 < len(words) && isPrefixOf(v, "-destination"):
			dest = &words[i+1]
			i++
		case !words[i].dynamic && slices.Contains([]string{"-path", "-literalpath", "-filepath", "-lp", "-pspath", "-value", "-name", "-itemtype", "-encoding", "-newname"}, v) && i+1 < len(words):
			if v != "-value" && v != "-itemtype" && v != "-encoding" {
				positional = append(positional, words[i+1])
			}
			i++
		case !words[i].dynamic && strings.HasPrefix(v, "-"):
		default:
			positional = append(positional, words[i])
		}
	}
	switch name {
	case "copy-item", "copy", "cp", "cpi":
		if dest == nil && len(positional) > 1 {
			dest = &positional[len(positional)-1]
		}
		if dest != nil {
			e.writeTo(arg(*dest), dir)
		}
		return
	case "set-content", "sc", "add-content", "ac", "out-file", "clear-content", "clc", "tee-object", "tee":
		// The first positional is the path; the rest is the content.
		positional = positional[:min(1, len(positional))]
	}
	if dest != nil {
		positional = append(positional, *dest)
	}
	for _, p := range positional {
		e.writeTo(arg(p), dir)
	}
}

// isPrefixOf reports an abbreviated PowerShell parameter: v is a prefix of full.
func isPrefixOf(v, full string) bool { return strings.HasPrefix(full, v) }
