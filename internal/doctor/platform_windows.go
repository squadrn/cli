//go:build windows

package doctor

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"sprawling-agents/internal/fsx"
	"sprawling-agents/internal/proc"
)

// executable: Windows has no execute bit; the extension decides (PATHEXT).
func executable(path string, fi fs.FileInfo) bool {
	if fi.IsDir() {
		return false
	}
	ext := strings.ToUpper(filepath.Ext(path))
	for _, e := range filepath.SplitList(pathext()) {
		if ext == strings.ToUpper(e) {
			return true
		}
	}
	return false
}

// private: access is governed by the profile directory's ACL, not mode bits.
func private(fs.FileInfo) bool { return true }

func pathext() string {
	if v := os.Getenv("PATHEXT"); v != "" {
		return v
	}
	return ".COM;.EXE;.BAT;.CMD"
}

// onPath lists distinct executables named name (with any PATHEXT extension) across PATH.
func onPath(name string) []string {
	var out []string
	seen := map[string]bool{}
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		for _, ext := range filepath.SplitList(pathext()) {
			p := filepath.Join(dir, name+strings.ToLower(ext))
			fi, err := os.Stat(p)
			if err != nil || !executable(p, fi) {
				continue
			}
			if real := strings.ToLower(resolve(p)); !seen[real] {
				seen[real] = true
				out = append(out, p)
			}
		}
	}
	return out
}

// gitRoot is the Git for Windows installation that provides git.exe ("" if none).
func gitRoot() string {
	git, err := exec.LookPath("git")
	if err != nil {
		return ""
	}
	return filepath.Dir(filepath.Dir(git)) // …\Git\cmd\git.exe
}

func firstFile(paths ...string) string {
	for _, p := range paths {
		if fi, err := os.Stat(p); err == nil && fi.Mode().IsRegular() {
			return p
		}
	}
	return ""
}

// powerShellOnly: without Git Bash, Claude Code runs every command through the PowerShell tool.
func powerShellOnly() bool {
	root := gitRoot()
	return root == "" || firstFile(filepath.Join(root, "bin", "bash.exe"), filepath.Join(root, "usr", "bin", "bash.exe")) == ""
}

var opensshVersion = regexp.MustCompile(`OpenSSH_for_Windows_(\d+)\.(\d+)`)

// platform checks what only Windows needs: Git for Windows pieces, the signature tool, the
// claude.exe that runs, the hook entry point, command copies, PowerShell policy, long paths.
func (e Env) platform(ctx context.Context) []Check {
	const g = "windows"
	var out []Check
	root := gitRoot()
	switch {
	case root == "":
		out = append(out, bad(g, Fail, "Git for Windows not found", "winget install --id Git.Git -e"))
	case powerShellOnly():
		out = append(out, bad(g, Warn, "Git Bash not found: Claude Code runs commands through PowerShell only, and the guard relies on its PowerShell analyser", "reinstall Git for Windows with Git Bash"))
	default:
		out = append(out, ok(g, "Git Bash (Claude Code's Bash tool)"))
	}
	if root != "" {
		if firstFile(filepath.Join(root, "usr", "bin", "sh.exe"), filepath.Join(root, "bin", "sh.exe")) == "" {
			out = append(out, bad(g, Fail, "sh.exe of Git for Windows not found: repository hooks cannot be chained", "reinstall Git for Windows"))
		}
		if firstFile(filepath.Join(root, "usr", "bin", "ssh-keygen.exe")) != "" {
			out = append(out, ok(g, "ssh-keygen for release signatures (Git for Windows)"))
		} else if v, err := proc.Output(ctx, proc.Options{}, filepath.Join(os.Getenv("SystemRoot"), "System32", "OpenSSH", "ssh.exe"), "-V"); err == nil || len(v) > 0 {
			out = append(out, e.opensshCheck(g, string(v)))
		} else {
			out = append(out, bad(g, Warn, "no ssh-keygen that can verify release signatures", "reinstall Git for Windows"))
		}
	}
	if p, err := e.LookPath("claude"); err == nil {
		if ext := strings.ToLower(filepath.Ext(p)); ext == ".cmd" || ext == ".bat" {
			out = append(out, bad(g, Warn, "claude on PATH is a "+ext+" shim ("+e.tilde(p)+"): ctrl-c prompts \"Terminate batch job\"", "irm https://claude.ai/install.ps1 | iex, and put %USERPROFILE%\\.local\\bin first in PATH"))
		}
	}
	out = append(out, e.hookProbe(ctx, g))
	out = append(out, e.commandCopies(g)...)
	if v, err := proc.Output(ctx, proc.Options{}, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command", "Get-ExecutionPolicy"); err == nil {
		if strings.EqualFold(strings.TrimSpace(string(v)), "Restricted") {
			out = append(out, bad(g, Warn, "PowerShell execution policy is Restricted: profile scripts (sprawl completion) do not load", "Set-ExecutionPolicy -Scope CurrentUser RemoteSigned"))
		}
	}
	if v, _ := proc.Output(ctx, proc.Options{}, "git", "config", "--global", "core.longpaths"); strings.TrimSpace(string(v)) != "true" {
		out = append(out, bad(g, Warn, "git core.longpaths is off: deep worktree paths can exceed 260 characters", "git config --global core.longpaths true"))
	}
	if !fsx.Protected(e.Layout.DataDir) {
		out = append(out, bad(g, Warn, e.tilde(e.Layout.DataDir)+" is outside your profile and writable by other users", "reinstall, or keep sprawl's directories under %USERPROFILE%"))
	}
	return out
}

