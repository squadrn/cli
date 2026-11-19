# 0008 — Native Windows

## Status

Accepted (revision 2, after a five-role design review: architecture, security, Windows platform,
release/DevOps, QA). P1–P4 are implemented; Windows ships as a beta from v1.0.0-rc.1. P5 (real-machine
spikes S1–S3, S5, S13, code signing, general availability) is open. Supersedes the "Linux and macOS
only" rule in AGENTS.md.

## Context

Some developers run Claude Code natively on Windows (PowerShell, Windows Terminal), not in WSL.
squadrn does not build for Windows today (`internal/proc` uses `Setpgid`/`kill(-pid)`), and about
20 files rely on Unix behaviour: process replacement, symlinks, `O_NOFOLLOW`, tmux, `sh` scripts,
execute bits.

Facts about Claude Code on Windows (official docs, verified 2026-11-18):

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
| `doctor`, `config`, update notices, `self-update`, `--rollback`, `self-uninstall` | Yes |
| `install.ps1`, signed releases, PowerShell completion | Yes |
| `swap` outside a grid | Yes (ADR 0009 S1: found by session ID; a running session is terminated after confirmation) |
| `grid`, `ps`, `grid ls/attach/stop`, `swap` in a pane | No (tmux). Refused by one check in `cli` with a clear message |
| Desktop notifications | No-op (already the case for unknown platforms) |
| Requirements | Windows 10 21H2+/11, **Git for Windows** (provides git, Git Bash, `sh.exe`, `ssh-keygen`) |

### Principles

1. **macOS and Linux behaviour is frozen.** A regression safety net (golden outputs, see
   *Testing*) lands before any refactor; existing tests pass unchanged; Unix release archives keep
   their names and contents. The only shared changes are inert on Unix (matcher, zip support,
   PowerShell analyser, extra archives).
2. **Seams, not switches.** Platform code lives in `_unix.go` / `_windows.go` pairs next to the
   code that needs it, each pair exposing one small function. Domain logic never branches on
   `runtime.GOOS`; `cli` may (it already does for notifications).
3. **Fail closed stays the rule** (ADR 0006): no text pre-filters, no "ask only if it mentions
   push", no hook that fails open when it cannot start.
4. **Git floor = what the code needs today: 2.31** (`git rev-parse --path-format=absolute`,
   `internal/worktree/git.go`). Checked explicitly instead of failing with a raw git error. No new
   requirement for anyone. (Revision 1 proposed 2.36 for `git hook run`; dropped, see *Review
   record* R1.)

### Seams

