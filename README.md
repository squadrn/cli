# Squadrn

*A squadron of coding agents. Any provider, any account, in parallel.*

Run many coding agents in parallel on one machine — across several accounts and isolated git
worktrees — with a guard that stops agents from force-pushing or pushing to protected branches.

Providers: Claude Code today; the account model (`provider/name`) is ready for Codex and Gemini.
Platforms: macOS and Linux (amd64, arm64); Windows 10 21H2+/11 (amd64, arm64) in beta, without
`grid` (ADR 0008).

## Install

Squadrn is private software (see [LICENSE](LICENSE)). Releases live in a separate
repository that holds binaries only; access is by invitation. With [gh](https://cli.github.com)
logged in to the account that was invited:

```sh
gh release download -R OWNER/sprawl-releases -p install.sh -O - | sh
```

This installs the latest stable release. For another one, a pre-release included, name its tag
(`gh release list -R OWNER/sprawl-releases` lists them); from 1.2.0-rc.3 on the installer installs
the release it comes from (with older ones, also pass `--version`):

```sh
gh release download vX.Y.Z -R OWNER/sprawl-releases -p install.sh -O - | sh -s -- --version vX.Y.Z
```

The default command is **`squadrn`** with the short alias **`sqd`**. At a terminal the installer
asks for other names (Enter keeps the default); or pass them up front (`--alias ''` installs no
alias):

```sh
gh release download -R OWNER/sprawl-releases -p install.sh -O - | sh -s -- --name ai --alias ''
```

The installer checks the release signature (`ssh-keygen`) and checksum, installs the binary to
`~/.local/share/squadrn/bin/squadrn`, links your command name(s) from `~/.local/bin`, and installs
the squadrn plugin into every Claude account.

### Windows (beta)

Requires [Git for Windows](https://git-scm.com/download/win) 2.31+ and `gh`. Windows builds ship
in pre-releases until 1.2.0, so name the release (the newest one below; any tag from
`gh release list -R OWNER/sprawl-releases` that has a `.zip`). In PowerShell:

```powershell
$tag = gh release list -R OWNER/sprawl-releases --exclude-drafts --limit 1 --json tagName --jq '.[0].tagName'
& ([scriptblock]::Create((gh release download $tag -R OWNER/sprawl-releases -p install.ps1 -O - | Out-String))) -Version $tag
# with options:
& ([scriptblock]::Create((gh release download $tag -R OWNER/sprawl-releases -p install.ps1 -O - | Out-String))) -Version $tag -Name ai -Alias ''
```

From 1.2.0-rc.3 on, the installer installs the release it was downloaded from and `-Version` is
only needed for another one. A failed install prints the reason in red and leaves the window
open. `-AddToProfile` loads tab completion from your PowerShell profile; `self-uninstall` removes
that line again.

Everything above works the same, with these differences:
- `grid`, `ps` and `swap` inside a pane need tmux and are not available; run each agent with
  `squadrn wt <name>` in its own terminal tab. `swap` outside a grid works.
- Command names are copies of `squadrn.exe` in `%USERPROFILE%\.local\bin`, refreshed on update.
- Shared settings use symbolic links with Developer Mode enabled; without it, directories become
  junctions and files hash-checked copies (an edit in a secondary account is reported, never
  overwritten).
- The push guard covers both the Bash (Git Bash) and PowerShell tools of Claude Code.
- Until releases are code-signed, SmartScreen may warn on first run.

### Updates

Like Claude Code, squadrn tells you about a new release and never installs it by itself. Once a day
it checks in the background (never delaying a command) and, at a terminal, shows one line:

```
✦ squadrn v1.1.0 is available (you have v1.0.0) · squadrn self-update · what's new: squadrn self-update --check
```

```sh
squadrn self-update --check       # what is new (release notes)
squadrn self-update               # install it: signature, checksum, then the plugin in every account
squadrn self-update --rollback    # back to the release installed before (run again to go forward)
squadrn config set update_channel beta    # also pre-releases; stable is the default
squadrn config set update_check false     # no checks, no notices (also: SQUADRN_NO_UPDATE_CHECK=1)
```

Updates keep your command names, refuse to downgrade unless `--allow-downgrade`, and refuse any
release not signed by a key built into the copy you have. No notice is shown in CI, with `--json`
or outside a terminal. `squadrn doctor` mentions a pending update too.

To remove squadrn: `squadrn self-uninstall` (plugin from every account, command links, completions,
installed files; accounts are kept). `--purge` also removes the config, guard log and caches.

### Names and completions

```sh
<command> config set command ai          # relinks ~/.local/bin and the shell completions
<command> config set aliases sqd,sx
```

Completions for zsh (`~/.zsh/completions`), bash (`~/.local/share/bash-completion/completions`)
and fish (`~/.config/fish/completions`) are written when those directories exist; otherwise
`squadrn completion zsh|bash|fish` prints them.

Requirements: `claude` (native build), `git` 2.31 or newer; `tmux` for `grid`/`ps`/`swap`; `gh` for installing
and updating. Examples below use `squadrn`; `sqd` works the same.

## Accounts

An account is a provider's config dir, written `provider/name`: `claude/main` is `~/.claude`,
`claude/personal` is `~/.claude-personal`. A bare name works when it is unique across providers;
`main` always means the default provider's main account. Settings, `CLAUDE.md`, agents, hooks and
skills are shared from the provider's main account through symlinks; logins, history and plugins
stay per account.

```sh
squadrn                              # pick an account interactively (offers login when needed)
squadrn personal                     # run as claude/personal (also: claude/personal, -a personal)
squadrn personal -p "summarize"      # agent flags go to the agent CLI unchanged
squadrn personal -- "fix the bug"    # a prompt goes after --
squadrn personal mcp list            # agent subcommands pass through
squadrn account add work             # create or repair claude/work
squadrn account login work
squadrn account ls [--json]
squadrn account logout work
squadrn account adopt squad          # make an existing ~/.claude-squad an account (asks; --yes)
squadrn account rename work job      # logs work out first; log in again as job
squadrn account rm work              # logs out and moves the dir to the trash
```

A word that is neither a squadrn command, an account nor an agent subcommand is a usage error with
a suggestion (`squadrn acount` → *did you mean account?*), so a typo never becomes a prompt.
Global options before the command: `-a <account>`, `-C <dir>`, `--no-color` (also `NO_COLOR=1`).

A dir named like an account but not made by squadrn (no `.squadrn-account` marker) is not an
account: `account ls` lists it as unmanaged and `account adopt` makes it one.

Account names: `^[a-z][a-z0-9-]{1,23}$`, e.g. `personal`, `comacpro-2`. Names that are squadrn or
provider subcommands (`mcp`, `wt`, …) are rejected. Account dirs are created private (`0700`).

## Usage limits

Claude Code reports a subscription's 5-hour and 7-day usage to its status line, and runs a
`StopFailure` hook when a turn stops on a usage limit. squadrn records both per account, so you
can see which account has room left before you swap.

```sh
squadrn usage                        # every account: plan, org, 5h/7d used and reset times, state
squadrn usage work                   # details, including the last limit hit and handoff
squadrn usage --json                 # stable JSON (below)
squadrn usage --refresh              # also re-query login, plan and organization
squadrn usage --watch                # redraw every 5 seconds
squadrn usage --setup [work|--all]   # record usage through the status line (asks; -y skips)
squadrn usage --setup --undo         # restore the previous status line
```

`--setup` wraps the status line command in `settings.json`: squadrn records what it receives, then
runs your own command with the same input and shows its output unchanged (without one, it shows
`work · 5h 42% · 7d 18%`). Accounts that share main's `settings.json` are all covered by one
change. `account add` offers it once (`--usage`/`--no-usage` decide without asking);
`self-uninstall` restores the previous command; `doctor` shows whether each account is tracked.
When a session hits its limit, the plugin's hook records it and notifies
*"claude/work hit its usage limit (resets 15:45) — squadrn swap"*.

Usage is only known for claude.ai subscriptions (Pro, Max, …), after a session's first reply, and
only as fresh as the last status line update. States: `ok`, `high` (5h ≥ 80% or 7d ≥ 90%),
`limited`, `reset` (a limit or high window has reset since), `stale` (older than 6 hours),
`nodata`, `metered` (API billing), `loggedout`, `na` (provider does not report usage). The footer
names the best account for the current repository: the same provider, not limited, then lowest 5h
and 7d use. Records live in `~/.local/state/squadrn/usage/` (private).

## Swap

`swap` moves a conversation to another account and resumes it there, in the directory it was
started in.

```sh
squadrn swap                          # the conversation that just hit its limit here; pick the target
squadrn swap --to personal            # same, to claude/personal
squadrn swap --session <id> --to work # any conversation, by session ID
squadrn swap feat-a personal          # latest conversation of worktree feat-a (earlier form)
```

Without `--session`, the conversation is the one that last hit its limit in the current directory
or below it, or a session running there (several: a picker; without a terminal, an error listing
their `--session` values). Without `--to`, the target is picked from the accounts ranked like
`squadrn usage` (without a terminal, `--to` is required). A logged-out target is refused, moving to
another organization asks, and an Opus conversation moving to a Pro account warns.

A session still running the conversation is stopped first, so it never has two writers: inside a
grid its pane is restarted in place; elsewhere squadrn asks, interrupts it (ctrl-c) and, if it is
still running after 10 seconds, asks again and terminates it (`-y` answers yes). On Windows a
session in another terminal cannot be interrupted, so it is terminated after you agree.

The transcript, its subagent and tool-result dir and its file checkpoints are copied (the copy is
checked against the source). When the target already has the conversation and it went on there,
the swap is refused unless `--force` overwrites that copy. Every handoff is appended to
`~/.local/state/squadrn/handoffs.jsonl`.

`--json` prints `{"accounts": [...], "from": "...", "best": "...", "next_reset": "..."}` (`from`:
the account last limited or active in this directory; `next_reset`: when every account is limited,
the first reset). Each account (also printed alone by `usage <account> --json`):

```json
{
  "account": "claude/work", "plan": "max", "org": "Acme", "email": "me@example.com",
  "logged_in": true, "state": "limited", "tracking": true,
  "five_hour": {"used_pct": 100, "resets_at": "2026-11-18T15:45:00+01:00"},
  "seven_day": {"used_pct": 41, "resets_at": "2026-11-23T00:00:00+01:00"},
  "spend": {"used_pct": 63, "resets_at": "…", "used_usd": 314.12, "limit_usd": 500, "period": "monthly"},
  "observed_at": "2026-11-18T13:02:11+01:00",
  "last_limit": {"at": "…", "session": "…", "cwd": "/repo", "resets_at": "…"},
  "error": "login status unavailable (only when it is)"
}
```

Missing values are left out; `logged_in` is `null` when the login status is unavailable. No data is
not an error: the command exits 0.

## Parallel agents

```sh
squadrn wt feat-login                                    # agent in .claude/worktrees/feat-login
squadrn wt ls [--json]                                   # worktrees, modified or not, running as whom
squadrn wt rm feat-login [--force]                       # remove one (branch only if merged)
squadrn wt rm feat-login --force-branch                  # also delete an unmerged branch (asks; -y)
squadrn grid ~/repo feat-a personal:fix-b                # tmux grid, one agent per pane
squadrn grid ~/repo feat-a claude/personal:fix-b         # account as provider/name
squadrn grid ~/repo feat-1 feat-2 feat-3 --spread        # spread over logged-in accounts
squadrn grid ~/repo personal:chore-c                     # add a pane to the running grid
squadrn grid ls | attach [repo] | stop [repo]            # manage running grids
squadrn ps [--json]                                      # which agent runs as which account
squadrn swap [--to personal]                             # account hit its limit: move the conversation
squadrn deps feat-a                                      # install dependencies (exit status passes through)
squadrn gc [--json]                                      # merged/empty worktrees; --yes removes them
squadrn gc --yes --force                                 # also those git refuses (submodules)
```

`wt rm` refuses a worktree a running session uses (`--force` removes it anyway) and keeps an
unmerged branch unless `--force-branch` is given.

Worktree names are `<type>-<slug>` (`feat-otp-login`, `fix-cart-total`); Claude Code creates branch
`worktree-<name>`. `swap` moves conversations between accounts of the same provider (see [Swap](#swap)).

## Git push guard

Three layers keep agents from force-pushing or pushing to protected branches:

1. **Pre-tool hook.** The squadrn plugin runs `squadrn hook guard-git` before every Bash command an
   agent runs. It parses the command as shell — pipelines, `$(…)`, `bash -c`, `eval`, here-documents
   and here-strings, `sudo`/`env`/`timeout`/`script`/`watch`/`find -exec` wrappers, `cd`, `git -C`,
   quoting tricks (`pu''sh`), case (`GIT PUSH`), git's option prefixes (`--forc`), `-c alias.*` and
   repository aliases — and resolves a bare `git push` the way git does (`push.default`,
   `remote.*.push`, upstream). It **denies** force pushes, `--all`/`--mirror`/`--prune`,
   `--no-verify`, pushes or deletions targeting a protected branch, `gh api` writes to branch refs
   or protection, `gh pr merge --admin`, and agents changing the guard's own policy
   (`squadrn config set protected_branches|git_hooks`, writes to its config file or git hooks);
   **asks** when the target is only known at runtime (`$BRANCH`, `xargs`, `python -c …`, a shell
   reading stdin), for git aliases or subcommands it cannot see, settings or `GIT_*` variables that
   make git run a program or use another repository, and other GitHub writes; **allows** the
   rest. Any failure or timeout asks, never allows.
2. **git pre-push hook.** Sessions squadrn starts (`squadrn`, `wt`, `grid`, `swap`) get
   `core.hooksPath` pointed at `~/.local/share/squadrn/git-hooks` through `GIT_CONFIG_*`. Git's
   pre-push hook sees the exact refs, so pushes layer 1 cannot see (scripts, interpreters) are
   checked too, including non-fast-forward updates. The repository's own hooks (husky, lefthook,
   pre-commit) still run after the check. The hook reads the protected list from the config file
   fixed at install, not from the pushing process's environment. Turn it off with
   `squadrn config set git_hooks false` (in your own terminal: agents are not allowed to).
3. **The server.** Only branch protection on the remote stops pushes made outside squadrn.
   `squadrn doctor` checks it for the current GitHub repository (classic protection or a ruleset
   that blocks force pushes and deletion).

Protected branches default to `main master develop release/*`; set your own list with
`squadrn config set protected_branches …`. A repository can add branches — never remove them:

```sh
git config squadrn.protectedBranches "production staging"
```

Decisions other than allow are logged to `~/.local/state/squadrn/guard.log` (`0600`, credentials
masked) with the layer that made them. `squadrn doctor` also suggests `permissions.deny` rules so
agents cannot edit squadrn's own files with their file tools.

## Configuration

`~/.config/squadrn/config.toml` (unknown keys and invalid values are errors):

```toml
schema = 1
command = "ai"                               # set at install; change with: config set command
aliases = ["sqd"]
default_provider = "claude"
shared_mcp = ["codegraph"]                   # MCP servers copied from main into new accounts
protected_branches = ["main", "release/*"]   # repositories can only add to this list
git_hooks = true                             # layer 2 of the push guard
update_check = true                          # daily background check, notice only
update_channel = "stable"                    # or "beta" for pre-releases
```

`squadrn config [--json]` shows every setting; `squadrn config get|set <key> [value]` reads or
changes one.

MCP servers whose config has a non-empty `env` or `headers` are never copied; add those per account.

| Path | Contents |
|---|---|
| `~/.local/share/squadrn/` | binary, plugin, local plugin marketplace, `git-hooks/`, `previous/` (rollback) |
| `~/.local/state/squadrn/` | guard log, migration backups |
| `~/.cache/squadrn/` | login status cache, update check |

## Exit codes

`0` ok · `1` error, or "no" at a confirmation · `2` usage or invalid input · `130` cancelled
(ctrl-c, esc) · `deps` passes the package manager's status through. Commands with `--json` print
machine-readable output on stdout.

## Migrating from sprawl or squad

Squadrn was called Sprawling Agents (`sprawl`) before 2.0, and squad before that (ADR 0010).
Installing Squadrn over either adopts its settings; command names you chose (say `ai`) are kept,
while the old defaults (`sprawl` + `sra`, `squad` + `sq`) become `squadrn` + `sqd`. In every
account it replaces the `sprawl@sprawl` (or `squad@squad`) plugin with `squadrn@squadrn`, removes
command links that pointed to the old install, rewrites `.sprawl-account` markers, moves the old
guard log to `~/.local/state/squadrn/legacy-logs/` and backs the old files up under
`~/.local/state/squadrn/legacy-<name>-*`. Still honoured through 2.x: `git config
sprawl.protectedBranches` (and `squad.protectedBranches`), grids started by the old release, and
`SPRAWL_*` environment variables (with a warning to rename them to `SQUADRN_*`). Releases signed
before the rename do not update to Squadrn: install it once with the installer.

## Releasing (maintainer)

Source and releases live in two repositories (ADR 0007): this one (private, source) and
`hoangthaininh/sprawl-releases` (private, binaries only; users are invited there with read
access). Release steps, the secrets they depend on, renewing the token and handling the signing
key: [docs/maintaining.md](docs/maintaining.md). A monthly `maintenance` workflow checks those
secrets and fails (GitHub emails you) before anything expires.

## Development

```sh
make build      # bin/squadrn
make check      # lint, race tests, coverage gates, plugin validation
make vuln       # govulncheck
```

Architecture: [docs/architecture.md](docs/architecture.md) · decisions: [docs/adr](docs/adr) ·
[CHANGELOG](CHANGELOG.md). Actions are pinned by commit.
