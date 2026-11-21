//go:build !windows

package cli

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/squadrn/squadrn-cli/internal/completion"
	"github.com/squadrn/squadrn-cli/internal/config"
	"github.com/squadrn/squadrn-cli/internal/doctor"
	"github.com/squadrn/squadrn-cli/internal/fsx"
	"github.com/squadrn/squadrn-cli/internal/proc"
)

// completionEnv is what the completion plans read from this machine.
func (a *app) completionEnv() completion.Env {
	dirs := config.DirsFor(a.store.Home, "")
	return completion.Env{
		Home: a.store.Home, ZDotDir: os.Getenv("ZDOTDIR"), DataHome: dirs.Data, ConfigHome: dirs.Config,
		Fpath: zshFpath,
	}
}

const fpathMarker = "__squadrn_fpath__"

// zshFpath asks an interactive zsh for its fpath: it reads the user's startup files, so a
// deadline bounds it.
func zshFpath() ([]string, error) {
	zsh, err := exec.LookPath("zsh")
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, err := proc.Output(ctx, proc.Options{}, zsh, "-ic", "print -r -- "+fpathMarker+"; print -rl -- $fpath")
	if err != nil {
		return nil, err
	}
	_, list, ok := strings.Cut(string(out), fpathMarker+"\n")
	if !ok {
		return nil, errors.New("zsh printed no fpath")
	}
	return strings.Fields(list), nil
}

// loginShell is the user's login shell ($SHELL), by name.
func loginShell() string {
	if s := os.Getenv("SHELL"); s != "" {
		return filepath.Base(s)
	}
	return ""
}

func (a *app) completionScript(shell, name string) ([]byte, error) {
	use := a.root.Use
	defer func() { a.root.Use = use }()
	a.root.Use = name
	var buf bytes.Buffer
	var err error
	switch shell {
	case "zsh":
		err = a.root.GenZshCompletion(&buf)
	case "bash":
		err = a.root.GenBashCompletionV2(&buf, true)
	case "fish":
		err = a.root.GenFishCompletion(&buf, true)
	}
	return buf.Bytes(), err
}

// writeScripts writes the scripts of every name into p.Dir, and removes the ones squadrn wrote
// for them into the shell's other dirs.
func (a *app) writeScripts(e completion.Env, p completion.Plan, names []string) error {
	if err := os.MkdirAll(p.Dir, 0o755); err != nil {
		return err
	}
	for _, n := range names {
		b, err := a.completionScript(p.Shell, n)
		if err == nil {
			err = fsx.WriteBytes(completion.File(p.Shell, p.Dir, n), b, 0o644)
		}
		if err != nil {
			return err
		}
		for _, d := range e.Dirs(p.Shell) {
			if d != p.Dir {
				removeScript(p.Shell, d, n)
			}
		}
	}
	return nil
}

// removeScript deletes the script squadrn generated for name in dir; false when there is none.
func removeScript(shell, dir, name string) bool {
	file := completion.File(shell, dir, name)
	b, err := os.ReadFile(file)
	return err == nil && generatedCompletion(b, name) && os.Remove(file) == nil
}

// writeCompletions updates the completion scripts on install, update and renames: those of the
// login shell, and of the other shells whose completion dir exists. It never edits a startup
// file; when the shell does not load the dir yet it says how (completion install).
func (a *app) writeCompletions(names, removedLinks []string) {
	for _, link := range removedLinks {
		a.removeCompletions(filepath.Base(link))
	}
	if !a.cfg.CompletionEnabled() {
		return
	}
	e := a.completionEnv()
	login := loginShell()
	for _, shell := range completion.Shells {
		p, _ := e.Plan(shell, names)
		if shell != login && !fsx.IsDir(p.Dir) {
			continue
		}
		if err := a.writeScripts(e, p, names); err != nil {
			a.warnf("%s completion: %v", shell, err)
			continue
		}
		hint := ""
		if !p.Active() {
			hint = " (" + shell + " does not load it yet; run: " + a.cmd + " completion install)"
		}
		a.out("  %s completion → %s%s\n", shell, a.tilde(p.Dir), hint)
	}
}

// removeCompletions deletes the completion scripts squadrn generated for name, in every dir it
// may have written them to.
func (a *app) removeCompletions(name string) {
	e := a.completionEnv()
	for _, shell := range completion.Shells {
		for _, d := range e.Dirs(shell) {
			removeScript(shell, d, name)
		}
	}
}

// removeCompletionBlocks takes squadrn's block out of the shell startup files.
func (a *app) removeCompletionBlocks() {
	for _, rc := range a.completionEnv().RCFiles() {
		a.removeBlock(rc)
	}
}

func (a *app) removeBlock(rc string) {
	b, err := os.ReadFile(rc)
	if err != nil {
		return
	}
	content, removed := completion.WithoutBlock(string(b))
	if !removed {
		return
	}
	if err := writeStartupFile(rc, content); err != nil {
		a.warnf("cannot remove squadrn's completion lines from %s: %v", a.tilde(rc), err)
		return
	}
	a.out("  removed completion from %s\n", a.tilde(rc))
}