func (e Env) opensshCheck(g, version string) Check {
	m := opensshVersion.FindStringSubmatch(version)
	if m != nil {
		major, _ := strconv.Atoi(m[1])
		minor, _ := strconv.Atoi(m[2])
		if major > 8 || major == 8 && minor >= 1 {
			return ok(g, "ssh-keygen for release signatures (Windows OpenSSH)")
		}
	}
	return bad(g, Warn, "the built-in OpenSSH cannot verify release signatures (needs 8.1)", "install Git for Windows")
}

// hookProbe runs the installed plugin hook the way Claude Code does (exec form through cmd.exe)
// with a force-push payload: it must deny, or the guard is not active.
func (e Env) hookProbe(ctx context.Context, g string) Check {
	bin := e.Layout.Binary()
	if !fsx.IsFile(bin) {
		return bad(g, Fail, "sprawl.exe is not installed in "+e.tilde(bin)+": plugin hooks block every command", "re-run install.ps1")
	}
	payload := `{"cwd":"` + strings.ReplaceAll(os.TempDir(), `\`, `/`) + `","tool_name":"PowerShell","tool_input":{"command":"git push --force origin main"}}`
	out, err := proc.Output(ctx, proc.Options{Stdin: strings.NewReader(payload)}, "cmd.exe", "/d", "/c", `"`+bin+`" hook guard-git || exit /b 2`)
	if err != nil || !strings.Contains(string(out), `"permissionDecision":"deny"`) {
		return bad(g, Fail, "the guard hook did not deny a force push when run like Claude Code runs it", "re-run install.ps1; then sprawl doctor")
	}
	return ok(g, "guard hook runs through cmd.exe and denies a force push")
}

// commandCopies reports command names whose copy differs from the installed binary: an update
// could not replace them, so they would run old guard code.
func (e Env) commandCopies(g string) []Check {
	var names []string
	if b, err := os.ReadFile(filepath.Join(e.Layout.DataDir, "commands.json")); err == nil {
		_ = json.Unmarshal(b, &names)
	}
	want := sha(e.Layout.Binary())
	var out []Check
	for _, n := range names {
		p := filepath.Join(e.Layout.BinDir, n+".exe")
		if got := sha(p); got == "" || got != want {
			out = append(out, bad(g, Fail, e.tilde(p)+" is not the installed sprawl (stale or missing copy)", "close running sessions, then: sprawl self-install or re-run install.ps1"))
		}
	}
	return out
}

func sha(p string) string {
	f, err := os.Open(p)
	if err != nil {
		return ""
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return ""
	}
	return hex.EncodeToString(h.Sum(nil))
}
