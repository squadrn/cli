// Package doctor checks that sprawl, the agent CLIs and every account are set up correctly.
package doctor

import (
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

	"sprawling-agents/internal/account"
	"sprawling-agents/internal/config"
	"sprawling-agents/internal/fsx"
	"sprawling-agents/internal/githook"
	"sprawling-agents/internal/gitver"
	"sprawling-agents/internal/guard"
	"sprawling-agents/internal/provider"
	"sprawling-agents/internal/release"
)

type Status int

const (
	OK Status = iota
	Warn
	Fail
)

func (s Status) String() string {
	switch s {
	case OK:
		return "ok"
	case Warn:
		return "warn"
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
	// Self is the running sprawl binary; Names are the command and aliases that must reach it.
	Self  string
	Names []string
	// Command is the user's command name, used in fix hints.
	Command string
	Timeout time.Duration
	// Repo is the repository doctor runs in ("" outside one); its branch protection is checked.
	Repo string
	GH   GH
	// Version is the running sprawl; Latest the newest release seen by the update check ("" if
	// none); Signed reports whether this build verifies release signatures.
	Version, Latest string
	Signed          bool
	// Git returns the installed Git version; nil skips the version check.
	Git func(context.Context) (gitver.Version, error)
	// GitHint is the command that upgrades Git on this system.
	GitHint string
}

// Run performs every check. Account checks run concurrently.
func Run(ctx context.Context, e Env) []Check {
	var checks []Check
	checks = append(checks, e.install()...)
	checks = append(checks, e.versions(ctx)...)
	checks = append(checks, e.guard(ctx)...)
	checks = append(checks, e.settings()...)
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
		return bad(g, Fail, fmt.Sprintf("git %s is too old (sprawl needs %d.%d or newer)", v, gitver.Min.Major, gitver.Min.Minor), e.GitHint)
	}
	return ok(g, "git "+v.String())
}

func (e Env) install() []Check {
	const g = "install"
	var out []Check
	for _, spec := range provider.All() {
		bin, err := e.LookPath(spec.Binary)
		switch {
		case err != nil:
			out = append(out, bad(g, Fail, spec.Binary+" not on PATH", spec.InstallHint))
			continue
		case spec.NativeMarker != "":
			if real, _ := filepath.EvalSymlinks(bin); !strings.Contains(real, spec.NativeMarker) {
				out = append(out, bad(g, Warn, spec.Binary+" at "+e.tilde(bin)+" is not the native build (no auto-update)", spec.InstallHint))
				break
			}
			out = append(out, ok(g, spec.Binary+" native build"))
		default:
			out = append(out, ok(g, spec.Binary))
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
		} else if !fsx.SameFile(p, e.Self) {
			out = append(out, bad(g, Warn, n+" on PATH ("+e.tilde(p)+") is a different program than this sprawl", e.Command+" config set command <name>"))
		} else {
			out = append(out, ok(g, n+" on PATH"))
		}
	}
	for _, t := range []struct {
		name   string
		status Status
		why    string
	}{{"git", Fail, "required"}, {"tmux", Warn, "needed by grid, ps, swap"}, {"gh", Warn, "needed by self-update, gc PR detection and the branch protection check"}} {
		switch _, err := e.LookPath(t.name); {
		case err != nil:
			out = append(out, bad(g, t.status, t.name+" missing ("+t.why+")", "install "+t.name))
		case t.name == "git":
			out = append(out, e.gitCheck(context.Background()))
		default:
			out = append(out, ok(g, t.name))
		}
	}
	if e.Latest != "" {
		out = append(out, bad(g, Warn, "sprawl "+e.Latest+" is available (you have v"+strings.TrimPrefix(e.Version, "v")+")", e.Command+" self-update"))
	}
	if !e.Signed {
		out = append(out, bad(g, Warn, "this build cannot verify release signatures (no signing key built in)", "install an official release"))
	} else {
		out = append(out, ok(g, "release signatures verified on update"))
	}
	if e.Layout.Marketplace() == "" {
		out = append(out, bad(g, Warn, "sprawl plugin files not installed in "+e.tilde(e.Layout.DataDir), "re-run install.sh"))
	} else {
		out = append(out, ok(g, "plugin marketplace"))
	}
	return out
}

// versions reports each installed agent CLI's version, for bug reports. Information only.
func (e Env) versions(ctx context.Context) []Check {
	var out []Check
	for _, spec := range provider.All() {
		if _, err := e.LookPath(spec.Binary); err != nil || e.Agent == nil {
			continue
		}
		ag, err := e.Agent(spec)
		if err != nil {
			continue
		}
		vctx, cancel := context.WithTimeout(ctx, e.Timeout)
		b, err := ag.Output(vctx, "", "--version")
		cancel()
		v := strings.TrimSpace(string(b))
		if err != nil || v == "" {
			out = append(out, bad("install", Warn, spec.Binary+" --version failed", spec.InstallHint))
			continue
		}
		out = append(out, ok("install", spec.Binary+" "+v))
	}
	return out
}

