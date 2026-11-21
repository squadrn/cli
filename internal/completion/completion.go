// Package completion decides where shell completion scripts go so that the shell loads them,
// and edits a shell startup file only when it has to: with a marked block that can be found and
// removed again. It never prints.
package completion

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// Begin and End mark the block squadrn adds to a shell startup file.
const (
	Begin = "# >>> squadrn completion >>>"
	End   = "# <<< squadrn completion <<<"
)

// Shells squadrn installs completion scripts for on Unix.
var Shells = []string{"zsh", "bash", "fish"}

// Env is what a plan reads from the machine.
type Env struct {
	Home string
	// ZDotDir is $ZDOTDIR, where zsh reads its startup files ("" for Home).
	ZDotDir string
	// DataHome and ConfigHome are the XDG base dirs ($XDG_DATA_HOME, $XDG_CONFIG_HOME, or their
	// defaults under Home).
	DataHome, ConfigHome string
	// Fpath returns the fpath of an interactive zsh; nil, or an error, reads the startup files
	// instead.
	Fpath func() ([]string, error)
	// System also considers dirs outside Home (Homebrew's site-functions): only when the user
	// asks for completion to be installed, never on install or update.
	System bool
}

// Plan says where the scripts of one shell go and what makes the shell load them.
type Plan struct {
	Shell string
	Dir   string
	// RC is the startup file that needs Block to load Dir; "" when the shell loads Dir as it is.
	RC    string
	Block string
	// Done: RC already holds Block, ahead of compinit.
	Done bool
}

// Active reports whether a new shell loads the scripts in Dir.
func (p Plan) Active() bool { return p.RC == "" || p.Done }

// zshDirs are the zsh completion dirs squadrn uses when one is on fpath, in order of preference:
// dirs in the home directory, then Homebrew's and the default site-functions dir.
func (e Env) zshDirs() []string {
	dirs := []string{e.zshDefault(), filepath.Join(e.Home, ".zfunc"), filepath.Join(e.Home, ".zsh", "functions")}
	if e.System {
		dirs = append(dirs, systemZshDirs...)
	}
	return dirs
}

var systemZshDirs = []string{
	"/usr/local/share/zsh/site-functions",
	"/opt/homebrew/share/zsh/site-functions",
	"/home/linuxbrew/.linuxbrew/share/zsh/site-functions",
}

// zshDefault is the dir squadrn adds to fpath when none of zshDirs is on it.
func (e Env) zshDefault() string { return filepath.Join(e.Home, ".zsh", "completions") }

func (e Env) zshrc() string {
	if e.ZDotDir != "" {
		return filepath.Join(e.ZDotDir, ".zshrc")
	}
	return filepath.Join(e.Home, ".zshrc")
}

func (e Env) bashDir() string { return filepath.Join(e.DataHome, "bash-completion", "completions") }

// bashLoaders are the bash-completion entry points that load scripts from bashDir on demand.
var bashLoaders = []string{
	"/usr/share/bash-completion/bash_completion",
	"/usr/local/share/bash-completion/bash_completion",
	"/opt/homebrew/etc/profile.d/bash_completion.sh",
	"/usr/local/etc/profile.d/bash_completion.sh",
	"/home/linuxbrew/.linuxbrew/etc/profile.d/bash_completion.sh",
}

// Dirs lists every dir squadrn may have written shell's scripts to, for removal.
func (e Env) Dirs(shell string) []string {
	switch shell {
	case "zsh":
		return append(e.zshDirs()[:3:3], systemZshDirs...)
	case "bash":
		return []string{e.bashDir()}
	case "fish":
		return []string{filepath.Join(e.ConfigHome, "fish", "completions")}
	}
	return nil
}

// RCFiles are the startup files that may hold squadrn's block.
func (e Env) RCFiles() []string {
	return []string{e.zshrc(), filepath.Join(e.Home, ".bashrc")}
}

// File is the script of command name in dir for shell.
func File(shell, dir, name string) string {
	switch shell {
	case "zsh":
		return filepath.Join(dir, "_"+name)
	case "fish":
		return filepath.Join(dir, name+".fish")
	}
	return filepath.Join(dir, name)
}

// Plan returns where shell's scripts for names go. Unknown shells return false.
func (e Env) Plan(shell string, names []string) (Plan, bool) {
	switch shell {
	case "zsh":
		return e.zshPlan(), true
	case "bash":
		return e.bashPlan(names), true
	case "fish":
		return Plan{Shell: shell, Dir: e.Dirs("fish")[0]}, true
	}
	return Plan{}, false
}

