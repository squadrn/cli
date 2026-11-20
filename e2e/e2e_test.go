//go:build e2e

package e2e

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

const (
	sessionA = "11111111-1111-4111-8111-111111111111"
	sessionB = "22222222-2222-4222-8222-222222222222"
)

// TestE2E installs squadrn through its real installer into a sandbox and drives the main flows
// with the installed command. Sections run in order and share the sandbox; the first failure
// stops the run and prints the transcript of every command so far.
func TestE2E(t *testing.T) {
	s := newSandbox(t)
	start := time.Now()
	sections := []struct {
		name string
		run  func(*testing.T, *sandbox)
	}{
		{"install", testInstall},
		{"cli", testCLI},
		{"config", testConfig},
		{"accounts", testAccounts},
		{"run", testRun},
		{"worktrees", testWorktrees},
		{"grid", testGrid},
		{"usage", testUsage},
		{"swap", testSwap},
		{"guard", testGuard},
		{"doctor", testDoctor},
		{"update", testUpdate},
		{"uninstall", testUninstall},
	}
	for _, sec := range sections {
		if !t.Run(sec.name, func(t *testing.T) { sec.run(t, s) }) {
			return
		}
	}
	t.Logf("e2e passed on %s/%s in %s", runtime.GOOS, runtime.GOARCH, time.Since(start).Round(time.Second))
}

func testInstall(t *testing.T, s *sandbox) {
	write(t, filepath.Join(s.home, ".claude", ".loggedin"), "")
	pub, installer := s.buildRelease(t)
	env := []string{"SQUADRN_INSTALL_BASE=" + pub, "SQUADRN_COMMAND=srn", "SQUADRN_ALIASES=squadrn"}
	var r *result
	if runtime.GOOS == "windows" {
		ps := fmt.Sprintf("& ([scriptblock]::Create((Get-Content -Raw '%s'))); exit $LASTEXITCODE", installer)
		r = s.run(t, opt{env: env}, "powershell", "-NoProfile", "-ExecutionPolicy", "Bypass", "-Command", ps)
	} else {
		r = s.run(t, opt{env: env}, "sh", installer)
	}
	r.exit(0).out(`verified release signature`, `installed squadrn `+regexp.QuoteMeta(version))
	s.check(t, s.exists(s.lookPath("srn")) && s.lookPath("srn") != "srn", "srn is not on the sandbox PATH")
	s.srn(t, "version").exit(0).out(`^squadrn ` + regexp.QuoteMeta(version) + `$`)
	s.run(t, opt{}, "squadrn", "--version").exit(0).out(`^squadrn ` + regexp.QuoteMeta(version) + `$`)
}

func testCLI(t *testing.T, s *sandbox) {
	s.srn(t, "--help").exit(0).out(`account +Manage agent accounts`, `self-update`)
	s.srn(t, "version", "--json").exit(0).out(`"version": "` + regexp.QuoteMeta(version) + `"`)
	s.srn(t, "version", "extra").exit(2)
	s.srn(t, "acount", "ls").exit(2).err(`did you mean account`)
	s.srn(t, "completion", "bash").exit(0).out(`bash completion V2 for srn`)
	s.srn(t, "completion", "zsh").exit(0).out(`#compdef srn`)
	s.srn(t, "completion", "fish").exit(0).out(`complete -c srn`)
	s.srn(t, "completion", "powershell").exit(0).out(`srn`)
	s.srn(t, "completion", "tcsh").exit(2)
}

func testConfig(t *testing.T, s *sandbox) {
	s.srn(t, "config").exit(0).out(`^command +srn$`, `^aliases +squadrn$`)
	s.srn(t, "config", "set", "command", "sqx").exit(0).out(`linked ~/.local/bin/sqx`, `removed ~/.local/bin/srn`)
	s.run(t, opt{}, "sqx", "version").exit(0).out(`^squadrn ` + regexp.QuoteMeta(version) + `$`)
	s.check(t, s.lookPath("srn") == "srn", "srn still on PATH after the command swap")
	s.run(t, opt{}, "sqx", "config", "set", "command", "srn").exit(0).out(`linked ~/.local/bin/srn`)
	s.srn(t, "config", "get", "command").exit(0).out(`^srn$`)
	s.srn(t, "config", "set", "protected_branches", "main,production").exit(0).out(`^protected_branches = main,production$`)
	s.srn(t, "config", "set", "protected_branches", "").exit(0).out(`\(default\)$`)
	s.srn(t, "config", "set", "colour", "blue").exit(2)
}

