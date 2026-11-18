package guard

import (
	"context"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// maxDepth bounds recursion through nested shells, eval, aliases and here-documents.
const maxDepth = 6

// arg is one shell word reduced to its literal value. A dynamic word depends on runtime values
// ($VAR, $(…), globs, brace expansion); its value cannot be judged statically.
type arg struct {
	val     string
	dynamic bool
}

// gitCall is one git invocation a command line would run.
type gitCall struct {
	dir string
	sub string // subcommand, lower-cased
	// args are the words after the subcommand.
	args []arg
	// overrides are -c / GIT_CONFIG_* settings for this call; keys are lower-cased.
	overrides map[string][]string
	// configEnv marks git config injected through the environment (GIT_CONFIG_*, GIT_EXEC_PATH).
	configEnv bool
}

// finding is a reason to ask or deny that is found while walking the command, before any
// git semantics are applied.
type finding struct {
	verdict Verdict
	reason  string
}

type extractor struct {
	ctx      context.Context
	home     string
	facts    *factsCache
	calls    []gitCall
	findings []finding
	// sessionConfigEnv is set once the command line exports or unsets git config variables;
	// it applies to every later git call.
	sessionConfigEnv bool
}

func (e *extractor) flag(v Verdict, reason string) {
	e.findings = append(e.findings, finding{v, reason})
}

func (e *extractor) script(src, cwd string, depth int) error {
	if depth > maxDepth {
		e.flag(Ask, "commands nest too deeply to check")
		return nil
	}
	f, err := syntax.NewParser(syntax.Variant(syntax.LangBash)).Parse(strings.NewReader(src), "")
	if err != nil {
		return err
	}
	dir := cwd
	var walkErr error
	syntax.Walk(f, func(n syntax.Node) bool {
		if walkErr != nil {
			return false
		}
		switch node := n.(type) {
		case *syntax.DeclClause:
			// export GIT_CONFIG_COUNT=… / declare -x … affect every later command.
			for _, a := range node.Args {
				if a.Name != nil && isGitConfigEnv(a.Name.Value) {
					e.sessionConfigEnv = true
				}
			}
		case *syntax.Stmt:
			call, ok := node.Cmd.(*syntax.CallExpr)
			if !ok {
				return true
			}
			if len(call.Args) == 0 {
				for _, a := range call.Assigns {
					if a.Name != nil && isGitConfigEnv(a.Name.Value) {
						e.sessionConfigEnv = true
					}
				}
				return true
			}
			configEnv := false
			for _, a := range call.Assigns {
				if a.Name != nil && isGitConfigEnv(a.Name.Value) {
					configEnv = true
				}
			}
			walkErr = e.call(words(call.Args), node.Redirs, &dir, depth, configEnv)
		}
		return walkErr == nil
	})
	return walkErr
}

// call inspects one simple command; dir is updated by cd/pushd for the statements after it.
func (e *extractor) call(argv []arg, redirs []*syntax.Redirect, dir *string, depth int, configEnv bool) error {
	argv, envFlags := e.unwrap(argv, *dir, depth)
	configEnv = configEnv || envFlags
	if len(argv) == 0 {
		return nil
	}
	name := argv[0]
	if name.dynamic {
		if anyContainsFold(argv, "push") {
			e.flag(Ask, "a command built at runtime mentions push")
		}
		return nil
	}
	base := cmdBase(name.val)
	switch {
	case base == "cd" || base == "pushd":
		if t := firstOperand(argv[1:]); t != nil && !t.dynamic {
			*dir = e.resolve(*dir, t.val)
		}
	case base == "unset":
		for _, a := range argv[1:] {
			if isGitConfigEnv(a.val) {
				e.flag(Ask, "unsetting git config variables can disable the git-level guard")
			}
		}
	case base == "git":
		return e.git(argv[1:], *dir, configEnv, depth, nil)
	case strings.HasPrefix(base, "git-"):
		// git-push and friends run a git subcommand directly.
		return e.gitSub(strings.TrimPrefix(base, "git-"), argv[1:], *dir, nil, configEnv, depth)
	case base == "eval":
		return e.nested(argv[1:], *dir, depth, "eval")
	case base == "pwsh" || base == "powershell":
		words := make([]psWord, len(argv)-1)
		for i, a := range argv[1:] {
			words[i] = psWord{val: a.val, dynamic: a.dynamic}
		}
		e.psNested(words, *dir, depth)
	case base == "wsl" || base == "winpty":
		rest := wslCommand(argv)
		if len(rest) == 0 {
			if base == "wsl" {
				e.flag(Ask, "wsl starts a shell that reads its commands from stdin, which cannot be checked")
			}
			return nil
		}
		return e.call(rest, redirs, dir, depth+1, configEnv)
	case base == "cmd":
		if cmdRuns(argv[1:]) && anyContainsFold(argv[1:], "git") {
			e.flag(Ask, "cmd.exe syntax is not analysed, so a git push in it cannot be checked")
		}
	case base == "start" || base == "cygstart":
		if anyContainsFold(argv[1:], "git") {
			e.flag(Ask, base+" starts a program whose git commands cannot be checked")
		}
	case isShell(base):
		return e.shell(argv, redirs, *dir, depth)
	case isInterpreter(base):
		e.interpreter(argv, redirs)
	case base == "xargs":
		rest := skipOptions(argv[1:], map[string]bool{"-I": true, "-n": true, "-P": true, "-L": true, "-d": true, "-E": true, "-s": true, "-a": true})
		rest, _ = e.unwrap(rest, *dir, depth)
		if len(rest) > 0 && isGitName(rest[0]) && anyContainsFold(rest, "push") {
			e.flag(Ask, "xargs supplies git push arguments at runtime")
		}
	case base == "find":
		return e.findExec(argv[1:], *dir, depth)
	case base == "watch":
		rest := skipOptions(argv[1:], map[string]bool{"-n": true, "-d": false, "--interval": true, "-q": true})
		return e.nested(rest, *dir, depth, "watch")
	}
	return nil
}

// git skips git's global options, collecting -c overrides, and dispatches the subcommand.
// inherited carries -c overrides into an expanded alias, which still sees them.
func (e *extractor) git(args []arg, dir string, configEnv bool, depth int, inherited map[string][]string) error {
	overrides := map[string][]string{}
	for k, v := range inherited {
		overrides[k] = slices.Clone(v)
	}
	for len(args) > 0 {
		a := args[0]
		v := a.val
		switch {
		case a.dynamic:
			e.flag(Ask, "a git subcommand or option is only known at runtime")
			return nil
		case v == "-C" && len(args) > 1:
			if args[1].dynamic {
				e.flag(Ask, "git -C with a directory known only at runtime")
				return nil
			}
			dir = e.resolve(dir, args[1].val)
			args = args[2:]
		case v == "-c" && len(args) > 1:
			addOverride(overrides, args[1])
			args = args[2:]
		case strings.HasPrefix(v, "-c") && len(v) > 2:
			addOverride(overrides, arg{val: v[2:], dynamic: a.dynamic})
			args = args[1:]
		case v == "--exec-path" || strings.HasPrefix(v, "--exec-path="):
			if v != "--exec-path" { // the bare form only prints the path
				configEnv = true
			}
			args = args[1:]
		case v == "--config-env":
			configEnv = true
			args = args[min(2, len(args)):]
		case strings.HasPrefix(v, "--config-env="):
			configEnv = true
			args = args[1:]
		case slices.Contains([]string{"--git-dir", "--work-tree", "--namespace", "--super-prefix", "--attr-source", "--list-cmds"}, v):
			args = args[min(2, len(args)):]
		case strings.HasPrefix(v, "-"):
			args = args[1:]
		default:
			return e.gitSub(strings.ToLower(v), args[1:], dir, overrides, configEnv || e.sessionConfigEnv, depth)
		}
	}
	return nil
}

// gitSub records pushes and follows subcommands that run other commands or alias to them.
func (e *extractor) gitSub(sub string, args []arg, dir string, overrides map[string][]string, configEnv bool, depth int) error {
	if overrides == nil {
		overrides = map[string][]string{}
	}
	call := gitCall{dir: dir, sub: sub, args: args, overrides: overrides, configEnv: configEnv || e.sessionConfigEnv}
	switch sub {
	case "push":
		e.calls = append(e.calls, call)
	case "send-pack":
		e.flag(Deny, "git send-pack bypasses git push checks; use git push")
	case "submodule":
		if i := indexOf(args, "foreach"); i >= 0 {
			rest := skipOptions(args[i+1:], nil)
			return e.nested(rest, dir, depth, "git submodule foreach")
		}
	case "rebase":
		for i := 0; i < len(args); i++ {
			v := args[i].val
			switch {
			case (v == "--exec" || v == "-x") && i+1 < len(args):
				if err := e.nested(args[i+1:i+2], dir, depth, "git rebase --exec"); err != nil {
					return err
				}
				i++
			case strings.HasPrefix(v, "--exec="):
				if err := e.nested([]arg{{val: strings.TrimPrefix(v, "--exec="), dynamic: args[i].dynamic}}, dir, depth, "git rebase --exec"); err != nil {
					return err
				}
			}
		}
	case "bisect":
		if len(args) > 1 && args[0].val == "run" {
			return e.call(args[1:], nil, &dir, depth+1, false)
		}
	default:
		if knownGitCommand(sub) {
			return nil
		}
		if call.configEnv {
			e.flag(Ask, "git config is injected through the environment, so git "+sub+" may be an alias")
			return nil
		}
		if overrides["\x00dynamic"] != nil {
			e.flag(Ask, "a git -c setting is only known at runtime, so git "+sub+" may be an alias")
			return nil
		}
		return e.alias(sub, call, depth)
	}
	return nil
}

// alias expands `git <alias>` from -c overrides or the repository's config.
func (e *extractor) alias(sub string, call gitCall, depth int) error {
	key := "alias." + sub
	values, ok := call.overrides[key]
	if !ok {
		f, err := e.facts.get(e.ctx, call.dir)
		if err != nil {
			// Not a parse failure: the finding asks, and the walk goes on.
			e.flag(Ask, "could not read git config to resolve git "+sub+": "+err.Error())
			return nil //nolint:nilerr // reported as a finding
		}
		values, ok = f.Config[key]
	}
	if !ok || len(values) == 0 {
		return nil // an external git-<sub> program or a typo; git push semantics do not apply
	}
	if depth >= maxDepth {
		e.flag(Ask, "git alias "+sub+" nests too deeply to check")
		return nil
	}
	value := values[len(values)-1]
	if strings.HasPrefix(value, "!") {
		// Shell alias: git runs `sh -c '<value> "$@"'` with the remaining arguments.
		src := strings.TrimPrefix(value, "!") + " " + joinVals(call.args)
		return e.script(src, call.dir, depth+1)
	}
	expanded := words(nil)
	for _, w := range strings.Fields(value) {
		expanded = append(expanded, arg{val: w})
	}
	if len(expanded) == 0 {
		return nil
	}
	return e.git(append(expanded, call.args...), call.dir, call.configEnv, depth+1, call.overrides)
}

// shell follows `sh -c`, here-documents, here-strings and stdin-fed shells.
func (e *extractor) shell(argv []arg, redirs []*syntax.Redirect, dir string, depth int) error {
	i, operand := scriptFlag(argv)
	if i > 0 {
		if i+1 >= len(argv) {
			return nil
		}
		return e.nested(argv[i+1:i+2], dir, depth, argv[0].val+" -c")
	}
	for _, r := range redirs {
		switch {
		case r.Hdoc != nil:
			return e.nested([]arg{wordArg(r.Hdoc)}, dir, depth, "here-document")
		case r.Op == syntax.WordHdoc && r.Word != nil:
			return e.nested([]arg{wordArg(r.Word)}, dir, depth, "here-string")
		}
	}
	switch {
	case operand == nil:
		e.flag(Ask, "a shell reads its commands from stdin, which cannot be checked")
	case operand.dynamic:
		e.flag(Ask, "a shell runs a script given at runtime (process substitution or variable)")
	}
	// A named script file is outside this guard's view; the git-level guard covers it.
	return nil
}

func (e *extractor) interpreter(argv []arg, redirs []*syntax.Redirect) {
	code, inline := interpreterCode(argv)
	for _, r := range redirs {
		if r.Hdoc != nil || (r.Op == syntax.WordHdoc && r.Word != nil) {
			w := r.Hdoc
			if w == nil {
				w = r.Word
			}
			a := wordArg(w)
			code, inline = append(code, a), true
		}
	}
	if !inline {
		return
	}
	for _, c := range code {
		if c.dynamic || strings.Contains(strings.ToLower(c.val), "push") {
			e.flag(Ask, argv[0].val+" runs inline code that may push; confirm it does not target a protected branch")
			return
		}
	}
}

// findExec follows `find … -exec cmd … ;` (and -execdir, -ok, -okdir).
func (e *extractor) findExec(args []arg, dir string, depth int) error {
	for i := 0; i < len(args); i++ {
		switch args[i].val {
		case "-exec", "-execdir", "-ok", "-okdir":
			j := i + 1
			for j < len(args) && args[j].val != ";" && args[j].val != "+" {
				j++
			}
			if err := e.call(args[i+1:j], nil, &dir, depth+1, false); err != nil {
				return err
			}
			i = j
		}
	}
	return nil
}

// nested parses a script carried by eval, sh -c, watch, git aliases or here-documents. A script
// built at runtime cannot be inspected and is asked about when it could involve git.
func (e *extractor) nested(parts []arg, dir string, depth int, what string) error {
	vals := make([]string, len(parts))
	for i, p := range parts {
		if p.dynamic {
			e.flag(Ask, what+" runs a script built at runtime, which cannot be checked")
			return nil
		}
		vals[i] = p.val
	}
	return e.script(strings.Join(vals, " "), dir, depth+1)
}

func (e *extractor) resolve(dir, p string) string {
	p = nativePath(p)
	switch {
	case p == "~" || strings.HasPrefix(p, "~/"):
		return filepath.Join(e.home, strings.TrimPrefix(p, "~"))
	case filepath.IsAbs(p):
		return filepath.Clean(p)
	default:
		return filepath.Join(dir, p)
	}
}

// wrapperOptsWithValue lists options of command wrappers that consume the next word.
var wrapperOptsWithValue = map[string]map[string]bool{
	"sudo":    {"-u": true, "-g": true, "-C": true, "-D": true, "-h": true, "-p": true, "-r": true, "-t": true, "-U": true},
	"doas":    {"-u": true, "-C": true},
	"env":     {"-u": true, "-C": true, "-P": true, "--unset": true, "--chdir": true},
	"nice":    {"-n": true},
	"timeout": {"-s": true, "-k": true, "--signal": true, "--kill-after": true},
	"exec":    {"-a": true},
	"stdbuf":  {"-i": true, "-o": true, "-e": true},
	"ionice":  {"-c": true, "-n": true, "-p": true},
	"xcrun":   {"--sdk": true, "--toolchain": true},
	"script":  {"-t": true, "-T": true, "-I": true, "-O": true, "-B": true, "-E": true},
}

// unwrap strips command wrappers (sudo, env, nohup, timeout, xcrun, script, …) to reach the
// real command. It reports whether a wrapper set or cleared git config through the environment.
func (e *extractor) unwrap(argv []arg, dir string, depth int) ([]arg, bool) {
	configEnv := false
	for len(argv) > 0 && !argv[0].dynamic {
		w := cmdBase(argv[0].val)
		switch w {
		case "sudo", "doas", "command", "builtin", "exec", "nohup", "time", "nice", "stdbuf", "caffeinate",
			"noglob", "ionice", "chronic", "xcrun", "unbuffer", "torsocks", "proxychains", "proxychains4":
			argv = skipOptions(argv[1:], wrapperOptsWithValue[w])
		case "env":
			rest := argv[1:]
		envArgs:
			for len(rest) > 0 && !rest[0].dynamic {
				v := rest[0].val
				switch {
				case v == "-S" || v == "--split-string":
					if len(rest) > 1 {
						_ = e.nested(rest[1:2], dir, depth, "env -S")
					}
					return nil, configEnv
				case strings.HasPrefix(v, "-S"):
					_ = e.nested([]arg{{val: v[2:]}}, dir, depth, "env -S")
					return nil, configEnv
				case v == "-u" || v == "--unset":
					if len(rest) > 1 && isGitConfigEnv(rest[1].val) {
						e.flag(Ask, "unsetting git config variables can disable the git-level guard")
					}
					rest = rest[min(2, len(rest)):]
				case strings.HasPrefix(v, "-") && v != "-":
					if wrapperOptsWithValue["env"][v] {
						rest = rest[min(2, len(rest)):]
					} else {
						rest = rest[1:]
					}
				case strings.Contains(v, "="):
					if name, _, _ := strings.Cut(v, "="); isGitConfigEnv(name) {
						configEnv = true
					}
					rest = rest[1:]
				default:
					break envArgs
				}
			}
			argv = rest
		case "timeout":
			argv = skipOptions(argv[1:], wrapperOptsWithValue[w])
			if len(argv) > 0 {
				argv = argv[1:] // duration
			}
		case "script":
			rest := skipOptions(argv[1:], wrapperOptsWithValue[w])
			if i := indexOfFlag(argv[1:], "-c"); i >= 0 && i+2 < len(argv) {
				_ = e.nested(argv[i+2:i+3], dir, depth, "script -c")
				return nil, configEnv
			}
			// BSD form: script [-q] file command…
			if len(rest) > 1 {
				argv = rest[1:]
			} else {
				return nil, configEnv
			}
		default:
			return argv, configEnv
		}
	}
	return argv, configEnv
}

func skipOptions(argv []arg, withValue map[string]bool) []arg {
	for len(argv) > 0 && strings.HasPrefix(argv[0].val, "-") && !argv[0].dynamic && argv[0].val != "-" {
		if argv[0].val == "--" {
			return argv[1:]
		}
		if withValue[argv[0].val] && len(argv) > 1 {
			argv = argv[2:]
			continue
		}
		argv = argv[1:]
	}
	return argv
}

func isShell(base string) bool {
	switch base {
	case "sh", "bash", "zsh", "dash", "ksh", "mksh", "fish", "ash", "busybox":
		return true
	}
	return false
}

func isInterpreter(base string) bool {
	switch base {
	case "python", "python2", "python3", "node", "nodejs", "deno", "bun", "perl", "ruby", "php", "lua",
		"osascript", "rscript", "tclsh", "groovy", "jshell":
		return true
	}
	return strings.HasPrefix(base, "python3.")
}

// interpreterCode returns inline code passed with -c/-e/--eval style flags, or reports that the
// interpreter reads its program from stdin (no script operand).
func interpreterCode(argv []arg) ([]arg, bool) {
	for i := 1; i < len(argv); i++ {
		v := argv[i].val
		switch {
		case v == "-c" || v == "-e" || v == "-E" || v == "--eval" || v == "-p" || v == "--print" || v == "-r" || v == "-Command":
			if i+1 < len(argv) {
				return argv[i+1 : i+2], true
			}
			return nil, false
		case strings.HasPrefix(v, "--eval="):
			return []arg{{val: strings.TrimPrefix(v, "--eval="), dynamic: argv[i].dynamic}}, true
		case v == "-":
			return nil, true
		case !strings.HasPrefix(v, "-"):
			return nil, false // a script file
		}
	}
	return nil, false
}

// scriptFlag returns the index of the -c option (also inside clusters like -lc) or 0, and the
// first operand when the shell runs a script file (nil when it reads stdin).
func scriptFlag(argv []arg) (int, *arg) {
	for i := 1; i < len(argv); i++ {
		v := argv[i].val
		switch {
		case argv[i].dynamic && !strings.HasPrefix(v, "-"):
			return 0, &argv[i]
		case v == "-o" || v == "+o" || v == "-O" || v == "+O" || v == "--rcfile" || v == "--init-file":
			i++ // option with a value, e.g. -o pipefail, -O extglob
		case v == "--":
			if i+1 < len(argv) {
				return 0, &argv[i+1]
			}
			return 0, nil
		case v == "-s" || v == "-i":
			return 0, nil
		case !strings.HasPrefix(v, "-") && !strings.HasPrefix(v, "+"):
			return 0, &argv[i]
		case strings.HasPrefix(v, "--"):
		case strings.Contains(v[1:], "c"):
			return i, nil
		}
	}
	return 0, nil
}

// cmdBase normalises a command word: lower case, last element of a / or \ path, no .exe — so
// C:\Program Files\Git\cmd\git.exe and /mingw64/bin/git are both "git".
func cmdBase(v string) string {
	b := strings.ToLower(path.Base(strings.ReplaceAll(v, `\`, "/")))
	return strings.TrimSuffix(b, ".exe")
}

// wslCommand returns the command wsl or winpty runs, skipping their options.
func wslCommand(argv []arg) []arg {
	rest := argv[1:]
	for len(rest) > 0 && !rest[0].dynamic {
		switch v := rest[0].val; v {
		case "-d", "--distribution", "-u", "--user", "--cd":
			rest = rest[min(2, len(rest)):]
		case "-e", "--exec", "--":
			return rest[1:]
		default:
			if strings.HasPrefix(v, "-") {
				rest = rest[1:]
				continue
			}
			return rest
		}
	}
	return rest
}

// cmdRuns reports cmd.exe running a command line (/c, /k, //c from Git Bash).
func cmdRuns(args []arg) bool {
	for _, a := range args {
		switch strings.ToLower(a.val) {
		case "/c", "/k", "//c", "//k", "-c":
			return true
		}
	}
	return false
}

func isGitName(a arg) bool {
	if a.dynamic {
		return false
	}
	b := cmdBase(a.val)
	return b == "git" || strings.HasPrefix(b, "git-")
}

// isGitConfigEnv reports environment variables that inject git configuration or programs.
func isGitConfigEnv(name string) bool {
	name = strings.ToUpper(name) // Windows environment names are case-insensitive
	return name == "GIT_CONFIG_COUNT" || name == "GIT_CONFIG_PARAMETERS" || name == "GIT_EXEC_PATH" ||
		name == "GIT_CONFIG" || name == "GIT_CONFIG_GLOBAL" || name == "GIT_CONFIG_SYSTEM" ||
		strings.HasPrefix(name, "GIT_CONFIG_KEY_") || strings.HasPrefix(name, "GIT_CONFIG_VALUE_")
}

func addOverride(overrides map[string][]string, a arg) {
	k, v, _ := strings.Cut(a.val, "=")
	if a.dynamic {
		k = "\x00dynamic"
	}
	k = strings.ToLower(k)
	overrides[k] = append(overrides[k], v)
}

func firstOperand(argv []arg) *arg {
	for i := range argv {
		if !strings.HasPrefix(argv[i].val, "-") || argv[i].dynamic {
			return &argv[i]
		}
	}
	return nil
}

func indexOf(argv []arg, s string) int {
	for i, a := range argv {
		if !a.dynamic && a.val == s {
			return i
		}
	}
	return -1
}

func indexOfFlag(argv []arg, flag string) int {
	for i, a := range argv {
		if a.val == flag {
			return i
		}
	}
	return -1
}

func words(ws []*syntax.Word) []arg {
	out := make([]arg, len(ws))
	for i, w := range ws {
		out[i] = wordArg(w)
	}
	return out
}

// globChars make an unquoted word subject to brace or pathname expansion.
const globChars = "{}*?["

func wordArg(w *syntax.Word) arg {
	var b strings.Builder
	dynamic := false
	for _, part := range w.Parts {
		switch p := part.(type) {
		case *syntax.Lit:
			if strings.ContainsAny(p.Value, globChars) {
				dynamic = true
			}
			b.WriteString(unescape(p.Value))
		case *syntax.SglQuoted:
			if p.Dollar {
				b.WriteString(ansiC(p.Value))
			} else {
				b.WriteString(p.Value)
			}
		case *syntax.DblQuoted:
			for _, q := range p.Parts {
				if l, ok := q.(*syntax.Lit); ok {
					b.WriteString(l.Value)
				} else {
					dynamic = true
				}
			}
		default:
			dynamic = true
		}
	}
	return arg{val: b.String(), dynamic: dynamic}
}

// ansiC decodes the escapes of $'…' strings that matter for command names.
func ansiC(s string) string {
	r := strings.NewReplacer(`\n`, "\n", `\t`, "\t", `\'`, "'", `\"`, `"`, `\\`, `\`)
	s = r.Replace(s)
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+3 < len(s) && s[i+1] == 'x' {
			var v byte
			ok := true
			for _, c := range []byte{s[i+2], s[i+3]} {
				switch {
				case c >= '0' && c <= '9':
					v = v*16 + c - '0'
				case c >= 'a' && c <= 'f':
					v = v*16 + c - 'a' + 10
				case c >= 'A' && c <= 'F':
					v = v*16 + c - 'A' + 10
				default:
					ok = false
				}
			}
			if ok {
				b.WriteByte(v)
				i += 3
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// unescape removes backslash escapes from an unquoted literal (pu\sh → push).
func unescape(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	escaped := false
	for _, r := range s {
		if r == '\\' && !escaped {
			escaped = true
			continue
		}
		escaped = false
		b.WriteRune(r)
	}
	return b.String()
}

func anyContainsFold(argv []arg, s string) bool {
	for _, a := range argv {
		if strings.Contains(strings.ToLower(a.val), s) {
			return true
		}
	}
	return false
}

func joinVals(argv []arg) string {
	vals := make([]string, len(argv))
	for i, a := range argv {
		vals[i] = a.val
	}
	return strings.Join(vals, " ")
}

// knownGitCommand lists built-in subcommands; git ignores aliases that shadow them, so only
// other names need alias resolution.
func knownGitCommand(sub string) bool {
	switch sub {
	case "add", "am", "annotate", "apply", "archive", "bisect", "blame", "branch", "bundle", "cat-file",
		"check-ignore", "checkout", "cherry", "cherry-pick", "clean", "clone", "commit", "config", "count-objects",
		"describe", "diff", "diff-files", "diff-index", "diff-tree", "difftool", "fetch", "for-each-ref",
		"format-patch", "fsck", "gc", "grep", "hash-object", "help", "init", "log", "ls-files", "ls-remote",
		"ls-tree", "merge", "merge-base", "mergetool", "mv", "notes", "pull", "range-diff", "rebase", "reflog",
		"remote", "repack", "replace", "reset", "restore", "rev-list", "rev-parse", "revert", "rm", "shortlog",
		"show", "show-branch", "show-ref", "sparse-checkout", "stash", "status", "submodule", "switch",
		"symbolic-ref", "tag", "update-index", "update-ref", "var", "verify-commit", "verify-tag", "version",
		"whatchanged", "worktree", "write-tree", "maintenance", "lfs":
		return true
	}
	return false
}
