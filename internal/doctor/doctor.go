// Package doctor checks that squadrn, the agent CLIs and every account are set up correctly.
package doctor

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/squadrn/squadrn/internal/account"
	"github.com/squadrn/squadrn/internal/config"
	"github.com/squadrn/squadrn/internal/fsx"
	"github.com/squadrn/squadrn/internal/githook"
	"github.com/squadrn/squadrn/internal/gitver"
	"github.com/squadrn/squadrn/internal/proc"
	"github.com/squadrn/squadrn/internal/provider"
	"github.com/squadrn/squadrn/internal/release"
)

type Status int

const (
	OK Status = iota
	Warn
	Fail
	// Skip: something optional is not there; neither a pass nor a problem.
	Skip
)

func (s Status) String() string {
	switch s {
	case OK:
		return "ok"
	case Warn:
		return "warn"
	case Skip:
		return "skip"
	}
	return "fail"
}

func (s Status) MarshalText() ([]byte, error) { return []byte(s.String()), nil }

type Check struct {
	Group  string `json:"group"`
	Name   string `json:"name"`
	Status Status `json:"status"`
	Fix    string `json:"fix,omitempty"` // what to run when Status != OK
}

// GH runs the GitHub CLI; nil skips checks that need it.
type GH func(ctx context.Context, args ...string) ([]byte, error)

type Env struct {
	Store account.Store
	// Agent returns the driver of a provider; an error means its CLI is unavailable.
	Agent    func(provider.Spec) (provider.Agent, error)
	Layout   release.Layout
	Config   config.Config
	LookPath func(string) (string, error)
	// Self is the running squadrn binary; Names are the command and aliases that must reach it.
	Self  string
	Names []string
	// Command is the user's command name, used in fix hints.
	Command string
	Timeout time.Duration
	// Repo is the repository doctor runs in ("" outside one); its branch protection is checked.
	Repo string
	GH   GH
	// Version is the running squadrn; Latest the newest release seen by the update check ("" if
	// none); Signed reports whether this build verifies release signatures.
	Version, Latest string
	Signed          bool
	// Git returns the installed Git version; nil skips the version check.
	Git func(context.Context) (gitver.Version, error)
	// GitHint is the command that upgrades Git on this system.
	GitHint string
	// ConfigErr is why the config file could not be loaded (nil when it is fine).
	ConfigErr error
	// Usage reports whether an account records its usage limits; nil skips the line.
	Usage func(account.Account) bool
	// Only limits the account checks to this account label; "" checks every account.
	Only string
	// Notifier finds the desktop notifier: its name, what to know about it (or what to install
	// when ok is false); nil skips the check.
	Notifier func() (tool, note string, ok bool)
}

// Run performs every check. Account checks run concurrently.
func Run(ctx context.Context, e Env) []Check {
	var checks []Check
	checks = append(checks, e.install(ctx)...)
	checks = append(checks, e.guard(ctx)...)
	checks = append(checks, e.settings()...)
	checks = append(checks, e.notifications()...)
	checks = append(checks, e.accounts(ctx)...)
	checks = append(checks, e.platform(ctx)...)
	return checks
}

func ok(group, name string) Check { return Check{Group: group, Name: name} }

func bad(group string, s Status, name, fix string) Check {
	return Check{Group: group, Name: name, Status: s, Fix: fix}
}

func (e Env) tilde(p string) string {
	if rel, err := filepath.Rel(e.Store.Home, p); err == nil && !strings.HasPrefix(rel, "..") {
		return filepath.Join("~", rel)
	}
	return p
}

// gitCheck reports Git against the supported minimum (gitver.Min).
func (e Env) gitCheck(ctx context.Context) Check {
	const g = "install"
	if e.Git == nil {
		return ok(g, "git")
	}
	v, err := e.Git(ctx)
	switch {
	case err != nil:
		return bad(g, Fail, err.Error(), e.GitHint)
	case !v.AtLeast(gitver.Min):
		return bad(g, Fail, fmt.Sprintf("git %s is too old (squadrn needs %d.%d or newer)", v, gitver.Min.Major, gitver.Min.Minor), e.GitHint)
	}
	return ok(g, "git "+v.String())
}