func testAccounts(t *testing.T, s *sandbox) {
	s.srn(t, "account", "add", "work").exit(0).out(
		`^Claude Code .*· new account claude/work → ~/.claude-work$`,
		`^  profile +standard \(secrets denied, push guard, 30-day history\)$`,
		`^✓ created ~.\.claude-work \(private\)$`,
		`^✓ settings from claude/main \+ standard profile$`,
		`^✓ push guard: pre-tool hook \+ git pre-push$`,
		`^done in [0-9.]+s · log in: srn account login claude/work · check: srn doctor claude/work$`)
	s.check(t, strings.Contains(read(t, filepath.Join(s.home, ".claude-work", "settings.json")), `Bash(git push *)`), "the standard profile did not reach claude/work's settings")
	s.srn(t, "account", "add", "work").exit(0).out(`^✓ claude/work is up to date$`).noOut(`^✓ created`)
	s.srn(t, "account", "add", "strictacct", "--profile", "strict", "--with", "privacy", "-y").exit(0).out(`^✓ settings from claude/main \+ strict profile \+ privacy$`)
	strict := read(t, filepath.Join(s.home, ".claude-strictacct", "settings.json"))
	s.check(t, strings.Contains(strict, `"blockReadsOutsideWorkingDirectories": true`) && strings.Contains(strict, `"DISABLE_TELEMETRY": "1"`) && strings.Contains(strict, `.claude-work/.credentials.json`), "strict profile settings missing")
	s.srn(t, "doctor", "strictacct").exit(1).out(`^account claude/strictacct$`, `settings.json follows main \+ strict profile`).noOut(`^account claude/work$`)
	s.srn(t, "account", "rm", "strictacct", "--yes").exit(0)
	s.srn(t, "account", "ls").exit(0).out(`^claude/main +yes +me@.claude `, `^claude/work +no `)
	s.srn(t, "account", "login", "work").exit(0).out(`^logged in to claude/work as me@.claude-work$`)
	s.srn(t, "account", "rename", "work", "job", "-y").exit(0).out(`renamed claude/work → claude/job`)
	s.check(t, !s.exists(filepath.Join(s.home, ".claude-work")) && s.exists(filepath.Join(s.home, ".claude-job")), "rename did not move the dir")
	s.srn(t, "account", "rename", "job", "work", "-y").exit(0)
	s.srn(t, "account", "login", "work").exit(0)
	s.srn(t, "account", "add", "personal").exit(0)
	s.srn(t, "account", "login", "personal").exit(0)

	// A dir another tool made becomes an account by adopt; its own settings stay its own (ADR 0011).
	write(t, filepath.Join(s.home, ".claude-tool", "settings.json"), `{"model":"haiku"}`+"\n")
	write(t, filepath.Join(s.home, ".claude", "settings.json"), `{"model":"opus","theme":"dark"}`+"\n")
	s.srn(t, "account", "ls").exit(0).out(`unmanaged: ~/.claude-tool \(srn account adopt claude/tool\)`)
	s.srn(t, "account", "adopt", "tool").exit(2)
	s.srn(t, "account", "adopt", "tool", "--yes").exit(0).out(`^adopted claude/tool`, `^! settings: kept your change to model`)
	s.srn(t, "account", "add", "tool", "--yes").exit(0).out(`^✓ claude/tool is up to date$`)
	s.check(t, strings.Contains(read(t, filepath.Join(s.home, ".claude-tool", "settings.json")), `"theme": "dark"`), "main's settings did not reach the adopted account")
	s.srn(t, "account", "rm", "tool").exit(2)
	s.srn(t, "account", "rm", "main", "--yes").exit(2)
	s.srn(t, "account", "rm", "tool", "--yes").exit(0).out(`removed account claude/tool`)
	s.check(t, !s.exists(filepath.Join(s.home, ".claude-tool")), "account rm left ~/.claude-tool")
	s.srn(t, "account", "add", "Bad").exit(2)
	s.srn(t, "-a", "ghost").exit(1).err(`account not found: claude/ghost`)
}

