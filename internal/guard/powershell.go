package guard

import (
	"context"
	"path"
	"regexp"
	"slices"
	"strings"
)

// PowerShell commands come from Claude Code's PowerShell tool on Windows. PowerShell has no Go
// parser, so this analyser is an allow-list (ADR 0008): it understands plain statements — literal
// git invocations, directory changes, assignments — and asks about anything that can run a
// program in a way it does not follow, whether or not the text mentions push. Literal git calls go
// through the same push judgement as the shell parser.

// DecidePowerShell judges a PowerShell command run from cwd, like Decide does for shell commands.
func (g Guard) DecidePowerShell(ctx context.Context, cmd, cwd string) Decision {
	e := &extractor{ctx: ctx, home: g.Home, facts: newFactsCache(g.Git)}
	e.powershell(cmd, cwd, 0)
	return g.verdict(ctx, e)
}

// psWord is one word of a statement. dynamic: its value is only known at runtime (variables,
// subexpressions, escapes, splatting, script blocks).
type psWord struct {
	val     string
	dynamic bool
}

// psExecutors run programs, code or scripts that the analyser cannot follow.
var psExecutors = map[string]string{
	"iex": "Invoke-Expression", "invoke-expression": "Invoke-Expression",
	"icm": "Invoke-Command", "invoke-command": "Invoke-Command",
	"saps": "Start-Process", "start-process": "Start-Process", "start": "Start-Process",
	"start-job": "Start-Job", "sajb": "Start-Job", "start-threadjob": "Start-ThreadJob",
	"invoke-item": "Invoke-Item", "ii": "Invoke-Item",
	"add-type": "Add-Type", "new-object": "New-Object",
	"set-alias": "Set-Alias", "sal": "Set-Alias", "new-alias": "New-Alias", "nal": "New-Alias",
	"function": "a function definition", "filter": "a filter definition",
	"cmd": "cmd.exe", "wsl": "wsl", "runas": "runas", "schtasks": "schtasks",
	"register-scheduledtask": "a scheduled task", "new-scheduledtaskaction": "a scheduled task",
	"invoke-cimmethod": "CIM", "invoke-wmimethod": "WMI", "start-scheduledtask": "a scheduled task",
}

var gitBase = regexp.MustCompile(`^git(-[a-z-]+)?(\.exe)?$`)

