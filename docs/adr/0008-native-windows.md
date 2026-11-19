# 0008 — Native Windows

Status: accepted (v1.0.0); Windows is a beta until the open items below are done

## Context

Some developers run Claude Code natively on Windows (PowerShell, Windows Terminal), not in WSL.
squadrn was written for Unix: process groups, process replacement, symlinks, `O_NOFOLLOW`, tmux,
`sh` scripts, execute bits.

Facts about Claude Code on Windows (official docs):

| Topic | Behaviour |
|---|---|
| Shell tools | With Git for Windows the **Bash** tool runs in Git Bash; without it, the **PowerShell** tool runs commands. `PowerShell` is also on by default for claude.ai accounts *with* Git Bash. Hook matchers must be `Bash\|PowerShell`. |
| Hook commands | Shell form runs in Git Bash, or PowerShell without Git Bash. Exec form (`command` + `args`) needs a real `.exe`. A hook that fails to start or errors is non-blocking (fail-open) unless it exits 2 or returns a deny. `${CLAUDE_PLUGIN_ROOT}` is available. |
| Credentials | `<CLAUDE_CONFIG_DIR>\.credentials.json` (default `%USERPROFILE%\.claude`): one login per config dir, as on Unix. |
| Paths | `~/.claude` = `%USERPROFILE%\.claude`; native `claude.exe` in `%USERPROFILE%\.local\bin`; versions in `%USERPROFILE%\.local\share\claude`. |
| Plugins | Local directory marketplaces work and load in place; component paths use `/`. |
| Worktrees | `claude --worktree` works. |
| Not documented | `projects/` folder naming; exec-form path resolution details. |