func testRun(t *testing.T, s *sandbox) {
	s.srn(t, "work", "-p", "hi").exit(0).out(`^cfg=.*\.claude-work args=-p hi$`)
	s.srn(t, "-a", "personal", "--", "fix the bug").exit(0).out(`^cfg=.*\.claude-personal args=fix the bug$`)
	s.srn(t, "main", "--", "hi").exit(0).out(`^cfg=<unset> args=hi$`)
	s.run(t, opt{env: []string{"CLAUDE_CONFIG_DIR=" + filepath.Join(s.root, "elsewhere")}}, "srn", "--", "x").exit(0).out(`^cfg=<unset> args=x$`)
}

// repo returns the sandbox repository with a bare remote, creating both on first use.
func (s *sandbox) repo(t *testing.T) string {
	t.Helper()
	repo := filepath.Join(s.root, "repo")
	if s.exists(repo) {
		return repo
	}
	remote := filepath.Join(s.root, "remote.git")
	s.git(t, s.root, "init", "-q", "--bare", "-b", "main", remote)
	s.git(t, s.root, "init", "-q", "-b", "main", repo)
	write(t, filepath.Join(repo, "README"), "e2e\n")
	s.git(t, repo, "add", "README")
	s.git(t, repo, "commit", "-q", "-m", "init")
	s.git(t, repo, "remote", "add", "origin", remote)
	s.git(t, repo, "push", "-q", "origin", "main")
	return repo
}

// addWorktree creates an agent worktree like the agent CLI does for `srn wt <name>`.
func (s *sandbox) addWorktree(t *testing.T, repo, name string) string {
	t.Helper()
	s.git(t, repo, "worktree", "add", "-q", "-b", "worktree-"+name, filepath.Join(".claude", "worktrees", name))
	return filepath.Join(repo, ".claude", "worktrees", name)
}