func (e Env) zshPlan() Plan {
	rc := e.zshrc()
	content := read(rc)
	fpath := e.zshFpath(content)
	// A dir the file adds only after compinit runs is on fpath, but its scripts are not loaded.
	var late []string
	if c := compinitLine(content); c >= 0 {
		late = StaticFpath(content[c:], e.Home)
		for _, d := range StaticFpath(content[:c], e.Home) {
			late = slices.DeleteFunc(late, func(x string) bool { return x == d })
		}
	}
	for _, d := range e.zshDirs() {
		if slices.Contains(fpath, d) && !slices.Contains(late, d) && writable(d) {
			return Plan{Shell: "zsh", Dir: d}
		}
	}
	dir := e.zshDefault()
	block := "fpath=(" + e.shellPath(dir) + " $fpath)"
	if compinitLine(content) < 0 {
		block += "\nautoload -Uz compinit && compinit"
	}
	p := Plan{Shell: "zsh", Dir: dir, RC: rc, Block: block}
	if b := strings.Index(content, Begin); b >= 0 {
		c := compinitLine(content)
		p.Done = c < 0 || b < c
	}
	return p
}

func (e Env) bashPlan(names []string) Plan {
	dir := e.bashDir()
	for _, l := range bashLoaders {
		if isFile(l) {
			return Plan{Shell: "bash", Dir: dir}
		}
	}
	var lines []string
	for _, n := range names {
		f := e.shellPath(File("bash", dir, n))
		lines = append(lines, `[ -f "`+f+`" ] && . "`+f+`"`)
	}
	rc := filepath.Join(e.Home, ".bashrc")
	return Plan{Shell: "bash", Dir: dir, RC: rc, Block: strings.Join(lines, "\n"), Done: strings.Contains(read(rc), Begin)}
}

// zshFpath is the fpath of an interactive zsh, else the dirs its startup files add to it (zsh's
// built-in dirs are not known then, so only those count).
func (e Env) zshFpath(zshrc string) []string {
	if e.Fpath != nil {
		if dirs, err := e.Fpath(); err == nil {
			return dirs
		}
	}
	var dirs []string
	base := e.Home
	if e.ZDotDir != "" {
		base = e.ZDotDir
	}
	for _, f := range []string{filepath.Join(e.Home, ".zshenv"), filepath.Join(base, ".zprofile")} {
		dirs = append(dirs, StaticFpath(read(f), e.Home)...)
	}
	return append(dirs, StaticFpath(zshrc, e.Home)...)
}

var fpathAssign = regexp.MustCompile(`^\s*(?:typeset\s+-U\s+)?fpath\+?=\(?([^)#]*)\)?`)

// StaticFpath lists the dirs a zsh startup file adds to fpath with fpath=(…) or fpath+=(…).
func StaticFpath(content, home string) []string {
	var out []string
	for _, l := range strings.Split(content, "\n") {
		m := fpathAssign.FindStringSubmatch(l)
		if m == nil {
			continue
		}
		for _, w := range strings.Fields(m[1]) {
			w = strings.Trim(w, `"'`)
			if strings.Contains(w, "fpath") {
				continue
			}
			for _, h := range []string{"~", "$HOME", "${HOME}"} {
				if rest, ok := strings.CutPrefix(w, h); ok && (rest == "" || rest[0] == '/') {
					w = home + rest
				}
			}
			if !strings.Contains(w, "$") {
				out = append(out, filepath.Clean(w))
			}
		}
	}
	return out
}

// compinitRe matches a line that runs compinit, directly or through a zsh framework.
var compinitRe = regexp.MustCompile(`^[^#]*(\bcompinit\b|oh-my-zsh\.sh|\bzinit\b|\bantidote\b|\bzimfw\b|prezto/init\.zsh)`)

// compinitLine is the byte offset of the first line of content that runs compinit; -1 if none.
func compinitLine(content string) int {
	off := 0
	for _, l := range strings.SplitAfter(content, "\n") {
		if strings.Contains(l, Begin) {
			// squadrn's own block may run compinit; it never comes before itself.
			off += len(l)
			continue
		}
		if compinitRe.MatchString(l) && !inBlock(content, off) {
			return off
		}
		off += len(l)
	}
	return -1
}

func inBlock(content string, off int) bool {
	b := strings.LastIndex(content[:off], Begin)
	return b >= 0 && !strings.Contains(content[b:off], End)
}

// WithBlock puts block between the markers into content: in place of an existing block, else
// above the first line that runs compinit (zsh only reads fpath when compinit runs), else at the end.
func WithBlock(content, block string) string {
	content, _ = WithoutBlock(content)
	text := Begin + "\n" + block + "\n" + End + "\n"
	if c := compinitLine(content); c >= 0 {
		return content[:c] + text + content[c:]
	}
	if content != "" && !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	return content + text
}

// WithoutBlock removes squadrn's block from content; other lines stay byte for byte.
func WithoutBlock(content string) (string, bool) {
	b := strings.Index(content, Begin)
	if b < 0 {
		return content, false
	}
	e := strings.Index(content[b:], End)
	if e < 0 {
		return content, false
	}
	end := b + e + len(End)
	if end < len(content) && content[end] == '\n' {
		end++
	}
	return content[:b] + content[end:], true
}

// shellPath writes p for a startup file: $HOME instead of the home directory.
func (e Env) shellPath(p string) string {
	if rel, err := filepath.Rel(e.Home, p); err == nil && !strings.HasPrefix(rel, "..") {
		return "$HOME/" + filepath.ToSlash(rel)
	}
	return p
}

func read(path string) string {
	b, _ := os.ReadFile(path)
	return string(b)
}

func isFile(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir()
}