// guard checks the three push-guard layers.
func (e Env) guard(ctx context.Context) []Check {
	const g = "push guard"
	var out []Check
	gd := guard.Guard{Protected: []string{"main"}, Git: guard.GitCLI{}}
	sctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if d := gd.Decide(sctx, "git pu''sh --forc origin main", os.TempDir()); d.Verdict == guard.Deny {
		out = append(out, ok(g, "layer 1 (pre-tool hook) denies force pushes"))
	} else {
		out = append(out, bad(g, Fail, "layer 1 self-test did not deny a force push", "report a bug"))
	}
	switch {
	case !e.Config.GitHooksEnabled():
		out = append(out, bad(g, Warn, "layer 2 (git pre-push hook) is off", e.Command+" config set git_hooks true"))
	case !githook.Installed(e.Layout.GitHooks(), e.Layout.Binary()):
		out = append(out, bad(g, Warn, "layer 2 (git pre-push hook) is not installed in "+e.tilde(e.Layout.GitHooks()), "re-run install.sh"))
	default:
		out = append(out, ok(g, "layer 2 (git pre-push hook) installed for sessions sprawl starts"))
	}
	out = append(out, e.branchProtection(ctx)...)
	return out
}

// branchProtection checks layer 3, the server: whether the current repository's default
// branch is protected on GitHub. Only the server can stop a push made outside sprawl.
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
	name := fmt.Sprintf("layer 3 (server) protects %s on %s", branch, repo)
	if _, err := e.GH(ctx, "api", fmt.Sprintf("repos/%s/branches/%s/protection", repo, branch), "--silent"); err != nil {
		msg := err.Error()
		switch {
		case strings.Contains(msg, "404") || strings.Contains(msg, "Branch not protected"):
			status := Warn
			if powerShellOnly() {
				status = Fail // the weaker PowerShell analyser is the only layer 1 here
			}
			return []Check{bad(g, status, fmt.Sprintf("layer 3 (server): %s on %s is not protected", branch, repo),
				fmt.Sprintf("protect it: https://github.com/%s/settings/branches", repo))}
		case strings.Contains(msg, "403"):
			return []Check{bad(g, Warn, "layer 3 (server): cannot read branch protection of "+repo+" (needs admin)", "ask a repository admin to protect "+branch)}
		}
		return []Check{bad(g, Warn, "layer 3 (server): branch protection unknown: "+msg, "")}
	}
	return []Check{ok(g, name)}
}

// recommendedDeny keeps an agent from editing the guard itself through its file tools.
func (e Env) recommendedDeny() []string {
	var rules []string
	for _, dir := range []string{e.Layout.DataDir, config.DirsFor(e.Store.Home, config.Slug).Config} {
		p := filepath.ToSlash(e.tilde(dir)) // Claude's permission rules use / on every platform
		rules = append(rules, "Edit("+p+"/**)", "Write("+p+"/**)")
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
				if strings.HasSuffix(exe, "guard-git.sh") {
					out = append(out, bad(g, Warn, "legacy guard-git.sh hook in "+spec.Settings+"; the sprawl plugin provides the guard", "remove the guard-git.sh entry from "+e.tilde(path)))
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
		return ok(g, "agents cannot edit sprawl's files (permissions.deny)")
	}
	b, _ := json.Marshal(missing)
	return bad(g, Warn, "agents can edit sprawl's own files", "add to permissions.deny in "+e.tilde(path)+": "+string(b))
}

func (e Env) accounts(ctx context.Context) []Check {
	accts, err := e.Store.List()
	if err != nil {
		return []Check{bad("accounts", Fail, err.Error(), "")}
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
		out = append(out, e.plugin(ctx, ag, a, g)...)
	}
	if !a.IsMain() {
		if fi, err := os.Stat(a.Dir); err == nil && !private(fi) {
			out = append(out, bad(g, Warn, fmt.Sprintf("%s is readable by other users (%v)", e.tilde(a.Dir), fi.Mode().Perm()), "chmod 700 "+e.tilde(a.Dir)))
		}
		out = append(out, e.links(a, g)...)
	}
	return out
}

func (e Env) plugin(ctx context.Context, ag provider.Agent, a account.Account, g string) []Check {
	b, err := ag.Output(ctx, a.ConfigDir(), "plugin", "list", "--json")
	if err != nil {
		return []Check{bad(g, Warn, "cannot list plugins", "")}
	}
	var plugins []struct {
		ID      string `json:"id"`
		Enabled bool   `json:"enabled"`
	}
	_ = json.Unmarshal(b, &plugins)
	var out []Check
	status := bad(g, Fail, "sprawl plugin not installed (no git push guard)", e.Command+" account add "+a.Label())
	if a.IsMain() {
		status.Fix = "re-run install.sh"
	}
	for _, p := range plugins {
		if slices.Contains(account.LegacyPluginIDs(), p.ID) {
			out = append(out, bad(g, Warn, "old plugin "+p.ID+" still installed (guard runs twice)", a.Spec.Binary+" plugin uninstall "+p.ID))
		}
		if p.ID == account.PluginID {
			status = ok(g, "sprawl plugin enabled")
			if !p.Enabled {
				status = bad(g, Fail, "sprawl plugin disabled (no git push guard)", a.Spec.Binary+" plugin enable "+account.PluginID)
			}
		}
	}
	return append(out, status)
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
	for _, item := range a.Spec.Shared {
		src := filepath.Join(e.Store.MainDir(a.Spec), item)
		if _, err := os.Stat(src); err != nil {
			continue
		}
		if !account.LinksTo(filepath.Join(a.Dir, item), src) {
			out = append(out, bad(g, Fail, item+" is not linked to main", e.Command+" account add "+a.Label()))
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

// onPath lists distinct executables named name across PATH.

func resolve(p string) string {
	if real, err := filepath.EvalSymlinks(p); err == nil {
		return real
	}
	return p
}