func testWorktrees(t *testing.T, s *sandbox) {
	repo := s.repo(t)
	sub := filepath.Join(repo, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	s.srnIn(t, sub, "wt", "feat-a").exit(0).out(`^cfg=<unset> args=--worktree feat-a$`)
	s.srnIn(t, repo, "personal", "wt", "feat-b").exit(0).out(`^cfg=.*\.claude-personal args=--worktree feat-b$`)
	s.srnIn(t, repo, "wt", "Bad_Name").exit(2)
	s.srn(t, "wt", "feat-a").exit(1)

	wt := s.addWorktree(t, repo, "feat-a")
	s.srnIn(t, repo, "wt", "ls").exit(0).out(`^feat-a +worktree-feat-a +clean`)
	deps := s.addWorktree(t, repo, "feat-deps")
	write(t, filepath.Join(deps, "pnpm-lock.yaml"), "")
	s.srnIn(t, repo, "deps", "feat-deps").exit(0).out(`pnpm install --frozen-lockfile --prefer-offline in .*feat-deps`)
	s.srnIn(t, repo, "deps", "feat-none").exit(1)
	s.srnIn(t, repo, "wt", "rm", "feat-deps").exit(1).err(`uncommitted changes or untracked files`)
	s.srnIn(t, repo, "wt", "rm", "feat-deps", "--force", "--yes").exit(0).out(`removed worktree feat-deps`)

	// A running session keeps its worktree: wt rm refuses until the session ends.
	held := filepath.Join(s.home, "held.txt")
	_ = os.Remove(held)
	cmd := exec.Command(s.lookPath("srn"), "wt", "feat-a")
	cmd.Dir = repo
	cmd.Env = append(append([]string{}, s.env...), "FAKE_HOLD="+sessionB)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	s.waitFor(t, held, `^[0-9]+$`)
	s.srnIn(t, repo, "wt", "rm", "feat-a").exit(1).err(`feat-a is in use by pid [0-9]+`)
	b, _ := os.ReadFile(held)
	if pid, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil {
		if p, err := os.FindProcess(pid); err == nil {
			_ = p.Kill()
		}
	}
	_ = cmd.Process.Kill()
	_ = cmd.Wait()
	// The killed session's record is stale once its process is gone (Windows takes a moment).
	r := s.srnIn(t, repo, "wt", "rm", "feat-a")
	for deadline := time.Now().Add(15 * time.Second); r.code != 0 && time.Now().Before(deadline); {
		time.Sleep(250 * time.Millisecond)
		r = s.srnIn(t, repo, "wt", "rm", "feat-a")
	}
	r.exit(0).out(`removed worktree feat-a`, `deleted branch worktree-feat-a`)
	s.check(t, !s.exists(wt), "wt rm left %s", wt)

	// gc removes merged worktrees and keeps unmerged ones (dry run first).
	s.addWorktree(t, repo, "feat-merged")
	open := s.addWorktree(t, repo, "feat-open")
	s.git(t, open, "commit", "-q", "--allow-empty", "-m", "wip")
	s.srnIn(t, repo, "gc").exit(0).out(`remove +feat-merged`, `keep +feat-open +unmerged commits`, `dry run`)
	s.srnIn(t, repo, "gc", "--yes").exit(0).out(`removed 1 worktree`)
	s.srnIn(t, repo, "wt", "rm", "feat-open").exit(1).err(`not merged`)
	s.srnIn(t, repo, "wt", "rm", "feat-open", "--force-branch", "--yes").exit(0).out(`deleted branch worktree-feat-open`)
}

func testGrid(t *testing.T, s *sandbox) {
	if runtime.GOOS == "windows" {
		s.srn(t, "grid", "ls").exit(1).err(`not available on Windows`)
		return
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	repo := s.repo(t)
	s.srn(t, "grid", repo, "feat-g", "personal:feat-h").exit(0).out(`claude/main +→ feat-g`, `claude/personal +→ feat-h`)
	calls := filepath.Join(s.home, "calls.log")
	s.waitFor(t, calls, `cfg=<unset> pwd=.*repo args=--worktree feat-g`)
	s.waitFor(t, calls, `cfg=.*\.claude-personal pwd=.*repo args=--worktree feat-h`)
	s.srn(t, "ps").exit(0).out(`^srn-repo +claude/main +feat-g `, `^srn-repo +claude/personal +feat-h `)
	s.srn(t, "grid", "ls").exit(0).out(`^srn-repo +.*repo +2 +no$`)
	s.srn(t, "grid", "attach", "repo").exit(1)
	s.srnIn(t, repo, "grid", "stop").exit(2)
	s.srnIn(t, repo, "grid", "stop", "--yes").exit(0).out(`stopped srn-repo`)
	s.srn(t, "ps").exit(0).out(`^no running agents$`)
}

func testUsage(t *testing.T, s *sandbox) {
	s.srn(t, "usage").exit(0).out(`^ACCOUNT +PLAN`, `^claude/work .* nodata`, `not tracked: `)
	s.srn(t, "usage", "setup").exit(2)
	s.srn(t, "usage", "setup", "--all", "-y").exit(0).out(`^usage tracking on: ~/.claude/settings.json \(claude/main\)$`, `^usage tracking on: ~/.claude-work/settings.json \(claude/work\)$`)
	s.srn(t, "usage", "setup", "work").exit(0).out(`already on`)

	status := `{"session_id":"s1","cwd":"/repo","rate_limits":{"five_hour":{"used_percentage":42,"resets_at":4102444800},"seven_day":{"used_percentage":18.2,"resets_at":4102444800}}}`
	work := "CLAUDE_CONFIG_DIR=" + filepath.Join(s.home, ".claude-work")
	s.run(t, opt{stdin: status, env: []string{work}}, "srn", "hook", "statusline").exit(0).out(`^work · 5h 42% · 7d 18%$`)

	limit := fmt.Sprintf(`{"session_id":%q,"cwd":%q,"hook_event_name":"StopFailure","error":"rate_limit","last_assistant_message":"You've hit your session limit · resets 3:45pm"}`, "s2", filepath.Join(s.root, "elsewhere"))
	s.run(t, opt{stdin: limit, env: []string{work}}, "srn", "hook", "limit").exit(0)
	s.srn(t, "usage", "work").exit(0).out(`^state +limited$`, `^tracking +on$`)
	s.srn(t, "usage", "--json").exit(0).out(`"state": "limited"`, `"used_pct": 42`)

	s.srn(t, "usage", "setup", "--undo", "-y").exit(0).out(`^usage tracking off: `)
	s.srn(t, "usage", "setup", "--undo", "-y").exit(0).out(`not set up`)
}

// projectKey is Claude Code's projects/ dir name for a working directory.
func projectKey(dir string) string {
	if real, err := filepath.EvalSymlinks(dir); err == nil {
		dir = real
	}
	return regexp.MustCompile(`[^A-Za-z0-9]`).ReplaceAllString(dir, "-")
}

func testSwap(t *testing.T, s *sandbox) {
	repo := s.repo(t)
	key := projectKey(repo)
	conv := func(account string) string {
		return filepath.Join(s.home, account, "projects", key, sessionA+".jsonl")
	}
	line := func(cwd string) string {
		b := strings.ReplaceAll(cwd, `\`, `\\`)
		return `{"type":"user","cwd":"` + b + `","sessionId":"` + sessionA + `"}` + "\n" + `{"type":"assistant","message":{"model":"claude-sonnet-4-6"}}` + "\n"
	}
	write(t, conv(".claude-work"), line(repo))
	old := time.Now().Add(-time.Hour)
	_ = os.Chtimes(conv(".claude-work"), old, old)
	work := "CLAUDE_CONFIG_DIR=" + filepath.Join(s.home, ".claude-work")
	limit := fmt.Sprintf(`{"session_id":%q,"cwd":%q,"error":"rate_limit","last_assistant_message":"You've hit your session limit"}`, sessionA, repo)
	s.run(t, opt{stdin: limit, env: []string{work}}, "srn", "hook", "limit").exit(0)

	// No argument: the conversation that hit its limit here moves and resumes.
	s.srnIn(t, repo, "swap").exit(2).err(`pass the account to move the conversation to with --to`)
	s.srnIn(t, repo, "swap", "--to", "personal").exit(0).
		out(`^swap: claude/work → claude/personal \(session `+sessionA+`\)$`, `^cfg=.*\.claude-personal args=--resume `+sessionA+`$`)
	s.check(t, s.exists(conv(".claude-personal")), "the conversation was not copied")

	// --session moves it back; then both copies go on: divergence is refused unless --force.
	s.srnIn(t, repo, "swap", "--session", sessionA, "--to", "work").exit(0).out(`^swap: claude/personal → claude/work`)
	write(t, conv(".claude-work"), line(repo)+`{"type":"user","message":"went on in work"}`+"\n")
	write(t, conv(".claude-personal"), line(repo)+`{"type":"user","message":"went on in personal"}`+"\n")
	older := time.Now().Add(-30 * time.Minute)
	_ = os.Chtimes(conv(".claude-personal"), older, older)
	s.srnIn(t, repo, "swap", "--session", sessionA, "--to", "personal").exit(1).err(`went on in claude/personal after it was copied there`)
	s.srnIn(t, repo, "swap", "--session", sessionA, "--to", "personal", "--force").exit(0).out(`^swap: claude/work → claude/personal`)
	s.srnIn(t, repo, "swap", "--session", "nope", "--to", "work").exit(2)
	s.srnIn(t, repo, "swap", "--session", sessionA, "--to", "ghost").exit(1)

	// --worktree: the newest conversation of that worktree, from main.
	wt := s.addWorktree(t, repo, "feat-s")
	wkey := projectKey(wt)
	write(t, filepath.Join(s.home, ".claude", "projects", wkey, "sid-1.jsonl"), "{}\n")
	s.srnIn(t, repo, "swap", "--worktree", "feat-s", "--to", "personal").exit(0).
		out(`swap feat-s: claude/main → claude/personal \(session sid-1\)`, `^cfg=.*\.claude-personal args=--resume sid-1$`)
	s.check(t, s.exists(filepath.Join(s.home, ".claude-personal", "projects", wkey, "sid-1.jsonl")), "the worktree conversation was not copied")
	s.srnIn(t, repo, "swap", "--worktree", "feat-none", "--to", "personal").exit(1)
}

func testGuard(t *testing.T, s *sandbox) {
	repo := s.repo(t)
	s.git(t, repo, "checkout", "-q", "-b", "feat-x")
	s.git(t, repo, "commit", "-q", "--allow-empty", "-m", "feature")
	cwd := strings.ReplaceAll(repo, `\`, `/`)
	payload := func(command string) string {
		return fmt.Sprintf(`{"cwd":%q,"tool_name":"Bash","tool_input":{"command":%q}}`, cwd, command)
	}

	// Layer 1: the pre-tool hook decides on the command line.
	s.run(t, opt{stdin: payload("git push origin feat-x")}, "srn", "hook", "guard-git").exit(0).noOut(`.`)
	s.run(t, opt{stdin: payload("git push -f origin feat-x")}, "srn", "hook", "guard-git").exit(0).
		out(`"permissionDecision":"deny"`, `force push is not allowed`)
	s.run(t, opt{stdin: payload("git push origin HEAD:main")}, "srn", "hook", "guard-git").exit(0).
		out(`"permissionDecision":"deny"`, `direct push to protected branch ."main`)
	s.run(t, opt{stdin: payload(`git push origin "$BRANCH"`)}, "srn", "hook", "guard-git").exit(0).out(`"permissionDecision":"ask"`)
	s.run(t, opt{stdin: "not json"}, "srn", "hook", "guard-git").exit(0).out(`"permissionDecision":"ask"`)
	if runtime.GOOS != "windows" {
		shim := filepath.Join(s.home, ".local", "share", "squadrn", "plugin", "bin", "squadrn-hook")
		s.run(t, opt{stdin: payload("git push --force origin feat-x")}, shim, "guard-git").exit(0).out(`"permissionDecision":"deny"`)
	}

	// Layer 2: real pushes from a session go through git's pre-push hook, which squadrn wires
	// into every session it starts. The fake agent runs the push in the session's environment.
	push := func(ref string) *result {
		return s.run(t, opt{dir: repo, env: []string{`FAKE_EXEC=["git","push","origin","` + ref + `"]`}}, "srn", "--", "push")
	}
	push("feat-x").exit(0)
	remote := filepath.Join(s.root, "remote.git")
	s.check(t, s.git(t, remote, "rev-parse", "feat-x") == s.git(t, repo, "rev-parse", "HEAD"), "the feature branch did not reach the remote")
	mainBefore := s.git(t, remote, "rev-parse", "main")
	r := push("HEAD:main")
	s.check(t, r.code != 0, "a push to main from a session was not denied")
	r.err(`squadrn guard: direct push to protected branch "main"`)
	s.check(t, s.git(t, remote, "rev-parse", "main") == mainBefore, "main on the remote changed")

	// The hook itself judges the exact refs it is given.
	head := strings.TrimSpace(s.git(t, repo, "rev-parse", "HEAD"))
	zero := strings.Repeat("0", 40)
	s.run(t, opt{dir: repo, stdin: "refs/heads/feat-x " + head + " refs/heads/main " + zero + "\n"}, "srn", "hook", "git", "pre-push", "origin", remote).
		exit(1).err(`direct push to protected branch "main"`)
	s.run(t, opt{dir: repo, stdin: "refs/heads/feat-x " + head + " refs/heads/feat-x " + head + "\n"}, "srn", "hook", "git", "pre-push", "origin", remote).exit(0)
	log, _ := os.ReadFile(filepath.Join(s.home, ".local", "state", "squadrn", "guard.log"))
	s.check(t, strings.Contains(string(log), "pre-push"), "guard.log has no pre-push denial:\n%s", log)
	s.git(t, repo, "checkout", "-q", "main")
}

// knownDoctorFailures lists doctor problems (✗) a platform is known to report in the sandbox;
// each is an open product issue. Windows: the layer-1 probe starts the cmd.exe line of
// hooks.windows.json with Go's argument quoting, and cmd.exe misreads the escaped quotes
// ('\"…squadrn.exe\"' is not recognized). Whether Claude Code's own spawn hits the same is
// open; the guard section checks the guard binary directly.
var knownDoctorFailures = map[string][]string{
	"windows": {"layer 1 (pre-tool hook) did not deny a force push when run like Claude Code runs it"},
}

func testDoctor(t *testing.T, s *sandbox) {
	r := s.srn(t, "doctor")
	if known := knownDoctorFailures[runtime.GOOS]; len(known) > 0 {
		// Known product issues, tracked for a fix: the run fails once they are fixed, so the
		// entry is removed and doctor must pass again.
		r.exit(1)
		var unexpected []string
		for _, line := range strings.Split(norm(r.stdout), "\n") {
			if strings.Contains(line, "✗") && !slices.ContainsFunc(known, func(k string) bool { return strings.Contains(line, k) }) {
				unexpected = append(unexpected, line)
			}
		}
		s.check(t, len(unexpected) == 0, "doctor reports unexpected problems:\n%s", strings.Join(unexpected, "\n"))
		for _, k := range known {
			r.out(regexp.QuoteMeta(k))
		}
	} else {
		r.exit(0)
	}
	r.out(`^install$`, `✓ installed release `+regexp.QuoteMeta(version), `✓ git`, `✓ layer 2 \(git pre-push hook\) installed`,
		`^account claude/work$`, `✓ logged in as me@.claude-work`, `✓ squadrn plugin enabled`)
	if len(knownDoctorFailures[runtime.GOOS]) == 0 {
		r.out(`✓ layer 1 \(pre-tool hook\) denies force pushes`).noOut(`✗`)
	}
	s.srn(t, "doctor", "--json").out(`"group": "push guard"`)
	s.srn(t, "doctor", "a", "b").exit(2)
}

func testUpdate(t *testing.T, s *sandbox) {
	s.srn(t, "self-update", "--check").exit(0).
		out(`^update available: v`+regexp.QuoteMeta(version)+` → `+regexp.QuoteMeta(newer)+`$`, notes, `install it with: srn self-update`)
	s.srn(t, "self-update", "--check", "--json").exit(0).out(`"available": true`, `"latest": "`+regexp.QuoteMeta(newer)+`"`)
	s.srn(t, "self-update", "--json").exit(2)
}

func testUninstall(t *testing.T, s *sandbox) {
	s.srn(t, "self-uninstall").exit(2)
	s.srn(t, "self-uninstall", "--yes", "--purge").exit(0)
	gone := []string{
		filepath.Join(s.home, ".local", "bin", "srn"),
		filepath.Join(s.home, ".local", "bin", "srn.exe"),
		filepath.Join(s.home, ".config", "squadrn"),
		filepath.Join(s.home, ".local", "share", "squadrn"),
	}
	// On Windows the running binary's files go once it has exited.
	deadline := time.Now().Add(20 * time.Second)
	for {
		var left []string
		for _, p := range gone {
			if s.exists(p) {
				left = append(left, p)
			}
		}
		if len(left) == 0 {
			break
		}
		if time.Now().After(deadline) {
			s.fail(t, "self-uninstall --purge left %v", left)
		}
		time.Sleep(250 * time.Millisecond)
	}
	b, _ := os.ReadFile(filepath.Join(s.home, ".claude", ".plugins"))
	s.check(t, !strings.Contains(string(b), "squadrn@squadrn"), "plugin still registered in claude/main")
}