func (e Env) install(ctx context.Context) []Check {
	const g = "install"
	var out []Check
	if e.ConfigErr != nil {
		out = append(out, bad(g, Fail, "config invalid: "+e.ConfigErr.Error(), "fix the file, then run: "+e.Command+" doctor"))
	}
	if c, ok := e.installedRelease(); ok {
		out = append(out, c)
	}
	used := e.usedProviders()
	var later []Check
	for _, spec := range provider.All() {
		bin, err := e.LookPath(spec.Binary)
		if err != nil {
			if used[spec.ID] {
				out = append(out, bad(g, Fail, spec.Binary+" not on PATH", spec.InstallHint))
			} // else an optional provider nobody uses
			continue
		}
		// One line per CLI: its version, for bug reports, and how it is installed.
		name, notes := e.version(ctx, spec)
		switch {
		case spec.NativeMarker != "":
			if real, _ := filepath.EvalSymlinks(bin); !strings.Contains(real, spec.NativeMarker) {
				out = append(out, bad(g, Warn, name+" at "+e.tilde(bin)+" is not the native build (no auto-update)", spec.InstallHint))
				break
			}
			out = append(out, ok(g, name+", native build"))
		default:
			out = append(out, ok(g, name))
		}
		for _, n := range notes {
			if n.Group == g {
				out = append(out, n)
			} else {
				later = append(later, n) // after the install group, next to its own group's checks
			}
		}
		if dups := onPath(spec.Binary); len(dups) > 1 {
			for i := range dups {
				dups[i] = e.tilde(dups[i])
			}
			out = append(out, bad(g, Warn, "several "+spec.Binary+" binaries on PATH: "+strings.Join(dups, ", "), "remove the ones not under ~/.local/bin"))
		}
	}
	for _, n := range e.Names {
		if p, err := e.LookPath(n); err != nil {
			out = append(out, bad(g, Warn, n+" not on PATH", e.pathFix()))
		} else if !sameProgram(p, e.Self) {
			out = append(out, bad(g, Warn, n+" on PATH ("+e.tilde(p)+") is a different program than this squadrn", e.Command+" config set command <name>"))
		} else {
			out = append(out, ok(g, n+" on PATH"))
		}
	}
	for _, t := range []struct {
		name   string
		status Status
		why    string
	}{{"git", Fail, "required"}, {"tmux", Warn, "needed by grid, ps, swap"}, {"gh", OK, "optional: PR detection in gc, the branch protection check"}} {
		switch _, err := e.LookPath(t.name); {
		case err != nil && t.status == OK:
			out = append(out, bad(g, Skip, t.name+" not installed ("+t.why+")", ""))
		case err != nil:
			out = append(out, bad(g, t.status, t.name+" missing ("+t.why+")", "install "+t.name))
		case t.name == "git":
			out = append(out, e.gitCheck(ctx))
		default:
			out = append(out, ok(g, t.name))
		}
	}
	if e.Latest != "" {
		out = append(out, bad(g, Warn, "squadrn "+e.Latest+" is available (you have "+release.Tag(e.Version)+")", e.Command+" self-update"))
	}
	if !e.Signed {
		out = append(out, bad(g, Warn, "this build cannot verify release signatures (no signing key built in)", "install an official release"))
	} else {
		out = append(out, ok(g, "release signatures verified on update"))
	}
	if e.Layout.Marketplace() == "" {
		out = append(out, bad(g, Warn, "squadrn plugin files not installed in "+e.tilde(e.Layout.DataDir), reinstallHint))
	} else {
		out = append(out, ok(g, "plugin marketplace"))
	}
	return append(out, later...)
}

// installedRelease compares the installed release with the running binary when that binary is
// the installed one: an install that stopped half way leaves a new binary with an old VERSION
// and plugin.
// usedProviders are the default provider and those with accounts other than an implicit main.
func (e Env) usedProviders() map[string]bool {
	used := map[string]bool{provider.Default: true}
	if e.Store.DefaultProvider != "" {
		used[e.Store.DefaultProvider] = true
	}
	accts, _ := e.Store.List()
	for _, a := range accts {
		used[a.Provider] = true
	}
	return used
}

