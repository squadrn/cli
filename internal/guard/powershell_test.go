package guard

import (
	"regexp"
	"strings"
	"testing"
)

// TestDecidePowerShell mirrors TestDecide for the PowerShell tool: literal git calls get the
// push judgement; anything that can run git in a way the analyser does not follow asks.
func TestDecidePowerShell(t *testing.T) {
	r := newRepos(t)
	g := newGuard(r)
	dirs := decideDirs(r)
	tests := []decideCase{
		// Literal git calls: the same judgement as the shell parser.
		{Deny, "feat", `git push --force`},
		{Deny, "feat", `git push origin main`},
		{Deny, "feat", `git.exe push origin HEAD:main`},
		{Deny, "feat", `git push origin 'main'`},
		{Deny, "feat", `git push origin "main"`},
		{Deny, "feat", `& git push -f`},
		{Deny, "feat", `& 'C:\Program Files\Git\cmd\git.exe' push origin main`},
		{Deny, "feat", `& "C:\Program Files\Git\cmd\git.exe" push --mirror origin`},
		{Deny, "feat", `GIT push origin main`},
		{Deny, "feat", `npm test; git push origin main`},
		{Deny, "feat", `npm test && git push origin main`},
		{Deny, "feat", "npm test\ngit push origin main"},
		{Deny, "feat", `Get-Date | git push origin main`},
		{Deny, "feat", `$out = git push origin main`},
		{Deny, "feat", `git -c alias.p=push p origin main`},
		{Deny, "feat", `git push origin –force`},
		{Deny, "feat", `git push origin “main”`},
		{Deny, "feat", `cd {main}; git push`},
		{Deny, "feat", `Set-Location -Path {main}; git push`},
		{Deny, "feat", `bash -c 'git push -f'`},
		{Deny, "feat", `pwsh -Command 'git push origin main'`},
		{Deny, "feat", `powershell -c "git push origin main"`},
		{Deny, "alias", `git p origin main`},
		// Runtime values and unanalysable forms ask, whether or not the text says push.
		{Ask, "feat", `git push origin $branch`},
		{Ask, "feat", `git push origin "$branch"`},
		{Ask, "feat", "git pu`sh origin main"},
		{Ask, "feat", `git @args`},
		{Ask, "feat", `git push origin (Get-Branch)`},
		{Ask, "feat", `& $git push origin main`},
		{Ask, "feat", `& ('g'+'it') push origin main`},
		{Ask, "feat", `iex 'git push origin main'`},
		{Ask, "feat", `Invoke-Expression ("git pu"+"sh")`},
		{Ask, "feat", `Invoke-Command { git status }`},
		{Ask, "feat", `Start-Process git -ArgumentList 'push','origin','main'`},
		{Ask, "feat", `saps git`},
		{Ask, "feat", `Set-Alias g git`},
		{Ask, "feat", `function p { git push }`},
		{Ask, "feat", `cmd /c "git push origin main"`},
		{Ask, "feat", `wsl git push origin main`},
		{Ask, "feat", `pwsh -EncodedCommand ZwBpAHQA`},
		{Ask, "feat", `powershell -ec ZwBpAHQA`},
		{Ask, "feat", `pwsh -Command $cmd`},
		{Ask, "feat", `bash -c $script`},
		{Ask, "feat", `bash`},
		{Ask, "feat", `git push origin main --% -f`},
		{Ask, "feat", `$env:GIT_CONFIG_COUNT = 0`},
		{Ask, "feat", `$Env:git_config_parameters = ''`},
		{Ask, "feat", `Remove-Item Env:GIT_CONFIG_COUNT`},
		{Ask, "feat", `[Environment]::SetEnvironmentVariable('GIT_CONFIG_COUNT', '0')`},
		{Ask, "feat", `(git push origin main)`},
		{Ask, "feat", `g push origin main`},
		{Ask, "feat", `schtasks /create /tn x /tr "git push"`},
		// Ordinary PowerShell is allowed.
		{Allow, "feat", `git push -u origin feat-x`},
		{Allow, "feat", `git status; git log --oneline -5`},
		{Allow, "feat", `Get-ChildItem -Recurse | Select-Object -First 5`},
		{Allow, "feat", `$files = Get-ChildItem *.go`},
		{Allow, "feat", `Write-Output "push it"`},
		{Allow, "feat", `npm run build`},
		{Allow, "feat", `& .\build.ps1`},
		{Allow, "feat", `pwsh -File .\deploy.ps1`},
		{Allow, "feat", "# git push origin main\nGet-Date"},
		{Allow, "feat", "$doc = @'\ngit push --force origin main\n'@"},
		{Allow, "feat", `git commit -m 'push the button'`},
		{Allow, "feat", `Remove-Item -Recurse .\dist`},
		{Allow, "feat", `bash -c 'npm test'`},
		{Deny, "feat", `git-push.exe origin main`},
		{Deny, "feat", `$out += git push origin main`},
		{Deny, "feat", `git push origin 'it''s' main`},
		{Deny, "feat", `git push origin "ma""in" main`},
		{Deny, "feat", `Get-Date || git push -f`},
		{Deny, "feat", `Push-Location -LiteralPath {main}; git push`},
		{Deny, "feat", "git push origin main # done"},
		{Ask, "feat", "$doc = @\"\n$name\n\"@; git push origin $doc"},
		{Ask, "feat", `pwsh -c`},
		{Ask, "feat", `bash -c 'git push "'`},
		{Ask, "feat", `Remove-Item Env:*`},
		{Ask, "feat", `Clear-Item Env:GIT_CONFIG_COUNT`},
		{Ask, "feat", `ForEach-Object { git push }`},
		{Ask, "feat", `Write-Output $(git push origin main)`},
		{Allow, "feat", `Write-Output push`},
		{Allow, "feat", `Select-String -Pattern push *.md`},
		{Allow, "feat", `Get-Date --% -x`},
		{Allow, "feat", `Write-Output @'inline'@`},
		{Allow, "feat", `$x = 1; $x -= 1`},
		{Allow, "feat", `cd`},
		{Allow, "feat", `&`},
	}
	for _, tt := range tests {
		t.Run(tt.dir+": "+tt.cmd, func(t *testing.T) {
			d := decidePS(g, tt.command(r), dirs[tt.dir])
			if d.Verdict != tt.want {
				t.Fatalf("got %s (%s), want %s", d.Verdict, d.Reason, tt.want)
			}
			if d.Verdict != Allow && d.Reason == "" {
				t.Fatal("non-allow decision without a reason")
			}
		})
	}
}

