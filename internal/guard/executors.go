package guard

import (
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// riskyEnv reports environment variables that redirect git to another repository or
// configuration, switch off the git-level guard, or name a program git runs.
func riskyEnv(name string) bool {
	switch n := strings.ToUpper(name); n {
	case "GIT_CONFIG", "GIT_CONFIG_COUNT", "GIT_CONFIG_PARAMETERS", "GIT_CONFIG_GLOBAL", "GIT_CONFIG_SYSTEM",
		"GIT_CONFIG_NOSYSTEM", "GIT_DIR", "GIT_WORK_TREE", "GIT_EXEC_PATH", "GIT_TEMPLATE_DIR",
		"GIT_SSH", "GIT_SSH_COMMAND", "GIT_EDITOR", "GIT_SEQUENCE_EDITOR", "GIT_PAGER", "GIT_ASKPASS",
		"GIT_EXTERNAL_DIFF", "GIT_PROXY_COMMAND":
		return true
	default:
		return strings.HasPrefix(n, "GIT_CONFIG_KEY_") || strings.HasPrefix(n, "GIT_CONFIG_VALUE_")
	}
}

// envWrite flags setting, exporting or unsetting a variable that changes what git does.
func (e *extractor) envWrite(name string) {
	if riskyEnv(name) {
		e.flag(Ask, "changing "+strings.ToUpper(name)+" redirects git or the programs it runs, and can disable the git-level guard")
	}
}

// trap judges the command a trap runs: trap [--] '<cmd>' SIGNAL…
func (e *extractor) trap(args []arg, dir string, depth int) error {
	if len(args) > 0 && args[0].val == "--" {
		args = args[1:]
	}
	if len(args) < 2 || (strings.HasPrefix(args[0].val, "-") && !args[0].dynamic) {
		return nil // trap -p, trap -l, trap SIGNAL (reset)
	}
	return e.nested(args[:1], dir, depth, "trap")
}

// rcFiles are the shell start-up files that may be sourced without asking.
var rcFiles = []string{".bashrc", ".zshrc", ".profile", ".bash_profile", ".zprofile", ".zshenv"}

// source asks unless the script is the user's own shell start-up file.
func (e *extractor) source(args []arg, dir string) {
	if len(args) > 0 && !args[0].dynamic && e.home != "" {
		p := e.resolve(dir, args[0].val)
		if filepath.Dir(p) == filepath.Clean(e.home) && slices.Contains(rcFiles, filepath.Base(p)) {
			return
		}
	}
	e.flag(Ask, "sourcing a script runs commands that cannot be checked")
}

// isOpaqueRunner reports programs that run a command this guard cannot follow: remote shells,
// terminal multiplexers and automation tools.
func isOpaqueRunner(base string, argv []arg) bool {
	switch base {
	case "ssh", "expect", "su", "at", "batch", "flock", "setsid", "unshare", "chroot", "systemd-run":
		return true
	case "tmux":
		for _, a := range argv[1:] {
			switch a.val {
			case "new-session", "new", "new-window", "neww", "split-window", "splitw", "send-keys", "send",
				"run-shell", "run", "respawn-pane", "respawnp", "respawn-window", "respawnw", "display-popup", "popup":
				return true
			}
		}
	}
	return false
}

func isEditorLike(base string) bool {
	switch base {
	case "awk", "gawk", "mawk", "nawk", "sed", "gsed", "vim", "nvim", "vi", "ex", "emacs":
		return true
	}
	return false
}

// sedExec matches GNU sed's e command or the e flag of s///.
var sedExec = regexp.MustCompile(`(^|[;{}\n])\s*e(\s|;|$)|[/|#,:@!][gpiImMw0-9]*e[gpiImMw0-9]*\s*($|[;}\n])`)

// editorLike asks when awk, sed or an editor runs inline code that could start git.
func (e *extractor) editorLike(base string, argv []arg) {
	for _, a := range argv[1:] {
		if a.dynamic && !strings.HasPrefix(a.val, "-") {
			continue
		}
		v := strings.ToLower(a.val)
		if !strings.Contains(v, "git") {
			continue
		}
		switch {
		case strings.HasSuffix(base, "awk") && (strings.Contains(v, "system") || strings.Contains(v, "|")),
			strings.HasSuffix(base, "sed") && sedExec.MatchString(a.val),
			!strings.HasSuffix(base, "awk") && !strings.HasSuffix(base, "sed"):
			e.flag(Ask, base+" runs inline code that may run git; confirm it does not push to a protected branch")
			return
		}
	}
}

// gitPushAlias matches the push aliases of common shell frameworks (oh-my-zsh: gp, gpf, gpf!,
// gpsup, gpu, gpd, ggpush, ggp, ggpf, gpoat, gpr, …) but not gpg, gprof or gpm.
var gitPushAlias = regexp.MustCompile(`^g{1,2}p(f!?|sup|supf|u|d|v|od|oat|r|ush|np|ushd)?$`)