func (e Env) installedRelease() (Check, bool) {
	const g = "install"
	bin := e.Layout.Binary()
	if !fsx.IsFile(bin) || e.Self == "" {
		return Check{}, false
	}
	if !e.Layout.IsInstalledCopy(e.Self) {
		return Check{}, false // a development build or another copy; self-update warns about it
	}
	running := strings.TrimPrefix(e.Version, "v")
	if !release.Valid(running) {
		return Check{}, false
	}
	if e.Layout.Incomplete(running) {
		return bad(g, Fail, "the installed copy of v"+running+" is incomplete", e.Command+" self-update"), true
	}
	return ok(g, "installed release "+running), true
}

// version names spec's CLI with its version ("claude 2.1.0 (Claude Code)"), and adds a warning
// when it cannot be read, plus what the platform says about that version.
func (e Env) version(ctx context.Context, spec provider.Spec) (string, []Check) {
	if e.Agent == nil {
		return spec.Binary, nil
	}
	ag, err := e.Agent(spec)
	if err != nil {
		return spec.Binary, nil
	}
	vctx, cancel := context.WithTimeout(ctx, e.Timeout)
	b, err := ag.Output(vctx, "", "--version")
	cancel()
	v := strings.TrimSpace(string(b))
	if err != nil || v == "" {
		return spec.Binary, []Check{bad("install", Warn, spec.Binary+" --version failed", spec.InstallHint)}
	}
	return spec.Binary + " " + v, versionNotes(spec, v)
}

// guard checks the three push-guard layers.
func (e Env) guard(ctx context.Context) []Check {
	const g = "push guard"
	out := []Check{e.hookProbe(ctx)}
	switch {
	case !e.Config.GitHooksEnabled():
		out = append(out, bad(g, Warn, "layer 2 (git pre-push hook) is off", e.Command+" config set git_hooks true"))
	case !githook.Installed(e.Layout.GitHooks(), e.Layout.Binary(), e.configFile()):
		out = append(out, bad(g, Warn, "layer 2 (git pre-push hook) is not installed or outdated in "+e.tilde(e.Layout.GitHooks()), reinstallHint))
	default:
		out = append(out, ok(g, "layer 2 (git pre-push hook) installed for sessions squadrn starts"))
	}
	out = append(out, e.branchProtection(ctx)...)
	return out
}

func (e Env) configFile() string {
	return filepath.Join(config.DirsFor(e.Store.Home, config.Slug).Config, "config.toml")
}

// hookProbe runs the installed plugin's guard hook exactly as its hooks.json declares it, the
// way Claude Code does, with a force-push payload: it must deny, or layer 1 is not active.
func (e Env) hookProbe(ctx context.Context) Check {
	const g = "push guard"
	if !fsx.IsFile(e.Layout.Binary()) {
		return bad(g, Fail, "layer 1 (pre-tool hook): squadrn is not installed in "+e.tilde(e.Layout.Binary()), reinstallHint)
	}
	root := filepath.Join(e.Layout.DataDir, "plugin")
	bin, args, err := guardHookCommand(filepath.Join(root, "hooks", "hooks.json"), root)
	if err != nil {
		return bad(g, Fail, "layer 1 (pre-tool hook): "+err.Error(), reinstallHint)
	}
	payload, _ := json.Marshal(map[string]any{
		"cwd":        filepath.ToSlash(os.TempDir()),
		"tool_name":  probeTool,
		"tool_input": map[string]string{"command": "git push --force origin main"},
	})
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	env := append(os.Environ(), "CLAUDE_PLUGIN_ROOT="+root)
	out, err := proc.Output(ctx, proc.Options{Stdin: bytes.NewReader(payload), Env: env}, bin, args...)
	if err != nil || !strings.Contains(string(out), `"permissionDecision":"deny"`) {
		return bad(g, Fail, "layer 1 (pre-tool hook) did not deny a force push when run like Claude Code runs it", reinstallHint+"; then "+e.Command+" doctor")
	}
	return ok(g, "layer 1 (pre-tool hook) denies force pushes")
}