| Seam (pair) | Unix (moved verbatim) | Windows |
|---|---|---|
| `proc.setup(cmd)` / cancel | process group, `kill(-pid)` | Job Object with `KILL_ON_JOB_CLOSE`, child created suspended → assigned → resumed; cancel = `TerminateJobObject` |
| `claude.run(bin, argv, env)` (behind `Agent.Exec`) | `syscall.Exec` | child process with inherited console handles; `signal.Notify` and drain `os.Interrupt` while it runs (not `Ignore`, which would let ctrl-c kill squadrn); save/restore console modes; return the child's status unclamped as `childExit` |
| `cli.detach(cmd)` | `Setsid` | `CREATE_NO_WINDOW \| CREATE_NEW_PROCESS_GROUP \| CREATE_BREAKAWAY_FROM_JOB` (retry without breakaway on `ACCESS_DENIED`) |
| `guard.openLog(path)` | `O_NOFOLLOW` | `CreateFile` with `FILE_FLAG_OPEN_REPARSE_POINT`; refuse a reparse point or a link count > 1 (race-free) |
| `fsx.ReplaceExecutable(src, dst)` | atomic rename (today's behaviour) | write temp → rename existing to `.old-<time>` (allowed while running) → rename temp in; retry on sharing violations (Defender); `fsx.CleanOld(dir)` at start tolerates `ACCESS_DENIED` |
| `account.link(src, dst) (Mode, error)` / `linkedTo(dst)` | symlink (today's behaviour) | symlink → directory **junction** → file **hard link** → file **copy with recorded hash**; `doctor` reads `Mode` as data |
| `githook.isRunnable` / `githook.hookCommand(path, args)` | execute bit; run `path` | any regular file starting with `#!` or ending `.exe`; run through Git for Windows' `sh.exe` (next to `git.exe`), exactly as Git does |
| `release.binaryName`, `release.archiveExt` | `squadrn`, `.tar.gz` | `squadrn.exe`, `.zip` |
| `cli.terminal()` | — | enable `ENABLE_VIRTUAL_TERMINAL_PROCESSING` on stdout/stderr |

Shared (no pair): `fsx.SameFile(a, b)` replaces `EvalSymlinks` identity checks in `install` and
`doctor`; zip extraction in `release`; the PowerShell analyser in `guard`. All are tested on every
platform.

### Components

1. **Layout.** Unchanged and already portable: `config.DirsFor` uses `filepath.Join` and honours
   XDG. On Windows that gives `%USERPROFILE%\.local\share\squadrn` etc., the same convention as
   Claude Code; `%USERPROFILE%\.local\bin` is usually on PATH already (Claude's installer adds it).
2. **Commands and aliases on Windows: copies, not links.** Hard links cannot follow a
   rename-aside update and a running link cannot be removed (review R3). Each name
   (`squadrn.exe`, `sqd.exe`, …) is a copy updated with `fsx.ReplaceExecutable`. "Ours" is decided
   by the configured names (`install.Names`) plus a stamp check, not by link targets; `doctor`
   fails when a copy differs from the installed binary (stale guard code).
3. **Plugin hooks on Windows: exec form, fail closed.** The Windows archive ships its own
   `plugin/hooks/hooks.json` (built, not rewritten at install). Every hook (PreToolUse,
   Notification, Stop) is exec form through `cmd.exe`, so a missing, quarantined or blocked
   binary blocks the tool call instead of letting it through:
   `cmd.exe /d /c "<plugin root>\..\bin\squadrn.exe" hook guard-git || exit /b 2`
   (spike S1 settles quoting and `${CLAUDE_PLUGIN_ROOT}` expansion). Install paths containing
   `% ^ & !` are refused. Unix keeps its shell-form `hooks.json` and `sh` shim byte for byte; only
   the matcher becomes `Bash|PowerShell`. `doctor` *executes* the hook with a probe payload.
4. **Push guard, layer 1.**
   - `hookInput` gains `tool_name`: missing → Bash parser; `PowerShell` → PowerShell analyser;
     anything else → ask.
   - Bash parser additions (all platforms): unwrap `wsl [-d X] [-u U] [-e|--exec|--]`, `winpty`;
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
     go through the existing push judgement (deny/ask/allow). `-EncodedCommand` is never decoded.
5. **Push guard, layer 2.** Shims unchanged (Git for Windows runs them through its `sh`; paths
   written with `/`). Chaining to the repository's own hook keeps today's design on every
   platform: squadrn resolves the original hook and runs it **with the environment unchanged**, so
   a `git push` started inside a repository hook still reaches squadrn's pre-push. On Windows the
   hook runs through `sh.exe` (seam above), which also fixes a latent bug: Go reports no execute
   bit on Windows, so today's lookup would silently skip every hook.
6. **Shared items between accounts.** Mode order per seam above. Copies record a hash at copy
   time; a later launch refreshes a copy only if it still matches that hash, otherwise reports a
   conflict (never silently overwrites edits made in a secondary account). Deletion paths
   (`account rm`, trash, `gc`, `self-uninstall`) detect reparse points on an unfollowed handle and
   never recurse through a junction into main's directories.
7. **Install (`install.ps1`).** Two bootstraps, one per shell family: `install.sh` (macOS and
   Linux) and `install.ps1` (Windows); no per-OS scripts, no "universal" installer. Each does only
   what must happen before the binary can be trusted — check requirements, download, verify the
   signature and checksum, extract — then hands over to `squadrn self-install`, where every other
   step lives once, in tested Go. Both share one parameter contract (`--name/-Name`,
   `--alias/-Alias`, `--version/-Version`, `--force/-Force`), messages and exit codes, and are
   rendered from one script (`render-install.sh <template> <out>`), ASCII only (Windows PowerShell 5.1 misreads BOM-less UTF-8), checked by
   PSScriptAnalyzer in CI. Steps: require Git for Windows ≥ 2.31 → `gh release download` →
   signature with `ssh-keygen` from Git for Windows (`usr\bin`, absolute path), else built-in
   OpenSSH only if ≥ 8.1 (absolute `System32` path), else refuse → checksum → extract → `squadrn.exe
   self-install`. PATH via `[Environment]::SetEnvironmentVariable('Path', …, 'User')` reading the
   raw registry value. Documented invocation works under the default execution policy
   (script block from `gh` output, or `-ExecutionPolicy Bypass -File`). `install.sh` detects
   MINGW/MSYS and points to `install.ps1`.
8. **Update, rollback, uninstall.** `self-update` replaces the binary, every command copy and the
   snapshot with `fsx.ReplaceExecutable`; errors renaming `previous/` are retried and reported (no
   silent wrong rollback). `self-uninstall` on Windows schedules deletion of the running
   executable with a detached `cmd.exe` cleanup after exit.
9. **Directory ACLs.** When squadrn's data/bin directories are created outside `%USERPROFILE%`
   (XDG, other drives), they get a protected DACL (owner, SYSTEM, Administrators); `doctor`
   verifies it. Inside the profile, the profile's ACL already applies.
10. **Doctor on Windows.** Git for Windows and its version; Git Bash; `sh.exe`; signature tool;
    `claude.exe` (warn if `claude.cmd`/npm shim is found first); hook probe (component 3); link
    modes; stale command copies; execution policy before offering completion; `core.longpaths`;
    PowerShell-only mode makes missing branch protection a **Fail**.
11. **Code signing.** Unsigned binaries trigger SmartScreen and are blocked by Smart App Control
    on fresh Windows 11. The beta ships unsigned; general availability requires an Authenticode
    signature (decision needed, see *Open decisions*).

### Security considerations

- Layer 1 for PowerShell is an allow-list that asks on anything unrecognised; layer 2 covers
  pushes neither analyser sees. Residual risks shared with Unix (server-side protection is the
  only stop): `ssh host git push`, libgit2/JGit tools that skip hooks, `gh api` ref updates,
  scripts written and run by the agent. Windows adds launchers that drop the environment
  (`Start-Process -UseNewEnvironment`, scheduled tasks, WMI, `wsl`); layer 1 asks or denies on
  them when git may run.
- Hooks fail closed (component 3); command copies are checked for staleness; data directories
  outside the profile get a protected DACL.
- Spike S13 checks whether PowerShell tool sessions keep state (aliases, functions, variables)
  between calls; if they do, the analyser asks on any git call whose name could be shadowed.

### Testing and CI

- **Unix safety net first** (lands before the refactor): baseline tag `pre-windows`; golden files
  for every `--help`, `completion bash|zsh|fish`, the guard decision corpus (every `TestDecide`
  row through `squadrn hook guard-git`), the install layout listing after `self-install`, release
  archive names and contents (`goreleaser release --snapshot --skip=publish,sign`), and a byte
  comparison of `hooks.json` allowing only the matcher change.
- `.gitattributes` with `* text=auto eol=lf` before the first Windows checkout.
- CI: a Linux job runs `GOOS=windows go vet` and golangci-lint so Windows files are always
  compiled and linted; `windows-latest` runs `go test ./...` (no `-race`), the testscript suite
  with a Go-built fake `claude.exe` (registered in `testscript.Main`), `USERPROFILE` set, Unix-only
  scripts marked `[!windows]`, and cover gates (including new `proc` and `fsx` gates) per OS;
  Defender exclusion for the workspace. A Debian 11 container (Git 2.30) proves the clear
  "Git too old" refusal.
- Windows unit tests: tree kill of an orphaned grandchild, ctrl-c/ctrl-break survival and exit
  code passthrough (isolated console), replace-while-running including running copies, link-mode
  fallbacks with injected `ERROR_PRIVILEGE_NOT_HELD`, zip extraction hostile entries on every OS,
  `TestDecidePowerShell` tables plus a differential test against the bash parser and a fuzz
  invariant ("never allow unparsed input that may run git"), hook chaining through `sh.exe`,
  Git version parsing (Apple Git, `.windows.N`, `.vfs`, rc, garbage).
- Post-release job on tags: install N-1 then `self-update` to N on Linux; `install.ps1` →
  `version` → rollback on `windows-latest`.
- Manual Windows E2E checklist (Windows 10 and 11, non-admin user, Defender on) gates releases
  that touch Windows code; kept in `docs/maintaining.md`.

## Plan

| Phase | Work | Estimate |
|---|---|---|
| **P0 Spikes** | `scripts/windows-spike.ps1` writes one JSON record per check (versions, command, expected, observed). Runner-capable: S4 signature tools, S6 junction/rename/symlink as standard user, S7 hook chaining through `sh.exe`, S8 ctrl-c/break/close, S9 update while sessions and a hook run, S11 OpenSSH versions, S12 junction semantics on Go 1.27. Needs a logged-in Claude Code on a real machine: S1 exec-form hook through `cmd.exe`, S2 PowerShell tool payload, S3 `projects/` naming, S5 picker keys in Windows Terminal and conhost, S13 PowerShell session state. | 2–3 days |
| **P1 Safety net and refactor** | Golden tests, `.gitattributes`, Git 2.31 check with clear messages, `refactor:` move into `_unix.go` seams (no behaviour change), `fsx.SameFile`. No behaviour change on macOS or Linux. | 3–4 days |
| **P2 Windows core** | Seams for Windows: proc Job Objects, interactive child, detach, `ReplaceExecutable`, link modes, VT; accounts and provisioning; doctor; `windows-latest` CI | 4–5 days |
| **P3 Guard** | PowerShell allow-list analyser and tables; bash parser Windows forms; `tool_name`; Windows `hooks.json` (cmd.exe, fail closed); layer 2 through `sh.exe`; log via handle; junction-safe deletion | 4–5 days |
| **P4 Distribution** | GoReleaser Windows zips (`format_overrides`), attestations for zips, `install.ps1`, update/rollback/uninstall on Windows, ACLs, PowerShell completion, post-release upgrade job | 3–4 days |
| **P5 Beta → GA** | Pre-releases on the beta channel (each with its own CHANGELOG section), real-machine spikes, E2E checklist, code signing, docs (README, maintaining, AGENTS rule), Windows out of beta | 2–3 days |

Total: about 18–24 working days. macOS and Linux behave as before the port (golden tests); Windows
is a beta from v1.0.0-rc.1 until P5 is done.

## Open decisions

1. **Code signing for GA**: Azure Trusted Signing (about 10 USD/month, needs identity
   validation) or an OV certificate (about 200–400 USD/year), or ship unsigned with documented
   SmartScreen/Smart App Control workarounds.
2. **Real-machine spikes**: who runs S1, S2, S3, S5, S13 (a developer with Windows and Claude
   Code logged in).

## Review record

| # | Finding (reviewer) | Resolution |
|---|---|---|
| R1 | `git -c core.hooksPath=… hook run` exports `GIT_CONFIG_PARAMETERS`, which outranks squadrn's `GIT_CONFIG_COUNT`: a push inside a repository hook would skip layer 2, on every platform (architecture, security); `--to-stdin` needs Git 2.40, not 2.36 (platform, verified); the floor would lock Ubuntu 22.04 users out of pushing after an update (release) | Chaining stays exec-based with the environment unchanged; Windows runs hooks through `sh.exe`. Git floor back to the honest 2.31 |
| R2 | Exec-form hook fails open if the binary cannot start (Defender, Smart App Control, missing file) (security) | `cmd.exe … \|\| exit /b 2` wrapper, doctor probe, code signing for GA |
| R3 | Hard links break with rename-aside updates; running links cannot be removed; identity checks via `EvalSymlinks` miss hard links (all) | Copies updated with `ReplaceExecutable`; "ours" by configured names; `fsx.SameFile`; staleness check |
| R4 | `signal.Ignore(os.Interrupt)` lets the console kill squadrn on ctrl-c (platform) | `signal.Notify` and drain; console mode restore |
| R5 | `taskkill /T` misses orphaned grandchildren (architecture, platform, QA) | Job Objects, created suspended |
| R6 | `DETACHED_PROCESS` flashes console windows for `gh` (platform) | `CREATE_NO_WINDOW` and breakaway |
| R7 | PowerShell rule "ask when it mentions push" is a text pre-filter, bypassable by string building, escapes, Unicode, prefixes (architecture, security) | Allow-list analyser; ask whenever git could run |
| R8 | Windows launchers drop `GIT_CONFIG_*`; `wsl git push` unhandled; env names case-insensitive (security) | Parser additions and asks in component 4 |
| R9 | One-way copy sync silently loses edits; junction semantics changed in Go 1.23 (platform, QA) | Hash-guarded refresh; reparse-point-aware deletion; spike S12 |
| R10 | Built-in OpenSSH on older Windows lacks `-Y verify` (platform, security, release) | Git for Windows' `ssh-keygen` first, absolute paths, fail closed |
| R11 | Asset name and binary name hard-coded (`.tar.gz`, `squadrn`) (architecture, release) | `binaryName`/`archiveExt` seams; golden test for Unix names |
| R12 | CRLF checkout breaks scripts on Windows; lint/coverage only on Linux (QA) | `.gitattributes`; Windows vet/lint on Linux; per-OS gates |
| R13 | rc tags need their own CHANGELOG section; install of an rc should set the beta channel; zips not attested (release) | Runbook update; `install.ps1` sets channel; attest `dist/*.zip` |
| R14 | Today's hook lookup never finds a hook on Windows (no execute bits) (platform) | `isRunnable` seam |

## Consequences

- One code base, two platform families; every feature states its Windows behaviour.
- `grid` remains Unix-only.
- AGENTS.md changes from "no Windows code paths" to "platform code only in `_unix.go` /
  `_windows.go` pairs"; "deadline kills the process group" becomes "process tree".