func TestPowerShellNesting(t *testing.T) {
	g := Guard{Protected: []string{"main"}, Git: fakeGit{facts: Facts{Branch: "feat"}}}
	deep := strings.Repeat("pwsh -c ", 1) + "'" + strings.Repeat("pwsh -c ", 0) + "git status'"
	if d := decidePS(g, deep, "/r"); d.Verdict != Allow {
		t.Fatalf("one level: %+v", d)
	}
	e := &extractor{ctx: t.Context(), facts: newFactsCache(g.Git)}
	e.powershell("git status", "/r", maxDepth+1)
	if len(e.findings) != 1 || e.findings[0].verdict != Ask {
		t.Fatalf("too deep: %+v", e.findings)
	}
}

// Commands valid in both shells get the same verdict from both analysers.
func TestPowerShellMatchesShell(t *testing.T) {
	r := newRepos(t)
	g := newGuard(r)
	for _, c := range []string{
		`git push origin main`, `git push -f`, `git push -u origin feat-x`, `git push origin HEAD:main`,
		`git status`, `git push --mirror origin`, `git push origin --delete main`, `git -C . push origin main`,
	} {
		sh, ps := decide(g, c, r.feat), decidePS(g, c, r.feat)
		if sh.Verdict != ps.Verdict {
			t.Errorf("%s: shell %s, PowerShell %s (%s)", c, sh.Verdict, ps.Verdict, ps.Reason)
		}
	}
}

// FuzzPowerShellNeverAllowsUnparsedPush: whatever the input, a command that mentions both git and
// push is never allowed unless the analyser understood every word of the git call.
func FuzzPowerShellNeverAllowsUnparsedPush(f *testing.F) {
	for _, s := range []string{"git push origin main", "& $g push", "iex 'git push'", "git pu`sh", "(git push)"} {
		f.Add(s)
	}
	g := Guard{Protected: []string{"main"}, Git: fakeGit{facts: Facts{Branch: "main"}}}
	gitWord := regexp.MustCompile(`(?i)(^|[\s;|&'"/\\])git(\.exe)?([\s;|&'"]|$)`)
	pushWord := regexp.MustCompile(`(?i)(^|[\s'"])push([\s;|&'"]|$)`)
	execWord := regexp.MustCompile(`(?i)(^|[\s;|&(])(iex|invoke-\w+|icm|start-\w+|saps|start)([\s;|&(]|$)`)
	f.Fuzz(func(t *testing.T, cmd string) {
		d := decidePS(g, cmd, "/r")
		if d.Verdict != Allow || !gitWord.MatchString(cmd) || !pushWord.MatchString(cmd) {
			return
		}
		if strings.ContainsAny(cmd, "`$@({") || execWord.MatchString(cmd) {
			t.Fatalf("allowed %q although git may run in a way the analyser cannot follow", cmd)
		}
	})
}