// guardHookCommand reads the PreToolUse guard hook from hooks.json and returns how to start it:
// exec form (command + args) as written, shell form through the platform's shell.
func guardHookCommand(path, root string) (string, []string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", nil, fmt.Errorf("plugin hooks not installed (%w)", err)
	}
	var doc struct {
		Hooks map[string][]struct {
			Hooks []struct {
				Command string   `json:"command"`
				Args    []string `json:"args"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		return "", nil, fmt.Errorf("plugin hooks.json is invalid: %w", err)
	}
	expand := func(s string) string { return strings.ReplaceAll(s, "${CLAUDE_PLUGIN_ROOT}", root) }
	for _, grp := range doc.Hooks["PreToolUse"] {
		for _, h := range grp.Hooks {
			if !strings.Contains(h.Command+" "+strings.Join(h.Args, " "), "guard-git") {
				continue
			}
			if h.Args == nil {
				sh, args := shellForm(h.Command)
				return sh, args, nil
			}
			args := make([]string, len(h.Args))
			for i, a := range h.Args {
				args[i] = expand(a)
			}
			return expand(h.Command), args, nil
		}
	}
	return "", nil, errors.New("plugin hooks.json has no guard-git PreToolUse hook")
}

// protection is the part of GitHub's classic branch protection doctor reads.
type protection struct {
	AllowForcePushes struct{ Enabled bool } `json:"allow_force_pushes"`
	AllowDeletions   struct{ Enabled bool } `json:"allow_deletions"`
	EnforceAdmins    struct{ Enabled bool } `json:"enforce_admins"`
}

// branchProtection checks layer 3, the server: whether the current repository's default branch
// is protected on GitHub against force pushes and deletion, by classic branch protection or a
// ruleset. Only the server can stop a push made outside squadrn.
func (e Env) branchProtection(ctx context.Context) []Check {
	const g = "push guard"
	if e.Repo == "" || e.GH == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, e.Timeout)
	defer cancel()
	out, err := e.GH(ctx, "repo", "view", "--json", "nameWithOwner,defaultBranchRef", "--jq", ".nameWithOwner + \" \" + .defaultBranchRef.name")
	if err != nil {
		return nil // not a GitHub repository, or gh is not logged in
	}
	repo, branch, okSplit := strings.Cut(strings.TrimSpace(string(out)), " ")
	if !okSplit || branch == "" {
		return nil
	}
	weak := Warn
	if powerShellOnly() {
		weak = Fail // the weaker PowerShell analyser is the only layer 1 here
	}
	fix := fmt.Sprintf("protect it: https://github.com/%s/settings/branches", repo)
	if e.rulesetProtects(ctx, repo, branch) {
		return []Check{ok(g, fmt.Sprintf("layer 3 (server) protects %s on %s (ruleset)", branch, repo))}
	}
	body, err := e.GH(ctx, "api", fmt.Sprintf("repos/%s/branches/%s/protection", repo, branch))
	if err != nil {
		msg := err.Error()
		switch {
		case strings.Contains(msg, "404") || strings.Contains(msg, "Branch not protected"):
			return []Check{bad(g, weak, fmt.Sprintf("layer 3 (server): %s on %s is not protected", branch, repo), fix)}
		case strings.Contains(msg, "Upgrade to GitHub Pro") || strings.Contains(msg, "GitHub Pro"):
			return []Check{bad(g, weak, fmt.Sprintf("layer 3 (server): %s on %s cannot be protected on this plan (needs GitHub Pro or a public repository)", branch, repo), "make the repository public or upgrade the plan")}
		case strings.Contains(msg, "403"):
			return []Check{bad(g, Warn, "layer 3 (server): cannot read branch protection of "+repo+" (needs admin)", "ask a repository admin to protect "+branch)}
		}
		return []Check{bad(g, Warn, "layer 3 (server): branch protection unknown: "+msg, "")}
	}
	var p protection
	if err := json.Unmarshal(body, &p); err != nil {
		return []Check{bad(g, Warn, "layer 3 (server): unreadable branch protection of "+repo, "")}
	}
	switch {
	case p.AllowForcePushes.Enabled:
		return []Check{bad(g, weak, fmt.Sprintf("layer 3 (server): %s on %s allows force pushes", branch, repo), fix)}
	case p.AllowDeletions.Enabled:
		return []Check{bad(g, weak, fmt.Sprintf("layer 3 (server): %s on %s can be deleted", branch, repo), fix)}
	}
	out2 := []Check{ok(g, fmt.Sprintf("layer 3 (server) protects %s on %s", branch, repo))}
	if !p.EnforceAdmins.Enabled {
		out2 = append(out2, bad(g, Warn, fmt.Sprintf("layer 3 (server): admins can bypass the protection of %s on %s", branch, repo), "enable \"Do not allow bypassing the above settings\": https://github.com/"+repo+"/settings/branches"))
	}
	return out2
}

// rulesetProtects reports rulesets that block both force pushes and deletion of branch.
func (e Env) rulesetProtects(ctx context.Context, repo, branch string) bool {
	body, err := e.GH(ctx, "api", fmt.Sprintf("repos/%s/rules/branches/%s", repo, branch))
	if err != nil {
		return false
	}
	var rules []struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(body, &rules) != nil {
		return false
	}
	nonFF, del := false, false
	for _, r := range rules {
		nonFF = nonFF || r.Type == "non_fast_forward"
		del = del || r.Type == "deletion"
	}
	return nonFF && del
}

// recommendedDeny keeps an agent from editing the guard itself through its file tools. Edit rules
// cover every file-editing tool; Claude Code warns about Write rules at each start, so a settings
// file is judged on its Edit rules only.
func (e Env) recommendedDeny() []string {
	var rules []string
	for _, dir := range []string{e.Layout.DataDir, config.DirsFor(e.Store.Home, config.Slug).Config} {
		p := filepath.ToSlash(e.tilde(dir)) // Claude's permission rules use / on every platform
		rules = append(rules, "Edit("+p+"/**)")
	}
	return rules
}

func (e Env) settings() []Check {
	const g = "settings & hooks"
	spec := provider.Claude
	path := filepath.Join(e.Store.MainDir(spec), spec.Settings)
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return []Check{ok(g, "no "+spec.Settings), e.denyCheck(nil, path)}
	} else if err != nil {
		return []Check{bad(g, Fail, spec.Settings+" unreadable: "+err.Error(), "")}
	}
	var doc struct {
		Hooks map[string][]struct {
			Hooks []struct {
				Command string `json:"command"`
			} `json:"hooks"`
		} `json:"hooks"`
		Permissions struct {
			Deny []string `json:"deny"`
		} `json:"permissions"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		return []Check{bad(g, Fail, spec.Settings+" is not valid JSON", "fix "+e.tilde(path))}
	}
	out := []Check{ok(g, spec.Settings+" valid")}
	seen := map[string]bool{}
	for _, event := range slices.Sorted(maps.Keys(doc.Hooks)) {
		perEvent := map[string]int{}
		for _, grp := range doc.Hooks[event] {
			for _, h := range grp.Hooks {
				perEvent[h.Command]++
				exe := e.expand(strings.Fields(h.Command + " x")[0])
				if !filepath.IsAbs(exe) || seen[exe] {
					continue
				}
				seen[exe] = true
				if fi, err := os.Stat(exe); err != nil || !executable(exe, fi) {
					out = append(out, bad(g, Fail, "hook not executable: "+e.tilde(exe), "fix or remove it in "+e.tilde(path)))
				}
			}
		}
		for _, cmd := range slices.Sorted(maps.Keys(perEvent)) {
			if n := perEvent[cmd]; n > 1 {
				out = append(out, bad(g, Warn, fmt.Sprintf("%s runs %q %d times", event, cmd, n), "remove the duplicate from "+e.tilde(path)))
			}
		}
	}
	return append(out, e.denyCheck(doc.Permissions.Deny, path))
}

func (e Env) denyCheck(deny []string, path string) Check {
	const g = "settings & hooks"
	var missing []string
	for _, r := range e.recommendedDeny() {
		if !slices.Contains(deny, r) {
			missing = append(missing, r)
		}
	}
	if len(missing) == 0 {
		return ok(g, "agents cannot edit squadrn's files (permissions.deny)")
	}
	b, _ := json.Marshal(missing)
	return bad(g, Warn, "agents can edit squadrn's own files", "add to permissions.deny in "+e.tilde(path)+": "+string(b))
}

func (e Env) accounts(ctx context.Context) []Check {
	accts, err := e.Store.List()
	if err != nil {
		return []Check{bad("accounts", Fail, err.Error(), "")}
	}
	if e.Only != "" {
		accts = slices.DeleteFunc(accts, func(a account.Account) bool { return a.Label() != e.Only })
	}
	per := make([][]Check, len(accts))
	var wg sync.WaitGroup
	for i, a := range accts {
		wg.Go(func() {
			cctx, cancel := context.WithTimeout(ctx, e.Timeout)
			defer cancel()
			per[i] = e.account(cctx, a)
		})
	}
	wg.Wait()
	var out []Check
	for _, c := range per {
		out = append(out, c...)
	}
	return out
}

func (e Env) account(ctx context.Context, a account.Account) []Check {
	g := "account " + a.Label()
	ag, err := e.Agent(a.Spec)
	if err != nil {
		return []Check{bad(g, Fail, err.Error(), a.Spec.InstallHint)}
	}
	var out []Check
	switch s, err := ag.Auth(ctx, a.ConfigDir()); {
	case err != nil:
		out = append(out, bad(g, Fail, "login status unavailable: "+err.Error(), ""))
	case !s.LoggedIn:
		out = append(out, bad(g, Fail, "not logged in", e.Command+" account login "+a.Label()))
	default:
		out = append(out, ok(g, "logged in as "+s.Email))
	}
	if a.Spec.Caps.Plugins {
		out = append(out, e.plugin(ctx, ag, a, g))
	}
	if cs, has := ag.(provider.CredentialStore); has && !a.IsMain() {
		if err := cs.CheckCredentials(a.ConfigDir()); err != nil {
			out = append(out, bad(g, Fail, "credentials may be shared with other accounts: "+err.Error(), e.Command+" account add "+a.Label()))
		} else {
			out = append(out, ok(g, "credentials kept in "+e.tilde(a.Dir)))
		}
	}
	if e.Usage != nil && a.Spec.Caps.UsageReport {
		if e.Usage(a) {
			out = append(out, ok(g, "usage tracking: on"))
		} else {
			out = append(out, ok(g, "usage tracking: off ("+e.Command+" usage setup "+a.Label()+")"))
		}
	}
	if !a.IsMain() {
		if fi, err := os.Stat(a.Dir); err == nil && !private(fi) {
			out = append(out, bad(g, Warn, fmt.Sprintf("%s is readable by other users (%v)", e.tilde(a.Dir), fi.Mode().Perm()), "chmod 700 "+e.tilde(a.Dir)))
		}
		out = append(out, e.links(a, g)...)
	}
	return out
}

func (e Env) plugin(ctx context.Context, ag provider.Agent, a account.Account, g string) Check {
	gi, has := ag.(provider.GuardInstaller)
	if !has {
		return bad(g, Fail, "no git push guard hook for "+a.Spec.Name, "")
	}
	installed, enabled, err := gi.PluginState(ctx, a.ConfigDir())
	fix := e.Command + " account add " + a.Label()
	if a.IsMain() {
		fix = reinstallHint
	}
	// Without a plugin ID the guard is a hook squadrn writes itself (Codex: hooks.json), which
	// the CLI runs only once trusted.
	what, off, on := "squadrn plugin", "disabled", "enabled"
	if a.Spec.Plugin == "" {
		what, off, on = "push guard hook", "not trusted by "+a.Spec.Binary, "installed and trusted"
	}
	switch {
	case err != nil && installed:
		return bad(g, Warn, "cannot check the "+what+": "+err.Error(), "")
	case err != nil:
		return bad(g, Warn, "cannot list plugins", "")
	case !installed:
		return bad(g, Fail, what+" not installed (no git push guard)", fix)
	case !enabled:
		if args := gi.EnablePluginArgs(); len(args) > 0 {
			fix = a.Spec.Binary + " " + strings.Join(args, " ")
		}
		return bad(g, Fail, what+" "+off+" (no git push guard)", fix)
	}
	return ok(g, what+" "+on)
}

func (e Env) links(a account.Account, g string) []Check {
	var out []Check
	entries, _ := os.ReadDir(a.Dir)
	for _, ent := range entries {
		p := filepath.Join(a.Dir, ent.Name())
		if ent.Type()&fs.ModeSymlink != 0 {
			if _, err := os.Stat(p); err != nil {
				out = append(out, bad(g, Warn, "dangling link "+ent.Name(), e.Command+" account add "+a.Label()))
			}
		}
	}
	out = append(out, e.accountSettings(a, g)...)
	for _, item := range a.Spec.Shared {
		src := filepath.Join(e.Store.MainDir(a.Spec), item)
		if _, err := os.Stat(src); err != nil {
			continue
		}
		dst := filepath.Join(a.Dir, item)
		switch _, err := os.Lstat(dst); {
		case errors.Is(err, fs.ErrNotExist):
			// main gained it later: the next session or account add links it.
			out = append(out, bad(g, Warn, item+" is not linked to main yet", e.Command+" account add "+a.Label()))
		case !account.LinksTo(dst, src):
			out = append(out, bad(g, Fail, item+" is not linked to main", e.Command+" account add "+a.Label()+" --relink"))
		}
	}
	return out
}

func (e Env) expand(p string) string {
	p = strings.ReplaceAll(p, "$HOME", e.Store.Home)
	p = strings.ReplaceAll(p, "${HOME}", e.Store.Home)
	if strings.HasPrefix(p, "~/") {
		p = filepath.Join(e.Store.Home, p[2:])
	}
	return strings.Trim(p, `"'`)
}

// resolve follows symlinks; p itself when that fails.
func resolve(p string) string {
	if real, err := filepath.EvalSymlinks(p); err == nil {
		return real
	}
	return p
}

// accountSettings checks a secondary account's own settings copy and its record (ADR 0011).
func (e Env) accountSettings(a account.Account, g string) []Check {
	if d, ok := provider.DriverOf(a.Spec.ID); !ok || d.Settings == nil {
		return nil
	}
	fix := e.Command + " account add " + a.Label()
	name := a.Spec.Settings
	path := account.SettingsPath(a)
	if account.LinksTo(path, filepath.Join(e.Store.MainDir(a.Spec), name)) {
		return []Check{bad(g, Warn, name+" is still a link to main's (it becomes the account's own copy at the next start)", fix)}
	}
	m, recorded, err := account.ReadManaged(a.Dir)
	switch {
	case err != nil:
		return []Check{bad(g, Fail, account.ManagedFile+" unreadable: "+err.Error(), "move it aside, then: "+fix)}
	case !recorded:
		return []Check{bad(g, Warn, name+" not set up from main yet", fix)}
	}
	if fi, err := os.Lstat(path); err != nil || !fi.Mode().IsRegular() {
		return []Check{bad(g, Fail, name+" is missing or not a file", fix)}
	}
	var own []string
	for _, k := range slices.Sorted(maps.Keys(m.Keys)) {
		if m.Keys[k].Source == account.SourceUser {
			own = append(own, k)
		}
	}
	msg := name + " follows main"
	if m.Profile != "" {
		msg += " + " + m.Profile + " profile"
	}
	if len(own) > 0 {
		msg += "; changed here: " + strings.Join(own, ", ")
	}
	return []Check{ok(g, msg)}
}

// notifications reports how squadrn notifies (ADR 0012).
func (e Env) notifications() []Check {
	const g = "notifications"
	switch {
	case e.Notifier == nil:
		return nil
	case !e.Config.Notify.On():
		return []Check{ok(g, "notifications off")}
	}
	tool, note, found := e.Notifier()
	switch {
	case !found:
		return []Check{bad(g, Warn, "no desktop notifier", note)}
	case note != "":
		return []Check{bad(g, Warn, "desktop notifications via "+tool, note)}
	}
	return []Check{ok(g, "desktop notifications via "+tool)}
}