Facts about Git (verified in Git's source per tag): `git hook run` exists since 2.36,
`--to-stdin` since 2.40. Git documents that `-c` (exported to children as `GIT_CONFIG_PARAMETERS`)
overrides `GIT_CONFIG_COUNT`.

## Decision

### Scope

| Feature | Windows |
|---|---|
| Run as an account, picker, `account …`, provisioning, plugin | Yes |
| `wt`, `wt ls/rm`, `gc`, `deps` | Yes |
| Push guard layer 1 (Bash and PowerShell tools) and layer 2 (git pre-push) | Yes |
| `doctor`, `config`, `usage`, update notices, `self-update`, `--rollback`, `self-uninstall` | Yes |
| `install.ps1`, signed releases, PowerShell completion | Yes |
| `swap` outside a grid | Yes (ADR 0009: found by session ID; a running session is terminated after confirmation) |
| `grid`, `ps`, `grid ls/attach/stop`, `swap` in a pane | No (tmux). Refused by one check in `cli` with a clear message |
| Desktop notifications | No-op |
| Requirements | Windows 10 21H2+/11, **Git for Windows** (provides git, Git Bash, `sh.exe`, `ssh-keygen`) |

### Principles

1. **macOS and Linux behaviour is pinned.** Golden outputs (see *Testing*) fail on any change to
   Unix behaviour that is not intended; Unix release archives keep their names and contents.
   Shared changes are inert on Unix (matcher, zip support, PowerShell analyser, extra archives).
2. **Seams, not switches.** Platform code lives only in `_unix.go` / `_windows.go` pairs next to
   the code that needs it, each pair exposing one small function. No package branches on
   `runtime.GOOS` outside such a pair (tests may).
3. **Fail closed stays the rule** (ADR 0006): no text pre-filters, no "ask only if it mentions
   push", no hook that fails open when it cannot start.
4. **Git floor = what the code needs: 2.31** (`git rev-parse --path-format=absolute`), checked
   explicitly by `gitver` instead of failing with a raw git error. Hook chaining does not use
   `git hook run`: it would need 2.40 for `--to-stdin`, and the `-c core.hooksPath=…` it requires
   is exported as `GIT_CONFIG_PARAMETERS`, which outranks squadrn's `GIT_CONFIG_COUNT`, so a push
   started inside a repository hook would skip layer 2.

### Seams

| Seam (pair) | Unix | Windows |
|---|---|---|
| `proc.setup(cmd)` / cancel | process group, `kill(-pid)` | Job Object with `KILL_ON_JOB_CLOSE`, child created suspended → assigned → resumed; cancel = `TerminateJobObject` |
| `claude.run(bin, argv, env)` (behind `Agent.Exec`) | `syscall.Exec` | child process with inherited console handles; `signal.Notify` and drain `os.Interrupt` while it runs (not `Ignore`, which would let ctrl-c kill squadrn); save/restore console modes; return the child's status unclamped |
| `cli.startDetached(cmd)` | `Setsid` | `CREATE_NO_WINDOW \| CREATE_NEW_PROCESS_GROUP \| CREATE_BREAKAWAY_FROM_JOB` (retry without breakaway on `ACCESS_DENIED`) |
| `guard.openLog(path)` | `O_NOFOLLOW` | `CreateFile` with `FILE_FLAG_OPEN_REPARSE_POINT`; refuse a reparse point or a link count > 1 (race-free) |
| `fsx.ReplaceExecutable(src, dst)`, `fsx.CleanOld(dir, names)` | atomic rename; nothing to clean | write temp → rename existing to `.old-<time>` (allowed while running) → rename temp in; retry on sharing violations (Defender); `CleanOld` removes leftovers at start and tolerates `ACCESS_DENIED` |
| `account.link(src, dst)` / `linksTo(dst, src)` | symlink | symlink → directory **junction** → file **copy with recorded hash** (no hard links: editors replace files, which silently breaks them) |
| `githook.isRunnable` / `githook.hookCommand(path, args)` | execute bit; run `path` | any regular file starting with `#!` or ending `.exe`; run through Git for Windows' `sh.exe` (next to `git.exe`), exactly as Git does |
| `release.BinaryName`, `release.AssetName(version, goos, goarch)` | `squadrn`, `.tar.gz` | `squadrn.exe`, `.zip` |
| `cli.enableVT()` | always on | enable `ENABLE_VIRTUAL_TERMINAL_PROCESSING` on stdout/stderr |
| `cli.stopProcess(pid, force)` (`swap`) | `SIGINT`, then `SIGTERM` | `TerminateProcess` after confirmation |

Shared (no pair): `fsx.SameFile(a, b)` replaces `EvalSymlinks` identity checks in `install` and
`doctor`; zip extraction in `release`; the PowerShell analyser in `guard`. All are tested on every
platform.

### Components

1. **Layout.** `config.DirsFor` uses `filepath.Join` and honours XDG. On Windows that gives
   `%USERPROFILE%\.local\share\squadrn` etc., the same convention as Claude Code;
   `%USERPROFILE%\.local\bin` is usually on PATH already (Claude's installer adds it).
2. **Commands and aliases on Windows: copies, not links.** Hard links cannot follow a
   rename-aside update and a running link cannot be removed. Each name (`squadrn.exe`, `sqd.exe`,
   …) is a copy updated with `fsx.ReplaceExecutable`. "Ours" is what the manifest
   `<data dir>\commands.json` lists, not a link target; a file squadrn did not install is moved
   aside, never overwritten. `doctor` fails when a listed copy differs from the installed binary
   (stale guard code).
3. **Plugin hooks on Windows: exec form, fail closed.** The Windows archive ships its own
   `plugin/hooks/hooks.json` (built, not rewritten at install) and no `sh` shim. Every hook
   (PreToolUse, Notification, Stop, StopFailure) is exec form through `cmd.exe`, so a missing,
   quarantined or blocked binary blocks the tool call instead of letting it through:
   `cmd.exe /d /c "<plugin root>\..\bin\squadrn.exe" hook guard-git || exit /b 2`.
   Install paths containing `% ^ & !` are refused. Unix keeps its shell-form `hooks.json` and `sh`
   shim. `doctor` *executes* the hook with a probe payload.
4. **Push guard, layer 1.**
   - `hookInput` carries `tool_name`: missing → Bash parser; `PowerShell` → PowerShell analyser;
     anything else → ask.
   - Bash parser (all platforms): unwraps `wsl [-d X] [-u U] [-e|--exec|--]`, `winpty`;
     `git.exe` and Windows paths to git; `cmd /c`, `cmd.exe /c`, `cmd //c` → ask if git may run;
     `start`, `cygstart` → ask; `GIT_CONFIG_*` and `GIT_*` env names matched case-insensitively.
   - **PowerShell analyser: allow-list.** Returns allow only if, after normalising Unicode quotes
     (`‘’‚‛“”„`) and dashes (`–—―`), every git invocation is a bare `git`/`git.exe` or a quoted path
     whose basename matches `^git(-[a-z-]+)?(\.exe)?$` (case-insensitive) with literal arguments
     only, and the command contains none of: backticks, `--%`, splatting (`@x`, `$args`),
     subexpressions, script blocks, `$` in git words, `&` or dot-source call operators,
     `iex`/`Invoke-Expression`, `icm`/`Invoke-Command`, `Start-Process`/`saps`/`start`,
     `Start-Job`/`Start-ThreadJob`, `[scriptblock]`, `.Invoke(`, `Add-Type`, process/runspace
     types, `Set-Alias`/`New-Alias`/`sal`/`nal`, `function`, `cmd`, `wsl`, `bash`, `sh`, `pwsh`,
     `powershell` with command or encoded parameters (prefix-matched, including `/` forms),
     `schtasks`, `*-ScheduledTask`, `*-Cim*`, `*-Wmi*`, environment writes
     (`$env:GIT_*=`, `Remove-Item Env:`, `Set-Item Env:`, `[Environment]::SetEnvironmentVariable`).
     Anything else **asks when git could run — whether or not the text says "push"**; unknown
     commands whose arguments contain `push` or a repository push alias ask. Literal git calls
     go through the push judgement (deny/ask/allow). `-EncodedCommand` is never decoded.
5. **Push guard, layer 2.** The shims run through Git for Windows' `sh` (paths written with `/`).
   Chaining to the repository's own hook is the same on every platform: squadrn resolves the
   original hook and runs it **with the environment unchanged**, so a `git push` started inside a
   repository hook still reaches squadrn's pre-push. On Windows the hook runs through `sh.exe`
   (seam above), since Go reports no execute bit there.
6. **Shared items between accounts.** Mode order per seam above. Copies record a hash at copy
   time (`.squadrn-copies.json`); a later launch refreshes a copy only if it still matches that
   hash, otherwise reports a conflict (never silently overwrites edits made in a secondary
   account). Deletion paths (`account rm`, `gc`, `self-uninstall`) detect reparse points on an
   unfollowed handle and never recurse through a junction into main's directories.
7. **Install (`install.ps1`).** Two bootstraps, one per shell family: `install.sh` (macOS and
   Linux) and `install.ps1` (Windows); no per-OS scripts, no "universal" installer. Each does only
   what must happen before the binary can be trusted — check requirements, download, verify the
   signature and checksum, extract — then hands over to `squadrn self-install`, where every other
   step lives once, in tested Go (ADR 0007). Both share one parameter contract, messages and exit
   codes, are rendered from one script (`render-install.sh`), and download over HTTPS from the
   GitHub release (`Invoke-WebRequest`; no `gh`, no GitHub account). `install.ps1` is ASCII only
   (Windows PowerShell 5.1 misreads BOM-less UTF-8) and checked by PSScriptAnalyzer in CI. Steps:
   require Git for Windows ≥ 2.31 → download → signature with `ssh-keygen` from Git for Windows
   (`usr\bin`, absolute path), else built-in OpenSSH only if ≥ 8.1 (absolute `System32` path),
   else refuse → checksum → extract → `squadrn.exe self-install`. PATH via
   `[Environment]::SetEnvironmentVariable('Path', …, 'User')` reading the raw registry value. The
   documented invocations work under the default execution policy (`irm … | iex`, or a script
   block). `install.sh` detects MINGW/MSYS and points to `install.ps1`.
8. **Update, rollback, uninstall.** `self-update` replaces the binary, every command copy and the
   snapshot with `fsx.ReplaceExecutable`; errors renaming `previous/` are retried and reported (no
   silent wrong rollback). `self-uninstall` on Windows schedules deletion of the running
   executable with a detached `cmd.exe` cleanup after exit.
9. **Directory ACLs.** When squadrn's data/bin directories are created outside `%USERPROFILE%`
   (XDG, other drives), they get a protected DACL (owner, SYSTEM, Administrators); `doctor`
   verifies it. Inside the profile, the profile's ACL already applies.
10. **Doctor on Windows.** Git for Windows and its version; Git Bash; `sh.exe`; signature tool;
    `claude.exe` (warn if `claude.cmd`/npm shim is found first); hook probe (component 3); stale
    command copies; execution policy before offering completion; `core.longpaths`;
    PowerShell-only mode makes missing branch protection a **Fail**.
11. **Code signing.** Unsigned binaries trigger SmartScreen and are blocked by Smart App Control
    on fresh Windows 11. The beta ships unsigned; general availability requires an Authenticode
    signature (see *Open items*).

### Security considerations

- Layer 1 for PowerShell is an allow-list that asks on anything unrecognised; layer 2 covers
  pushes neither analyser sees. Residual risks shared with Unix (server-side protection is the
  only stop): `ssh host git push`, libgit2/JGit tools that skip hooks, `gh api` ref updates,
  scripts written and run by the agent. Windows adds launchers that drop the environment
  (`Start-Process -UseNewEnvironment`, scheduled tasks, WMI, `wsl`); layer 1 asks or denies on
  them when git may run.
- Hooks fail closed (component 3); command copies are checked for staleness; data directories
  outside the profile get a protected DACL.

### Testing and CI

- **Unix golden files**: every `--help`, `completion bash|zsh|fish`, the guard decision corpus
  (every `TestDecide` row through `squadrn hook guard-git`), the install layout listing after
  `self-install`, and `hooks.json`.
- `.gitattributes` with `* text=auto eol=lf`.
- CI: a Linux job runs `GOOS=windows go vet` and golangci-lint so Windows files are always
  compiled and linted; `windows-latest` runs `go test ./...` (no `-race`), the testscript suite
  with a Go-built fake `claude.exe`, Unix-only scripts marked `[!windows]`, its own cover gates
  (`make cover-gates-windows`), an install/update/rollback/uninstall run (`windows-e2e.ps1`) and
  `install.ps1` as documented in Windows PowerShell 5.1 and PowerShell 7; Defender excludes the
  workspace. A Debian 11 container (Git 2.30) proves the clear "Git too old" refusal.
- Windows unit tests: tree kill of an orphaned grandchild, ctrl-c/ctrl-break survival and exit
  code passthrough, replace-while-running including running copies, link-mode fallbacks with
  injected `ERROR_PRIVILEGE_NOT_HELD`, zip extraction hostile entries on every OS,
  `TestDecidePowerShell` tables plus a differential test against the bash parser and a fuzz
  invariant ("never allow unparsed input that may run git"), hook chaining through `sh.exe`,
  Git version parsing (Apple Git, `.windows.N`, `.vfs`, rc, garbage).
- Release workflow: before a draft is published, the upgrade jobs install the previous release
  with the new installer and `self-update` to the draft on Linux and `windows-latest` (and roll
  back on Linux).
- A manual Windows checklist (Windows 10 and 11, non-admin user, Defender on) gates releases that
  touch Windows code; kept in `docs/maintaining.md`.

## Open items (Windows beta → general availability)

1. **Real-machine checks** that need a logged-in Claude Code on Windows:
   - S1: the exec-form hook through `cmd.exe` (quoting, `${CLAUDE_PLUGIN_ROOT}` expansion);
   - S2: the PowerShell tool's hook payload;
   - S3: `projects/` folder naming;
   - S5: picker keys in Windows Terminal and the classic console;
   - S13: whether PowerShell tool sessions keep state (aliases, functions, variables) between
     calls; if they do, the analyser asks on any git call whose name could be shadowed.
2. **Code signing**: Azure Trusted Signing (about 10 USD/month, needs identity validation), an OV
   certificate (about 200–400 USD/year), or unsigned with documented SmartScreen/Smart App Control
   workarounds.
3. **General availability**: Windows leaves the beta in README, AGENTS.md and the release notes
   once 1 and 2 are done.

## Consequences

- One code base, two platform families; every feature states its Windows behaviour.
- `grid` remains Unix-only.
- Process deadlines kill the process tree on every platform (process group on Unix, Job Object on
  Windows).
