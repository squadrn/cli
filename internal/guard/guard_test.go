package guard

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type repos struct {
	root, feat, main, prod, squad, remotePush, matching, upstream, alias string
}

func isolateGit(t testing.TB) {
	t.Helper()
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	for _, k := range []string{"GIT_AUTHOR_NAME", "GIT_COMMITTER_NAME"} {
		t.Setenv(k, "t")
	}
	for _, k := range []string{"GIT_AUTHOR_EMAIL", "GIT_COMMITTER_EMAIL"} {
		t.Setenv(k, "t@t")
	}
}

// newRepos creates real repositories covering every configuration the guard reads.
func newRepos(t testing.TB) repos {
	t.Helper()
	isolateGit(t)
	root := t.TempDir()
	r := repos{root: root}
	mk := func(name, branch string, cfg ...string) string {
		dir := filepath.Join(root, name)
		gitRun(t, "", "init", "-q", "-b", branch, dir)
		for i := 0; i+1 < len(cfg); i += 2 {
			gitRun(t, dir, "config", cfg[i], cfg[i+1])
		}
		return dir
	}
	r.feat = mk("feat", "feat-x")
	r.main = mk("on-main", "main")
	r.prod = mk("prod", "feat-x", RepoConfigKey, "production, staging")
	r.squad = mk("squad", "feat-x", "squad.protectedBranches", "staging")
	r.remotePush = mk("remote-push", "feat-x", "remote.origin.push", "HEAD:refs/heads/main")
	r.matching = mk("matching", "feat-x", "push.default", "matching")
	r.upstream = mk("upstream", "feat-x", "push.default", "upstream", "branch.feat-x.remote", "origin", "branch.feat-x.merge", "refs/heads/main")
	r.alias = mk("alias", "feat-x", "alias.p", "push", "alias.sh", "!git push origin main", "alias.st", "status")
	if err := os.MkdirAll(filepath.Join(r.feat, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	return r
}

func gitRun(t testing.TB, dir string, args ...string) string {
	t.Helper()
	if dir != "" {
		args = append([]string{"-C", dir}, args...)
	}
	out, err := exec.Command("git", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func newGuard(r repos) Guard {
	return Guard{Protected: []string{"main", "master", "develop", "release/*"}, Git: GitCLI{}, Home: r.root, GitHooks: true}
}

func decide(g Guard, cmd, dir string) Decision {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return g.Decide(ctx, cmd, dir)
}

func TestDecide(t *testing.T) {
	r := newRepos(t)
	g := newGuard(r)
	dirs := map[string]string{
		"feat": r.feat, "main": r.main, "prod": r.prod, "squad": r.squad, "remote-push": r.remotePush,
		"matching": r.matching, "upstream": r.upstream, "alias": r.alias,
	}

	tests := []struct {
		want Verdict
		dir  string
		cmd  string
	}{
		// Force pushes in every spelling, including git's unique-prefix long options.
		{Deny, "feat", `git push --force`},
		{Deny, "feat", `git push -f origin feat-x`},
		{Deny, "feat", `git push -uf origin feat-x`},
		{Deny, "feat", `git push origin feat-x --force-with-lease`},
		{Deny, "feat", `git push --force-with-lease=feat-x:abc origin feat-x`},
		{Deny, "feat", `git push --force-if-includes origin feat-x`},
		{Deny, "feat", `git push --forc origin feat-x`},
		{Deny, "feat", `git push --for origin feat-x`},
		{Deny, "feat", `git push origin +feat-x`},
		{Deny, "feat", `git push --force origin $BRANCH`},
		// Protected destinations, normalised the way git resolves them.
		{Deny, "feat", `git push origin main`},
		{Deny, "feat", `git push origin Main`},
		{Deny, "feat", `git push origin HEAD:main`},
		{Deny, "feat", `git push origin feat-x:refs/heads/develop`},
		{Deny, "feat", `git push origin feat-x:heads/main`},
		{Deny, "feat", `git push upstream refs/heads/main`},
		{Deny, "feat", `git push origin release/1.2`},
		{Deny, "feat", `git push origin release/1.2/hotfix`},
		{Deny, "feat", `git push origin feat-x main`},
		{Deny, "feat", `git push origin 'main'`},
		{Deny, "feat", `git push origin ma\in`},
		{Deny, "feat", `git push --repo=origin main`},
		// Deleting, wildcards, matching and everything-at-once pushes.
		{Deny, "feat", `git push origin --delete main`},
		{Deny, "feat", `git push origin --del main`},
		{Deny, "feat", `git push origin -d master`},
		{Deny, "feat", `git push origin :master`},
		{Deny, "feat", `git push origin :`},
		{Deny, "feat", `git push origin 'refs/heads/*:refs/heads/*'`},
		{Deny, "feat", `git push --all origin`},
		{Deny, "feat", `git push --al origin`},
		{Deny, "feat", `git push --mirror origin`},
		{Deny, "feat", `git push --mirr origin`},
		{Deny, "feat", `git push --prune origin`},
		{Deny, "feat", `git push --pru origin`},
		{Deny, "feat", `git send-pack origin main`},
		// Bare pushes resolve their real target from configuration.
		{Deny, "main", `git push`},
		{Deny, "main", `git push origin`},
		{Deny, "main", `git push origin HEAD`},
		{Deny, "main", `git push -u origin @`},
		{Deny, "remote-push", `git push`},
		{Deny, "matching", `git push`},
		{Deny, "upstream", `git push`},
		{Deny, "feat", `git -c remote.origin.push=HEAD:main push`},
		{Deny, "feat", `git -c push.default=matching push`},
		// Word obfuscation is undone before judging.
		{Deny, "feat", `git pu''sh origin feat-x:main`},
		{Deny, "feat", `git pu""sh origin main`},
		{Deny, "feat", `git p\ush origin main`},
		{Deny, "feat", `git $'\x70ush' origin main`},
		{Deny, "feat", `Git push origin feat-x:main`},
		{Deny, "feat", `GIT PUSH origin main`},
		{Deny, "feat", `/usr/libexec/git-core/git-push origin feat-x:main`},
		{Ask, "feat", `git $(printf pu)sh origin main`},
		// Aliases: command-line, configured, and shell aliases.
		{Deny, "feat", `git -c alias.p=push p origin feat-x:main`},
		{Deny, "alias", `git p origin main`},
		{Deny, "alias", `git sh`},
		{Allow, "alias", `git st`},
		{Ask, "feat", `GIT_CONFIG_COUNT=1 GIT_CONFIG_KEY_0=alias.p GIT_CONFIG_VALUE_0=push git p origin main`},
		{Ask, "feat", `export GIT_CONFIG_COUNT=0; git push origin feat-x`},
		{Ask, "feat", `unset GIT_CONFIG_COUNT; git push origin feat-x`},
		{Ask, "feat", `env -u GIT_CONFIG_COUNT git push origin feat-x`},
		{Ask, "feat", `git --exec-path=/tmp/x push origin feat-x`},
		// The git-level guard cannot be switched off from the command line.
		{Deny, "feat", `git push --no-verify origin feat-x`},
		{Deny, "feat", `git push --no-v origin feat-x`},
		{Deny, "feat", `git -c core.hooksPath=/dev/null push origin feat-x`},
		// Directory changes: -C, cd and pushd are followed.
		{Deny, "feat", `git -C . push origin "main"`},
		{Deny, "feat", `git -C ` + r.main + ` push`},
		{Deny, "feat", `cd ` + r.main + ` && git push`},
		{Deny, "feat", `pushd ` + r.main + ` && git push`},
		{Deny, "main", `git -C ../feat push origin develop`},
		{Ask, "feat", `git -C "$D" push origin feat-x`},
		// Compound commands, substitutions, functions, control flow.
		{Deny, "feat", `npm test && git push origin main`},
		{Deny, "feat", `cd x; git -c push.default=current push origin develop`},
		{Deny, "feat", `(cd sub && git push origin main)`},
		{Deny, "feat", `echo $(git push -f)`},
		{Deny, "feat", "echo `git push origin main`"},
		{Deny, "feat", `f() { git push -f; }; f`},
		{Deny, "feat", `if true; then git push origin main; fi`},
		{Deny, "feat", `for b in a; do git push origin main; done`},
		// Nested shells, eval, here-documents, here-strings and stdin.
		{Deny, "feat", `bash -c "git push -f"`},
		{Deny, "feat", `sh -lc 'git push origin main'`},
		{Deny, "feat", `bash -o pipefail -c "git push --force"`},
		{Deny, "feat", `bash -O extglob -c 'git push origin main'`},
		{Deny, "feat", `eval "git push origin main"`},
		{Deny, "feat", `bash -c "bash -c 'git push -f'"`},
		{Deny, "feat", "bash <<'EOF'\ngit status\ngit push -f\nEOF"},
		{Deny, "feat", `bash <<< 'git push origin main'`},
		{Ask, "feat", `echo 'git push origin main' | bash`},
		{Ask, "feat", `bash <(echo git push origin main)`},
		{Ask, "feat", `bash -c "$CMD"`},
		{Ask, "feat", `eval "$push_cmd"`},
		// Wrappers and runners.
		{Deny, "feat", `sudo git push -f`},
		{Deny, "feat", `sudo -u bot git push origin main`},
		{Deny, "feat", `env GIT_TRACE=1 git push origin main`},
		{Deny, "feat", `GIT_TRACE=1 git push origin main`},
		{Deny, "feat", `env -S 'git push origin main'`},
		{Deny, "feat", `nohup git push -f &`},
		{Deny, "feat", `timeout -s KILL 30 git push origin main`},
		{Deny, "feat", `xcrun git push origin main`},
		{Deny, "feat", `script -q /dev/null git push origin main`},
		{Deny, "feat", `script -c 'git push origin main' /dev/null`},
		{Deny, "feat", `watch -n1 git push origin main`},
		{Deny, "feat", `find . -exec git push origin main \;`},
		{Deny, "feat", `git submodule foreach git push origin main`},
		{Deny, "feat", `git rebase --exec 'git push origin main' HEAD~1`},
		{Deny, "feat", `git bisect run git push origin main`},
		{Ask, "feat", `echo main | xargs git push origin`},
		{Ask, "feat", `python3 -c 'import subprocess; subprocess.run(["git","push","origin","main"])'`},
		{Ask, "feat", `node -e "require('child_process').execSync('git push origin main')"`},
		{Ask, "feat", "python3 - <<'PY'\nimport os; os.system('git push origin main')\nPY"},
		{Ask, "feat", `$GIT push origin main`},
		// Runtime-only targets and unparsable input ask.
		{Ask, "feat", `git push origin $BRANCH`},
		{Ask, "feat", `git push origin "$(git branch --show-current)"`},
		{Ask, "feat", `git push $REMOTE feat-x`},
		{Ask, "feat", `git push origin ma{i,}n`},
		{Ask, "feat", `git push origin release/*`},
		{Ask, "feat", `git push origin HEAD~1`},
		{Ask, "feat", `git push --frobnicate origin feat-x`},
		{Ask, "feat", `git push -z origin feat-x`},
		{Ask, "feat", `git push --receive-pack=evil origin feat-x`},
		{Ask, "feat", `git push origin main "`},
		// Repository policy can only add protected branches.
		{Deny, "prod", `git push origin production`},
		{Deny, "prod", `git push origin staging`},
		{Deny, "prod", `git push origin main`},
		{Deny, "squad", `git push origin staging`},
		{Deny, "feat", `git config sprawl.protectedBranches none && git push origin main`},

		// Allowed: feature branches, tags, review refs and commands that only mention push.
		{Allow, "feat", `git push`},
		{Allow, "feat", `git push -u origin feat-x`},
		{Allow, "feat", `git push origin HEAD`},
		{Allow, "feat", `git push origin HEAD~1:feat-y`},
		{Allow, "feat", `git push origin --delete feat-old`},
		{Allow, "feat", `git push --follow-tags origin feat-x`},
		{Allow, "feat", `git push --dry-run origin feat-x`},
		{Allow, "feat", `git push -o ci.skip origin feat-x`},
		{Allow, "feat", `git push -uo ci.skip origin feat-x`},
		{Allow, "feat", `git push --repo origin feat-x`},
		{Allow, "feat", `git push origin tag v1.0`},
		{Allow, "feat", `git push origin refs/tags/main`},
		{Allow, "feat", `git push origin HEAD:refs/for/main`},
		{Allow, "feat", `git push origin release-notes`},
		{Allow, "feat", `git push origin feat-x:feat-y`},
		{Allow, "feat", `git status && git log --oneline`},
		{Allow, "feat", `git stash push -m wip`},
		{Allow, "feat", `git log --grep=push`},
		{Allow, "feat", `echo "pushing to main is bad"`},
		{Allow, "feat", `echo "git push -f"`},
		{Allow, "feat", `printf 'git push origin main'`},
		{Allow, "feat", `grep -r "git push --force" docs/`},
		{Allow, "feat", "cat > notes.md <<'EOF'\ngit push --force origin main\nEOF"},
		{Allow, "feat", `bash deploy.sh`},
		{Allow, "feat", `npm run deploy`},
		{Allow, "feat", `ls ~/push`},
		{Allow, "feat", `echo x | xargs echo push`},
	}
	for _, tt := range tests {
		t.Run(tt.dir+": "+tt.cmd, func(t *testing.T) {
			d := decide(g, tt.cmd, dirs[tt.dir])
			if d.Verdict != tt.want {
				t.Fatalf("got %s (%s), want %s", d.Verdict, d.Reason, tt.want)
			}
			if d.Verdict != Allow && d.Reason == "" {
				t.Fatal("non-allow decision without a reason")
			}
		})
	}
}

func TestDecideReasons(t *testing.T) {
	r := newRepos(t)
	g := newGuard(r)
	for cmd, want := range map[string]string{
		`git push -f`:                   "force push is not allowed (-f)",
		`git push --forc origin x`:      "force push is not allowed (--forc)",
		`git push origin HEAD:main`:     `direct push to protected branch "main"`,
		`git push origin --delete main`: `deleting protected branch "main"`,
	} {
		if d := decide(g, cmd, r.feat); !strings.Contains(d.Reason, want) {
			t.Errorf("%s: reason %q, want %q", cmd, d.Reason, want)
		}
	}
	if d := decide(g, `git push`, r.main); !strings.Contains(d.Reason, `current branch "main" is protected`) {
		t.Errorf("bare push reason = %q", d.Reason)
	}
}

func TestDecideOutsideRepository(t *testing.T) {
	r := newRepos(t)
	g := newGuard(r)
	if d := decide(g, `git push origin feat-x`, r.root); d.Verdict != Allow {
		t.Fatalf("feature push outside a repo: %s", d.Verdict)
	}
	if d := decide(g, `git push origin main`, filepath.Join(r.root, "missing")); d.Verdict != Deny {
		t.Fatalf("protected push from a missing directory: %s", d.Verdict)
	}
	if d := decide(g, `git push`, r.root); d.Verdict != Allow {
		t.Fatalf("bare push outside a repo: %s", d.Verdict)
	}
}

func TestGitHooksOffAllowsHooksPathOverride(t *testing.T) {
	r := newRepos(t)
	g := newGuard(r)
	g.GitHooks = false
	if d := decide(g, `git -c core.hooksPath=/x push origin feat-x`, r.feat); d.Verdict != Allow {
		t.Fatalf("got %s", d.Verdict)
	}
}

// fakeGit fails or stalls on demand.
type fakeGit struct {
	facts    Facts
	err      error
	delay    time.Duration
	ancestor func(a, b string) (bool, error)
}

func (f fakeGit) Facts(ctx context.Context, _ string) (Facts, error) {
	select {
	case <-time.After(f.delay):
	case <-ctx.Done():
		return Facts{}, ctx.Err()
	}
	return f.facts, f.err
}

func (f fakeGit) IsAncestor(_ context.Context, _, a, b string) (bool, error) {
	return f.ancestor(a, b)
}

func TestDecideFailsClosed(t *testing.T) {
	g := Guard{Protected: []string{"main"}, Git: fakeGit{err: errors.New("git exploded")}}
	if d := decide(g, `git push origin feat-x`, "/r"); d.Verdict != Ask || !strings.Contains(d.Reason, "git exploded") {
		t.Errorf("git error: %+v", d)
	}
	g.Git = fakeGit{delay: time.Second}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	if d := g.Decide(ctx, `git push origin feat-x; git push other feat-y`, "/r"); d.Verdict != Ask {
		t.Errorf("deadline: %+v", d)
	}
	if time.Since(start) > 500*time.Millisecond {
		t.Errorf("the deadline did not bound the decision")
	}
	// A deny found before the deadline still stands.
	if d := g.Decide(ctx, `git push -f`, "/r"); d.Verdict != Deny {
		t.Errorf("deny after deadline: %+v", d)
	}
}

func TestDecidePrePush(t *testing.T) {
	isolateGit(t)
	dir := t.TempDir()
	gitRun(t, "", "init", "-q", "-b", "feat-x", dir)
	gitRun(t, dir, "commit", "-q", "--allow-empty", "-m", "a")
	a := gitRun(t, dir, "rev-parse", "HEAD")
	gitRun(t, dir, "commit", "-q", "--allow-empty", "-m", "b")
	b := gitRun(t, dir, "rev-parse", "HEAD")
	gitRun(t, dir, "checkout", "-q", "--orphan", "other")
	gitRun(t, dir, "commit", "-q", "--allow-empty", "-m", "c")
	c := gitRun(t, dir, "rev-parse", "HEAD")
	zero := strings.Repeat("0", 40)
	unknown := strings.Repeat("1", 40)

	g := Guard{Protected: []string{"main", "release/*"}, Git: GitCLI{}}
	tests := []struct {
		name string
		u    RefUpdate
		want Verdict
	}{
		{"protected branch", RefUpdate{"refs/heads/feat-x", b, "refs/heads/main", a}, Deny},
		{"protected glob", RefUpdate{"refs/heads/feat-x", b, "refs/heads/release/2", zero}, Deny},
		{"protected delete", RefUpdate{"(delete)", zero, "refs/heads/main", a}, Deny},
		{"protected case", RefUpdate{"refs/heads/feat-x", b, "refs/heads/MAIN", a}, Deny},
		{"new branch", RefUpdate{"refs/heads/feat-x", b, "refs/heads/feat-x", zero}, Allow},
		{"fast-forward", RefUpdate{"refs/heads/feat-x", b, "refs/heads/feat-x", a}, Allow},
		{"non fast-forward", RefUpdate{"refs/heads/other", c, "refs/heads/feat-x", b}, Deny},
		{"unknown remote commit", RefUpdate{"refs/heads/feat-x", b, "refs/heads/feat-x", unknown}, Deny},
		{"delete feature", RefUpdate{"(delete)", zero, "refs/heads/old", a}, Allow},
		{"tag", RefUpdate{"refs/tags/v1", b, "refs/tags/main", zero}, Allow},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := g.DecidePrePush(context.Background(), dir, []RefUpdate{tt.u})
			if d.Verdict != tt.want {
				t.Fatalf("got %s (%s), want %s", d.Verdict, d.Reason, tt.want)
			}
		})
	}
	gitRun(t, dir, "config", RepoConfigKey, "feat-x")
	if d := g.DecidePrePush(context.Background(), dir, []RefUpdate{{"refs/heads/feat-x", b, "refs/heads/feat-x", a}}); d.Verdict != Deny {
		t.Errorf("repo-added protected branch: %+v", d)
	}
	g.Git = fakeGit{err: errors.New("broken")}
	if d := g.DecidePrePush(context.Background(), dir, nil); d.Verdict != Deny {
		t.Errorf("git error must deny in a hook: %+v", d)
	}
}

func TestParsePrePush(t *testing.T) {
	u, err := ParsePrePush(strings.NewReader("refs/heads/a 111 refs/heads/b 222\n\n"))
	if err != nil || len(u) != 1 || u[0].RemoteRef != "refs/heads/b" || u[0].RemoteSHA != "222" {
		t.Fatalf("ParsePrePush = %+v, %v", u, err)
	}
	if _, err := ParsePrePush(strings.NewReader("garbage line\n")); err == nil {
		t.Fatal("malformed input accepted")
	}
}

func TestResolvePushOpt(t *testing.T) {
	for name, want := range map[string]string{"force": "force", "mirr": "mirror", "del": "delete", "no-ver": "no-verify"} {
		if o, ok := resolvePushOpt(name); !ok || o.name != want {
			t.Errorf("resolvePushOpt(%q) = %q, %v", name, o.name, ok)
		}
	}
	for _, ambiguous := range []string{"f", "forc", "no", "force-", "zzz"} {
		if o, ok := resolvePushOpt(ambiguous); ok {
			t.Errorf("resolvePushOpt(%q) resolved to %q", ambiguous, o.name)
		}
	}
}

func TestBranchMatcher(t *testing.T) {
	m := newBranchMatcher([]string{"main", "release/*", "hotfix-*-x", ""})
	for branch, want := range map[string]bool{
		"main": true, "Main": true, "release/1": true, "release/1/2": true, "hotfix-a-x": true,
		"main2": false, "xmain": false, "release": false, "release-notes": false,
	} {
		if got := m.match(branch); got != want {
			t.Errorf("match(%q) = %v", branch, got)
		}
	}
}

func TestVerdictString(t *testing.T) {
	if Allow.String() != "allow" || Deny.String() != "deny" || Verdict(9).String() != "verdict(9)" {
		t.Fatal("Verdict.String")
	}
}

func TestMask(t *testing.T) {
	for in, want := range map[string]string{
		`git push https://user:s3cr3t@github.com/o/r main`:             `git push https://***@github.com/o/r main`,
		`git push https://user:p@ss/w0rd@github.com/o/r main`:          `git push https://***@github.com/o/r main`,
		`git push https://ghp_AbC123xyz456@github.com/o/r`:             `git push https://***@github.com/o/r`,
		`GH_TOKEN=ghp_AbCdEf123456 gh pr create`:                       `GH_TOKEN=*** gh pr create`,
		`NPM_TOKEN=npm_abcdefghijkl npm publish`:                       `NPM_TOKEN=*** npm publish`,
		`DB_PASSWORD=hunter2 ./run`:                                    `DB_PASSWORD=*** ./run`,
		`MY_API_KEY=sk-ant-abcdefghij ./run`:                           `MY_API_KEY=*** ./run`,
		`AWS_SECRET_ACCESS_KEY=abc/def+ghi ./run`:                      `AWS_SECRET_ACCESS_KEY=*** ./run`,
		`aws configure set key AKIAABCDEFGHIJKLMNOP`:                   `aws configure set key AKIA***`,
		`echo sk-proj-abcdefghijklmnop`:                                `echo sk-proj-***`,
		`git -c http.extraheader="Authorization: Bearer abc.def" push`: `git -c http.extraheader="Authorization: ***" push`,
		`curl -H "x: Bearer abc.def" u`:                                `curl -H "x: Bearer ***" u`,
		`deploy --password=hunter2 --token 'x y' --api-key k`:          `deploy --password=*** --token *** --api-key ***`,
		`login --password "a b c"`:                                     `login --password ***`,
		`git push origin main`:                                         `git push origin main`,
	} {
		if got := Mask(in); got != want {
			t.Errorf("Mask(%q)\n got  %q\n want %q", in, got, want)
		}
	}
}

func TestLogRecord(t *testing.T) {
	path := filepath.Join(t.TempDir(), "logs", "guard.log")
	l := Log{Path: path}
	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	l.Record(at, "pretool", Decision{Verdict: Deny, Reason: "force"}, "/repo", "git push https://u:p@h/r -f\nrm -rf x")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := "2026-01-02T03:04:05Z\tdeny\tpretool\t/repo\tforce\tgit push https://***@h/r -f⏎rm -rf x\n"
	if string(b) != want {
		t.Fatalf("log = %q", b)
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o600 {
		t.Errorf("log mode = %v", fi.Mode().Perm())
	}
	if fi, _ := os.Stat(filepath.Dir(path)); fi.Mode().Perm() != 0o700 {
		t.Errorf("log dir mode = %v", fi.Mode().Perm())
	}
	// A symlink planted at the log path is not followed.
	target := filepath.Join(t.TempDir(), "target")
	if err := os.WriteFile(target, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "guard.log")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	Log{Path: link}.Record(at, "pretool", Decision{Verdict: Deny}, "/", "x")
	if b, _ := os.ReadFile(target); len(b) != 0 {
		t.Error("log followed a symlink")
	}
	if got := DefaultLogPath("/h"); !strings.HasSuffix(got, "/sprawl/guard.log") {
		t.Errorf("DefaultLogPath = %q", got)
	}
}

func BenchmarkDecideNoGit(b *testing.B) {
	g := Guard{Protected: []string{"main"}, Git: GitCLI{}}
	ctx := context.Background()
	for b.Loop() {
		g.Decide(ctx, `npm run build && npm test -- --coverage`, "/")
	}
}

func BenchmarkDecidePush(b *testing.B) {
	r := newRepos(b)
	g := newGuard(r)
	ctx := context.Background()
	for b.Loop() {
		g.Decide(ctx, `npm test && git push -u origin feat-x`, r.feat)
	}
}

func TestDecideEdgeForms(t *testing.T) {
	r := newRepos(t)
	g := newGuard(r)
	deep := strings.Repeat("eval ", 40) + "git status"
	tests := []struct {
		want Verdict
		cmd  string
	}{
		{Ask, `GIT_CONFIG_COUNT=1; git push origin feat-x`},
		{Ask, `declare -x GIT_CONFIG_PARAMETERS=x; git push origin feat-x`},
		{Deny, `git -calias.p=push p origin main`},
		{Ask, `git -c "$K"=push p origin feat-x`},
		{Allow, `git --exec-path push origin feat-x`},
		{Ask, `git --config-env alias.p=X p origin main`},
		{Ask, `git --config-env=alias.p=X p origin main`},
		{Deny, `git --git-dir .git push origin main`},
		{Deny, `git --no-pager push origin main`},
		{Allow, `git -C`},
		{Deny, `git $'\x50ush' origin main`},
		{Allow, `echo $'\xzz é \n'`},
		{Deny, `git rebase --exec='git push origin main' HEAD~1`},
		{Deny, `git rebase -x 'git push origin main' HEAD~1`},
		{Allow, `git frobnicate`},
		{Ask, `git -c alias.a=b -c alias.b=a a`},
		{Deny, `git -c alias.a=b -c alias.b=push a origin main`},
		{Deny, `git -c alias.a='-c alias.b=push b' a origin main`},
		{Deny, `git -c 'alias.x=!git push -f' x`},
		{Deny, `env '-Sgit push origin main'`},
		{Deny, `env --split-string 'git push origin main'`},
		{Deny, `env -i PATH=/bin git push origin main`},
		{Deny, `env -C . git push origin main`},
		{Ask, `env GIT_CONFIG_COUNT=1 git push origin feat-x`},
		{Allow, `env -u HOME git status`},
		{Allow, `script -q /dev/null`},
		{Deny, `timeout 30 git push origin main`},
		{Deny, `command -p git push origin main`},
		{Allow, `python3 -c`},
		{Allow, `python3 build.py`},
		{Allow, `python3`},
		{Ask, `node --eval='require("child_process").execSync("git push")'`},
		{Ask, `perl - <<<'system("git push")'`},
		{Allow, `bash -- deploy.sh`},
		{Ask, `bash --`},
		{Ask, `bash -s`},
		{Ask, `bash`},
		{Deny, `bash --norc -c 'git push -f'`},
		{Deny, `bash +o posix -c 'git push -f'`},
		{Ask, `bash "$SCRIPT"`},
		{Deny, `cd ~/nowhere && git push origin main`},
		{Ask, `git push --$OPT origin feat-x`},
		{Deny, `git push -- origin main`},
		{Allow, `git push --push-option ci.skip origin feat-x`},
		{Allow, `git push --repo=origin`},
		{Ask, `git push origin feat-x:`},
		{Deny, `git send-pack x; git push origin feat-x`},
		{Deny, `find . -name x -exec git push -f {} +`},
		{Allow, `find . -name x -exec`},
		{Ask, deep},
	}
	for _, tt := range tests {
		t.Run(tt.cmd, func(t *testing.T) {
			if d := decide(g, tt.cmd, r.feat); d.Verdict != tt.want {
				t.Fatalf("got %s (%s), want %s", d.Verdict, d.Reason, tt.want)
			}
		})
	}
}

func TestDecideImplicitFromFacts(t *testing.T) {
	cfg := func(kv ...string) map[string][]string {
		m := map[string][]string{}
		for i := 0; i+1 < len(kv); i += 2 {
			m[kv[i]] = append(m[kv[i]], kv[i+1])
		}
		return m
	}
	tests := []struct {
		name string
		f    Facts
		cmd  string
		want Verdict
	}{
		{"pushremote", Facts{Branch: "feat", Config: cfg("branch.feat.pushremote", "up", "remote.up.push", "HEAD:main")}, `git push`, Deny},
		{"pushdefault", Facts{Branch: "feat", Config: cfg("remote.pushdefault", "up", "remote.up.push", "HEAD:main")}, `git push`, Deny},
		{"branch remote", Facts{Branch: "feat", Config: cfg("branch.feat.remote", "up", "remote.up.push", "HEAD:main")}, `git push`, Deny},
		{"other remote", Facts{Branch: "feat", Config: cfg("remote.up.push", "HEAD:main")}, `git push`, Allow},
		{"nothing", Facts{Branch: "main", Config: cfg("push.default", "nothing")}, `git push`, Allow},
		{"detached bare", Facts{}, `git push`, Allow},
		{"detached HEAD", Facts{}, `git push origin HEAD`, Ask},
		{"tracking", Facts{Branch: "feat", Config: cfg("push.default", "tracking", "branch.feat.merge", "refs/heads/master")}, `git push`, Deny},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := Guard{Protected: []string{"main", "master"}, Git: fakeGit{facts: tt.f}}
			if d := decide(g, tt.cmd, "/r"); d.Verdict != tt.want {
				t.Fatalf("got %s (%s), want %s", d.Verdict, d.Reason, tt.want)
			}
		})
	}
	if d := decide(Guard{Protected: []string{"main"}}, `git push origin feat`, "/r"); d.Verdict != Ask {
		t.Errorf("no git: %+v", d)
	}
}

func TestGitCLIFacts(t *testing.T) {
	isolateGit(t)
	dir := t.TempDir()
	gitRun(t, "", "init", "-q", "-b", "feat", dir)
	gitRun(t, dir, "commit", "-q", "--allow-empty", "-m", "a")
	gitRun(t, dir, "checkout", "-q", "--detach")
	f, err := GitCLI{}.Facts(context.Background(), dir)
	if err != nil || f.NotRepo || f.Branch != "" {
		t.Fatalf("detached: %+v, %v", f, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := (GitCLI{}).Facts(ctx, dir); err == nil {
		t.Error("cancelled context: no error")
	}
	if _, err := (GitCLI{}).IsAncestor(ctx, dir, "HEAD", "HEAD"); err == nil {
		t.Error("cancelled IsAncestor: no error")
	}
	if ok, err := (GitCLI{}).IsAncestor(context.Background(), dir, "HEAD", "HEAD"); !ok || err != nil {
		t.Errorf("IsAncestor(HEAD, HEAD) = %v, %v", ok, err)
	}
}

func TestLogRecordUnwritable(t *testing.T) {
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	Log{Path: filepath.Join(file, "guard.log")}.Record(time.Now(), "pretool", Decision{}, "/", "x")
	if Ask.String() != "ask" {
		t.Fatal("Ask.String")
	}
}