// writeStartupFile replaces a shell startup file's content, through a symlink (dotfile managers)
// and keeping its permissions.
func writeStartupFile(path, content string) error {
	if real, err := filepath.EvalSymlinks(path); err == nil {
		path = real
	}
	mode := fs.FileMode(0o644)
	if fi, err := os.Stat(path); err == nil {
		mode = fi.Mode().Perm()
	}
	return fsx.WriteBytes(path, []byte(content), mode)
}

func (a *app) supportedShell(shell string) (string, error) {
	if shell == "" {
		shell = loginShell()
	}
	if !slices.Contains(completion.Shells, shell) {
		return "", usagef("tab completion for %q is not supported; name one with --shell (%s)", shell, strings.Join(completion.Shells, ", "))
	}
	return shell, nil
}

// installCompletion writes the scripts where shell loads them, adding squadrn's block to its
// startup file when it has to (after asking, unless yes).
func (a *app) installCompletion(shell string, yes bool) error {
	shell, err := a.supportedShell(shell)
	if err != nil {
		return err
	}
	if err := a.setCompletion(config.CompletionAuto); err != nil {
		return err
	}
	e := a.completionEnv()
	e.System = true
	names := a.names()
	p, _ := e.Plan(shell, names)
	if err := a.writeScripts(e, p, names); err != nil {
		return err
	}
	a.out("%s completion → %s\n", shell, a.tilde(p.Dir))
	if !p.Active() {
		a.out("%s loads it once %s has these lines:\n", shell, a.tilde(p.RC))
		for _, l := range strings.Split(completion.Begin+"\n"+p.Block+"\n"+completion.End, "\n") {
			a.out("  %s\n", l)
		}
		if err := a.confirm(yes, "Add them to %s", a.tilde(p.RC)); err != nil {
			return err
		}
		b, err := os.ReadFile(p.RC)
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		if err := writeStartupFile(p.RC, completion.WithBlock(string(b), p.Block)); err != nil {
			return err
		}
		a.out("added them to %s\n", a.tilde(p.RC))
	}
	a.out("tab completion is on in new %[1]s sessions; in one already open, run: exec %[1]s\n", shell)
	return nil
}

// uninstallCompletion removes the scripts and startup file lines of shell ("" for every shell)
// and turns completion off, so updates leave it alone.
func (a *app) uninstallCompletion(shell string) error {
	shells := completion.Shells
	if shell != "" {
		s, err := a.supportedShell(shell)
		if err != nil {
			return err
		}
		shells = []string{s}
	}
	e := a.completionEnv()
	for _, sh := range shells {
		for _, d := range e.Dirs(sh) {
			for _, n := range a.names() {
				if removeScript(sh, d, n) {
					a.out("  removed %s\n", a.tilde(completion.File(sh, d, n)))
				}
			}
		}
		switch sh {
		case "zsh":
			a.removeBlock(e.RCFiles()[0])
		case "bash":
			a.removeBlock(e.RCFiles()[1])
		}
	}
	if err := a.setCompletion(config.CompletionOff); err != nil {
		return err
	}
	a.out("tab completion is off (config completion = off; %s completion install turns it on)\n", a.cmd)
	return nil
}

// completionCheck is doctor's line for the login shell's tab completion; false for no line
// (a shell squadrn has no completion for).
func (a *app) completionCheck() (doctor.Check, bool) {
	const g = "install"
	shell := loginShell()
	if !slices.Contains(completion.Shells, shell) {
		return doctor.Check{}, false
	}
	if !a.cfg.CompletionEnabled() {
		return doctor.Check{Group: g, Name: shell + " tab completion off (completion = off)", Status: doctor.Skip}, true
	}
	e := a.completionEnv()
	e.System = true
	p, _ := e.Plan(shell, a.names())
	fix := a.cmd + " completion install"
	for _, n := range a.names() {
		want, _ := a.completionScript(shell, n)
		have, err := os.ReadFile(completion.File(shell, p.Dir, n))
		switch {
		case err != nil:
			return doctor.Check{Group: g, Name: shell + " tab completion for " + n + " not installed in " + a.tilde(p.Dir), Status: doctor.Warn, Fix: fix}, true
		case !bytes.Equal(have, want):
			return doctor.Check{Group: g, Name: shell + " tab completion for " + n + " is out of date", Status: doctor.Warn, Fix: fix}, true
		}
	}
	if !p.Active() {
		return doctor.Check{Group: g, Name: shell + " does not load the completion scripts in " + a.tilde(p.Dir), Status: doctor.Warn, Fix: fix}, true
	}
	return doctor.Check{Group: g, Name: shell + " tab completion (" + a.tilde(p.Dir) + ")"}, true
}

// completionShells are the shells completion install and uninstall take.
var completionShells = completion.Shells