// psName normalises a command word: lower case, last path element, Windows separators.
func psName(v string) string {
	return strings.ToLower(path.Base(strings.ReplaceAll(v, `\`, "/")))
}

func (e *extractor) powershell(src, cwd string, depth int) {
	if depth > maxDepth {
		e.flag(Ask, "commands nest too deeply to check")
		return
	}
	src = normalizePS(src)
	if strings.Contains(src, "--%") {
		if containsFold(src, "git") || containsFold(src, "push") {
			e.flag(Ask, "--% stops PowerShell parsing, so the arguments cannot be checked")
		}
		return
	}
	dir := cwd
	for _, st := range splitPS(src) {
		e.psStatement(st, &dir, depth)
	}
}

// normalizePS maps the Unicode quotes and dashes PowerShell accepts to their ASCII forms.
func normalizePS(s string) string {
	return strings.NewReplacer(
		"‘", "'", "’", "'", "‚", "'", "‛", "'",
		"“", `"`, "”", `"`, "„", `"`,
		"–", "-", "—", "-", "―", "-",
	).Replace(s)
}

func containsFold(s, sub string) bool { return strings.Contains(strings.ToLower(s), sub) }

func (e *extractor) psStatement(words []psWord, dir *string, depth int) {
	if len(words) == 0 {
		return
	}
	text := joinPS(words)
	first := words[0]

	// Assignment: $x = <pipeline>. The right side runs like any statement; $env:GIT_* changes
	// git's configuration for later commands.
	if psVariable.MatchString(first.val) && len(words) > 1 && isAssign(words[1].val) {
		if strings.HasPrefix(strings.ToLower(first.val), "$env:git_") {
			e.flag(Ask, "setting "+first.val+" changes git's configuration and can disable the git-level guard")
		}
		e.psStatement(words[2:], dir, depth)
		return
	}
	// A subexpression or script block anywhere — $(git push), { git push }, (git push) — runs
	// code the analyser does not follow.
	for _, w := range words {
		if w.dynamic && strings.ContainsAny(w.val, "({") && (containsFold(w.val, "git") || containsFold(w.val, "push")) {
			e.flag(Ask, "a PowerShell subexpression or script block may run git in a way that cannot be checked")
			return
		}
	}
	if first.dynamic {
		// (git push), $(…), a variable used as a command, …
		if containsFold(text, "git") || containsFold(text, "push") {
			e.flag(Ask, "a PowerShell expression may run git in a way that cannot be checked")
		}
		return
	}

	// & <command>, . <script>: the call operators.
	if first.val == "&" || first.val == "." {
		if len(words) < 2 {
			return
		}
		callee := words[1]
		if callee.dynamic {
			e.flag(Ask, "the call operator runs a command known only at runtime")
			return
		}
		if gitBase.MatchString(psName(callee.val)) {
			e.psGit(callee, words[2:], *dir, depth)
		}
		// A script run by path is outside this guard's view; the git-level guard covers it.
		return
	}

	name := psName(first.val)
	switch {
	case gitBase.MatchString(name):
		e.psGit(first, words[1:], *dir, depth)
	case name == "cd" || name == "chdir" || name == "sl" || name == "set-location" ||
		name == "pushd" || name == "push-location":
		if t := psOperand(words[1:], "-path", "-literalpath"); t != nil && !t.dynamic {
			*dir = e.resolve(*dir, t.val)
		}
	case name == "bash" || name == "sh" || name == "bash.exe" || name == "sh.exe":
		e.psShell(name, words[1:], *dir, depth)
	case name == "pwsh" || name == "powershell" || name == "pwsh.exe" || name == "powershell.exe":
		e.psNested(words[1:], *dir, depth)
	case psEnvWrite(name, words[1:]):
		e.flag(Ask, "changing GIT_* environment variables can disable the git-level guard")
	default:
		if what, ok := psExecutors[name]; ok {
			e.flag(Ask, what+" can run git in a way that cannot be checked")
			return
		}
		if strings.Contains(strings.ToLower(text), "setenvironmentvariable") && containsFold(text, "git_") {
			e.flag(Ask, "changing GIT_* environment variables can disable the git-level guard")
			return
		}
		if !psInert(name) && slices.ContainsFunc(words[1:], func(w psWord) bool { return containsFold(w.val, "push") }) {
			e.flag(Ask, "an unrecognised command with push in its arguments (an alias for git?)")
		}
	}
}

var psVariable = regexp.MustCompile(`^\$[A-Za-z_][\w:]*$`)

// psInert reports cmdlets that only produce or filter data: an argument mentioning push is text.
func psInert(name string) bool {
	switch name {
	case "echo", "write", "cat", "type", "gc", "sls", "findstr", "grep", "ls", "dir", "gci", "more", "sort":
		return true
	}
	for _, verb := range []string{
		"get-", "write-", "out-", "select-", "sort-", "format-", "measure-", "test-",
		"convertto-", "convertfrom-", "compare-", "group-", "set-content", "add-content",
	} {
		if strings.HasPrefix(name, verb) {
			return true
		}
	}
	return false
}

func isAssign(v string) bool {
	switch v {
	case "=", "+=", "-=", "*=", "/=", "%=", "??=":
		return true
	}
	return false
}

// psGit judges a literal git invocation; any word known only at runtime asks.
func (e *extractor) psGit(callee psWord, args []psWord, dir string, depth int) {
	argv := make([]arg, 0, len(args))
	for _, w := range args {
		if w.dynamic {
			e.flag(Ask, "a git argument is only known at runtime")
			return
		}
		argv = append(argv, arg{val: w.val})
	}
	name := strings.TrimSuffix(psName(callee.val), ".exe")
	if strings.HasPrefix(name, "git-") {
		_ = e.gitSub(strings.TrimPrefix(name, "git-"), argv, dir, nil, false, depth)
		return
	}
	_ = e.git(argv, dir, false, depth, nil)
}

// psShell follows bash/sh -c '<script>' with the shell parser; anything else asks if git may run.
func (e *extractor) psShell(name string, args []psWord, dir string, depth int) {
	for i, w := range args {
		if !w.dynamic && w.val == "-c" && i+1 < len(args) {
			if args[i+1].dynamic {
				e.flag(Ask, name+" -c runs a script built at runtime")
				return
			}
			if err := e.script(args[i+1].val, dir, depth+1); err != nil {
				e.flag(Ask, name+" -c runs a script that could not be parsed")
			}
			return
		}
	}
	if len(args) == 0 {
		e.flag(Ask, "a shell reads its commands from stdin, which cannot be checked")
	}
}

// psNested follows pwsh/powershell -Command '<code>'; encoded commands ask, -File scripts are
// left to the git-level guard. Parameters may be abbreviated and written with / on Windows.
func (e *extractor) psNested(args []psWord, dir string, depth int) {
	prefix := func(opt, full string, min int) bool { return len(opt) >= min && strings.HasPrefix(full, opt) }
	for i, w := range args {
		v := strings.ToLower(w.val)
		if w.dynamic || !strings.HasPrefix(v, "-") && !strings.HasPrefix(v, "/") {
			continue
		}
		switch opt := strings.TrimLeft(v, "-/"); {
		case opt == "e" || opt == "ec" || prefix(opt, "encodedcommand", 2) && opt != "ex":
			e.flag(Ask, "an encoded PowerShell command cannot be checked")
			return
		case opt == "c" || opt == "cwa" || prefix(opt, "command", 2) || prefix(opt, "commandwithargs", 8):
			if i+1 >= len(args) || args[i+1].dynamic {
				e.flag(Ask, "PowerShell runs a command known only at runtime")
				return
			}
			e.powershell(args[i+1].val, dir, depth+1)
			return
		case opt == "f" || prefix(opt, "file", 2):
			return
		}
	}
}

// psEnvWrite reports Remove-Item/Set-Item/Clear-Item on Env:GIT_* (and their aliases).
func psEnvWrite(name string, args []psWord) bool {
	switch name {
	case "remove-item", "ri", "rm", "del", "erase", "rd", "rmdir", "set-item", "si", "clear-item", "cli", "new-item", "ni":
	default:
		return false
	}
	for _, w := range args {
		v := strings.ToLower(w.val)
		if strings.HasPrefix(v, "env:") && (strings.HasPrefix(v, "env:git") || strings.Contains(v, "*")) {
			return true
		}
	}
	return false
}

// psOperand returns the value of one of the named parameters, else the first positional word.
func psOperand(words []psWord, params ...string) *psWord {
	for i := 0; i < len(words); i++ {
		v := strings.ToLower(words[i].val)
		if slices.Contains(params, v) && i+1 < len(words) {
			return &words[i+1]
		}
		if !strings.HasPrefix(v, "-") {
			return &words[i]
		}
	}
	return nil
}

func joinPS(words []psWord) string {
	vals := make([]string, len(words))
	for i, w := range words {
		vals[i] = w.val
	}
	return strings.Join(vals, " ")
}

// splitPS tokenises src into statements of words. Statements end at ; | && || and newlines
// outside quotes; # starts a comment. Single quotes are literal (” is a quote); double quotes
// and here-strings are dynamic when they interpolate ($, backtick). Unquoted $, @, (, ), {, },
// [, ], backticks and commas make a word dynamic.
func splitPS(src string) [][]psWord {
	var stmts [][]psWord
	var cur []psWord
	var w strings.Builder
	inWord, dyn := false, false
	flushWord := func() {
		if inWord {
			cur = append(cur, psWord{val: w.String(), dynamic: dyn})
		}
		w.Reset()
		inWord, dyn = false, false
	}
	flushStmt := func() {
		flushWord()
		if len(cur) > 0 {
			stmts = append(stmts, cur)
		}
		cur = nil
	}
	rs := []rune(src)
	for i := 0; i < len(rs); i++ {
		c := rs[i]
		switch {
		case c == '@' && i+1 < len(rs) && (rs[i+1] == '\'' || rs[i+1] == '"') && hereStart(rs, i+2):
			// Here-string: @' … '@ (literal) or @" … "@ (expandable), up to a line starting the end.
			q := rs[i+1]
			end := strings.Index(string(rs[i+2:]), "\n"+string(q)+"@")
			if end < 0 {
				end = len(string(rs[i+2:]))
			}
			body := string(rs[i+2:])[:end]
			inWord = true
			w.WriteString(body)
			if q == '"' && strings.ContainsAny(body, "$`") {
				dyn = true
			}
			i += 2 + len([]rune(body)) + 2
		case c == '\'':
			inWord = true
			for i++; i < len(rs); i++ {
				if rs[i] == '\'' {
					if i+1 < len(rs) && rs[i+1] == '\'' {
						w.WriteRune('\'')
						i++
						continue
					}
					break
				}
				w.WriteRune(rs[i])
			}
		case c == '"':
			inWord = true
			for i++; i < len(rs); i++ {
				if rs[i] == '`' && i+1 < len(rs) {
					dyn = true
					i++
					w.WriteRune(rs[i])
					continue
				}
				if rs[i] == '"' {
					if i+1 < len(rs) && rs[i+1] == '"' {
						w.WriteRune('"')
						i++
						continue
					}
					break
				}
				if rs[i] == '$' {
					dyn = true
				}
				w.WriteRune(rs[i])
			}
		case c == '#' && !inWord:
			for i < len(rs) && rs[i] != '\n' {
				i++
			}
			flushStmt()
		case c == '\n' || c == ';':
			flushStmt()
		case c == '|':
			if i+1 < len(rs) && rs[i+1] == '|' {
				i++
			}
			flushStmt()
		case c == '&' && i+1 < len(rs) && rs[i+1] == '&':
			i++
			flushStmt()
		case c == ' ' || c == '\t' || c == '\r':
			flushWord()
		case c == '&' && !inWord:
			if len(cur) == 0 {
				cur = append(cur, psWord{val: "&"}) // call operator
			} else {
				flushStmt() // background operator ends the pipeline
			}
		case c == '`':
			inWord, dyn = true, true
			if i+1 < len(rs) {
				i++
				w.WriteRune(rs[i])
			}
		case strings.ContainsRune("$@(){}[],", c):
			inWord, dyn = true, true
			w.WriteRune(c)
		default:
			inWord = true
			w.WriteRune(c)
		}
	}
	flushStmt()
	return stmts
}

// hereStart: a here-string opener is followed only by spaces up to the end of the line.
func hereStart(rs []rune, i int) bool {
	for ; i < len(rs); i++ {
		switch rs[i] {
		case ' ', '\t', '\r':
		case '\n':
			return true
		default:
			return false
		}
	}
	return false
}
