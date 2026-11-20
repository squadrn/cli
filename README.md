# Squadrn

Squadrn — command `srn` (also `squadrn`).

*A squadron of coding agents. Any provider, any account, in parallel.*

Run many coding agents in parallel on one machine — across several accounts and isolated git
worktrees — with a guard that stops agents from force-pushing or pushing to protected branches.

Providers: Claude Code and OpenAI Codex CLI (`claude/…`, `codex/…` accounts); the account model
is ready for more.
Platforms: macOS and Linux (amd64, arm64); Windows 10 21H2+/11 (amd64, arm64) in beta, without
`grid` (ADR 0008).

## Install

macOS and Linux (needs `curl` or `wget`, `git` 2.31+ and `ssh-keygen`; no GitHub account):

```sh
curl -fsSL https://github.com/squadrn/squadrn/releases/latest/download/install.sh | sh
```

`latest` points to the newest stable release; GitHub skips pre-releases there. Until a stable
release exists, or for any other release, take that release's installer (it installs the release
it was downloaded from; see [releases](https://github.com/squadrn/squadrn/releases)):

```sh
curl -fsSL https://github.com/squadrn/squadrn/releases/download/v1.0.0-rc.1/install.sh | sh
```

The default command is **`srn`** with the alias **`squadrn`**. If another program already
uses a name (the npm package `srn` installs one), the installer says so; at a terminal it asks for
other names (Enter keeps the default); or pass them up front (`--alias ''` installs no
alias; `SQUADRN_COMMAND`, `SQUADRN_ALIASES` and `SQUADRN_VERSION` work too):

```sh
curl -fsSL https://github.com/squadrn/squadrn/releases/latest/download/install.sh | sh -s -- --command ai --alias ''
```

The installer checks the release signature (`ssh-keygen`) and checksum, installs the binary to
`~/.local/share/squadrn/bin/squadrn`, links your command name(s) from `~/.local/bin`, and installs
the squadrn plugin into every Claude account. On the first install at a terminal it then runs
`srn setup`: it finds the agent CLIs on PATH, asks for the default settings profile, offers to adopt
existing `~/.claude-<name>` dirs and to track usage limits, and runs `srn doctor` (every step can be
skipped; run it again any time). Without a terminal it prints what it found and the next steps.
Releases also carry GitHub build provenance; verify
an archive by hand with `gh attestation verify <archive> --repo squadrn/squadrn`.

### Windows (beta)

Requires [Git for Windows](https://git-scm.com/download/win) 2.31+. In PowerShell:

```powershell
irm https://github.com/squadrn/squadrn/releases/latest/download/install.ps1 | iex
```

`latest` is the newest stable release; for a pre-release, or any other release, use its own installer:

```powershell
irm https://github.com/squadrn/squadrn/releases/download/v1.0.0-rc.1/install.ps1 | iex
```

`| iex` cannot pass options: set `$env:SQUADRN_COMMAND`, `$env:SQUADRN_ALIASES` or
`$env:SQUADRN_VERSION` first, or use the script block form:

```powershell
& ([scriptblock]::Create((irm https://github.com/squadrn/squadrn/releases/latest/download/install.ps1))) -Command ai -Alias '' -AddToProfile
```

A failed install prints the reason in red and leaves the window open. `-AddToProfile` loads tab
completion from your PowerShell profile; `self-uninstall` removes that line again.

Everything above works the same, with these differences:
- `grid`, `ps` and `swap` inside a pane need tmux and are not available; run each agent with
  `srn wt <name>` in its own terminal tab. `swap` outside a grid works.
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
✦ squadrn v1.1.0 is available (you have v1.0.0) · srn self-update · what's new: srn self-update --check
```

```sh
srn self-update --check       # what is new (release notes)
srn self-update               # install it: signature, checksum, then the plugin in every account
srn self-update --rollback    # back to the release installed before (run again to go forward)
srn self-update --tag v1.2.0  # a specific release
srn config set update_channel beta    # also pre-releases; stable is the default
srn config set update_check false     # no checks, no notices (also: SQUADRN_NO_UPDATE_CHECK=1)
```

Updates keep your command names, refuse to downgrade unless `--allow-downgrade`, and refuse any
release not signed by a key built into the copy you have. No notice is shown in CI, with `--json`
or outside a terminal. `srn doctor` mentions a pending update too.

To remove squadrn: `srn self-uninstall` (plugin from every account, command links, completions,
installed files; accounts are kept). `--purge` also removes the config, guard log and caches.

### Names and completions

```sh
<command> config set command ai          # relinks ~/.local/bin and the shell completions
<command> config set aliases srn,sx
```

Completions for zsh (`~/.zsh/completions`), bash (`~/.local/share/bash-completion/completions`)
and fish (`~/.config/fish/completions`) are written when those directories exist; otherwise
`srn completion zsh|bash|fish` prints them.

Requirements: `claude` (native build), `git` 2.31 or newer; `tmux` for `grid`/`ps`.
Optional: `gh` (PR detection in `gc`, the branch protection check in `doctor`, build provenance).
Updates read the public GitHub API; set `GH_TOKEN` or `GITHUB_TOKEN` (or log in with `gh`) only if
you hit its rate limit. Examples below use `srn`; `squadrn` works the same.

## Use within Anthropic's terms

Squadrn runs Claude Code under accounts you own — for example your work organization's account
and your personal one — and keeps them apart. It is not a way to share an account with other
people or to get around usage limits: each account stays bound by its own plan, limits and the
[terms](https://www.anthropic.com/legal/consumer-terms) of the organization it belongs to.
`usage` and `swap` help you continue a conversation on the right one of your accounts.

## Accounts

An account is a provider's config dir, written `provider/name`: `claude/main` is `~/.claude`,
`claude/personal` is `~/.claude-personal`. A bare name works when it is unique across providers;
`main` always means the default provider's main account. `CLAUDE.md`, agents, commands, rules,
output styles, hooks and skills are shared from the provider's main account through symlinks;
logins, history and plugins stay per account. Each account has its own `settings.json`, built from
main's plus a profile and kept in step with main before every session; a key you change in the
account (`/model`) stays yours and is reported once ([ADR 0011](docs/adr/0011-provider-model-v2.md)).

New accounts get a settings profile: `minimal` (secrets denied), `standard` (the default: secrets
and other accounts' credentials denied, asks before `git push`, `git reset --hard` and `rm -rf`,
30-day history) or `strict` (also sandboxed, reads limited to the project, 14-day history).
`--with privacy` turns telemetry off, `--with no-attribution` drops AI attribution; neither is on
by default, and the model, effort and permission mode are left to the CLI.

```sh
srn                              # pick an account interactively (offers login when needed)
srn personal                     # run as claude/personal (also: claude/personal, -a personal)
srn personal -p "summarize"      # agent flags go to the agent CLI unchanged
srn personal -- "fix the bug"    # a prompt goes after --
srn personal mcp list            # agent subcommands pass through
srn setup                        # guided first run (CLIs, profile, adopt, usage, doctor)
srn account add work             # create or repair claude/work (asks profile, usage, login)
srn account add work --profile strict --with privacy --usage --login -y
srn account login work
srn account ls [--json]
srn account logout work
srn account adopt other          # make an existing ~/.claude-other an account and set it up
srn account rename work job      # logs work out first; log in again as job
srn account rm work              # logs out and moves the dir to the trash (macOS ~/.Trash)
```

`srn account rm work --purge` deletes the dir permanently instead of trashing it and removes every
record squadrn keeps of the account: its usage record, its entries in the handoff ledger, its
in-use records (in its dir and in the repositories its records name) and its cached login status.
It prints exactly what it removed. Links inside the dir are removed, never followed; a dir that is
itself a link is refused. At a terminal you confirm by typing the account (`claude/work`), even
with `-y`; scripts without a terminal pass `--purge --yes`. If something cannot be deleted (a file
another program holds open on Windows), the account and its records stay and the same command
finishes the job.

A word that is neither a squadrn command, an account nor an agent subcommand is a usage error with
a suggestion (`srn acount` → *did you mean account?*), so a typo never becomes a prompt.
Global options before the command: `-a <account>`, `-C <dir>`, `--no-color` (also `NO_COLOR=1`).

A dir named like an account but not made by squadrn (no `.squadrn-account` marker) is not an
account: `account ls` lists it as unmanaged and `account adopt` makes it one.

Account names: `^[a-z][a-z0-9-]{1,23}$`, e.g. `personal`, `acme-2`. squadrn's own commands
(`wt`, `grid`, …) are rejected; an agent subcommand name (`mcp`) is fine, and such an account is
matched before the subcommand. Account dirs are created private (`0700`).

### Several providers

Every provider has its own accounts, dirs and CLI; `provider/name` picks one. Codex CLI
accounts are `CODEX_HOME` dirs: `codex/main` is `~/.codex` (Codex's default, nothing set) and
`codex/work` is `~/.codex-work`. Install Codex first (`npm install -g @openai/codex`); `doctor`
only requires a provider's CLI once you have accounts for it.

```sh
srn account add codex/work       # ~/.codex-work, push guard hook, AGENTS.md/skills/rules from codex/main
srn account login codex/work     # codex login with CODEX_HOME=~/.codex-work
srn codex/work                   # start Codex as that account (srn work when the name is unique)
srn work exec "fix the tests"    # codex subcommands pass through: exec, resume, mcp, review, …
srn work resume <session-id>     # resume a conversation of that account
srn doctor                       # login, guard hook trust and credential store per account
```

Each Codex account keeps its login in its own `auth.json`: squadrn sets
`cli_auth_credentials_store = "file"` in the account's `config.toml` (the OS keyring would be
shared) and restores it whenever it runs Codex for the account. MCP servers listed in
`shared_mcp` are copied from `codex/main` with `codex mcp add`. Claude-only for now: `swap`
(Codex's session store is not documented), `wt` and `grid` worktrees (`codex --worktree` takes
no name), usage limits in the status line (`srn usage` asks Codex for them instead, at most every
5 minutes unless `--refresh`).

Codex profiles map to `config.toml`: `minimal` keeps credentials per account and secret
environment variables out of commands; `standard` (default) adds a workspace-write sandbox without
network that asks before leaving it; `strict` makes the workspace **read-only** — use it for
review-only accounts, and `standard` for daily work.

## Usage limits

Claude Code reports a subscription's 5-hour and 7-day usage to its status line, and runs a
`StopFailure` hook when a turn stops on a usage limit. squadrn records both per account, so you
can see where each of your accounts stands and continue on the right one.

```sh
srn usage                        # every account: plan, org, 5h/7d used and reset times, state
srn usage work                   # details, including the last limit hit and handoff
srn usage --json                 # stable JSON (below)
srn usage --refresh              # also re-query login, plan and organization
srn usage --watch                # redraw every 5 seconds
srn usage setup [work|--all]     # record usage through the status line (asks; -y skips)
srn usage setup --undo           # restore the previous status line
```

`usage setup` wraps the status line command in `settings.json`: squadrn records what it receives, then
runs your own command with the same input and shows its output unchanged (without one, it shows
`work · 5h 42% · 7d 18%`). Each account's own `settings.json` is set up on its own. `account add` offers it once (`--usage`/`--no-usage` decide without asking);
`self-uninstall` restores the previous command; `doctor` shows whether each account is tracked.
When a session hits its limit, the plugin's hook records it and notifies
*"Usage limit · resets 15:45 · srn swap"* (see [Notifications](#notifications)).

Usage is only known for claude.ai subscriptions (Pro, Max, …), after a session's first reply, and
only as fresh as the last status line update. States: `ok`, `high` (5h ≥ 80% or 7d ≥ 90%),
`limited`, `reset` (a limit or high window has reset since), `stale` (older than 6 hours),
`nodata`, `metered` (API billing), `loggedout`, `na` (provider does not report usage). The footer
names the best account for the current repository, the current account included: the same
provider, not limited, known headroom (`ok`, `reset`) before `nodata`/`stale`, then lowest 5h and
7d use. Reset times show the weekday when they are not today (`Thu 15:45`). Logged-out accounts are
not listed as untracked. Records live in `~/.local/state/squadrn/usage/` (private).

## Swap

`swap` moves a conversation to another of your accounts and resumes it there, in the directory it
was started in.

```sh
srn swap                                  # the conversation that just hit its limit here; pick the target
srn swap --to personal                    # same, to claude/personal
srn swap --session <id> --to work         # any conversation, by session ID
srn swap --worktree feat-a --to personal  # latest conversation of worktree feat-a
```

Without `--session`, the conversation is the one that last hit its limit in the current directory
or below it, or a session running there (several: a picker; without a terminal, an error listing
their `--session` values). Without `--to`, the target is picked from the accounts ranked like
`srn usage` (without a terminal, `--to` is required). A logged-out target is refused, moving to
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
srn wt feat-login                                    # agent in .claude/worktrees/feat-login
srn wt ls [--json]                                   # worktrees, modified or not, running as whom
srn wt rm feat-login [--force]                       # remove one (branch only if merged)
srn wt rm feat-login --force-branch                  # also delete an unmerged branch (asks; -y)
srn grid ~/repo feat-a personal:fix-b                # tmux grid, one agent per pane
srn grid ~/repo feat-a claude/personal:fix-b         # account as provider/name
srn grid ~/repo feat-1 feat-2 feat-3 --spread        # spread over logged-in accounts
srn grid ~/repo personal:chore-c                     # add a pane to the running grid
srn grid ls | attach [repo] | stop [repo]            # manage running grids
srn ps [--json]                                      # every running agent: account, worktree, state
srn next                                             # jump to the tmux pane that waits for you
srn deps feat-a                                      # install dependencies (exit status passes through)
srn gc [--json]                                      # merged/empty worktrees; --yes removes them
srn gc --yes --force                                 # also those git refuses (submodules)
```

`wt rm` refuses a worktree a running session uses (`--force` removes it anyway) and keeps an
unmerged branch unless `--force-branch` is given.

Worktree names are `<type>-<slug>` (`feat-otp-login`, `fix-cart-total`); Claude Code creates branch
`worktree-<name>`. `swap` moves conversations between accounts of the same provider (see [Swap](#swap)).

## Notifications

Squadrn tells you when an agent needs you, failed or finished, and stays quiet while you are
looking at it:

| Event | When | Example |
|---|---|---|
| `input` | an approval, a question, a reply it waits for | *Needs approval: Bash* · *Waiting for your reply* |
| `failed` | the turn stopped on an error | *Usage limit · resets 15:45 · srn swap* · *Login expired · srn account login claude/work* |
| `done` | the turn finished (no background work left) | *Done* |
| `info` | logged in, auto-resume, plugin messages (off by default) | |

The title names the agent, the account and where it works: *Claude Code · work · api/fix-auth*.
The agent's own text (your prompt, its reply) is never shown unless `notify.preview = true`.
`input` and `failed` play a sound; nothing is shown while the session's tmux pane is in view, and
`done` is left out while its terminal is the frontmost app (macOS). One session gets at most one
notification every 30 seconds (except `input`), a newer one replaces the older, and an idle prompt
right after *Done* is not repeated.

```sh
srn notify status                 # settings, the notifier in use and why, the last notifications
srn notify test [input|failed|done]
srn config set notify.quiet_hours 22:00-08:00
srn config set notify.enabled false
```

| OS | Notifier |
|---|---|
| macOS | [`terminal-notifier`](https://github.com/julienXX/terminal-notifier) when installed (`brew install terminal-notifier`: grouped per session, click brings the terminal forward), else `osascript` (shown as Script Editor, which needs notification permission in System Settings) |
| Linux | `notify-send` (libnotify), else `gdbus`; `input` is urgent and stays until dismissed |
| Windows | a toast through Windows PowerShell 5.1, under the app id `Squadrn` registered by `self-install` |

Terminals that show notifications themselves get them from the agent instead of the desktop:
iTerm2 and WezTerm (OSC 9), Ghostty and Warp (OSC 777), kitty (OSC 99), for `input` and `failed`
outside tmux. In tmux, each agent's pane records its state (`working`, `input`, `failed`, `done`;
a grid's pane border shows it), the bell rings for `input` and `failed`, `srn ps` lists the state
and `srn next` jumps to the first pane that needs input, else the first that failed. With
`terminal-notifier`, a click on a notification switches tmux to the session's pane and brings its
terminal forward. `notify.method` picks one channel: `terminal`, `desktop`, `tmux` (default `auto`).

Do Not Disturb and Focus modes apply as for any app. While squadrn's notifications are on, account
profiles turn Claude Code's own off (`preferredNotifChannel: "notifications_disabled"`), so you do
not get both; your own value in an account's `settings.json` wins. Deliveries are logged (titles,
never agent text) in `~/.local/state/squadrn/notify.log`.

## Git push guard

The guard is a safety net against agents' mistakes, not a sandbox: it does not contain a hostile
process. Three layers keep agents from force-pushing or pushing to protected branches:

1. **Pre-tool hook.** The squadrn plugin (Claude Code), or a `PreToolUse` hook in
   `$CODEX_HOME/hooks.json` that squadrn installs and marks trusted (Codex), runs
   `squadrn hook guard-git` before every shell command an agent runs. It parses the command as shell — pipelines, `$(…)`, `bash -c`, `eval`, here-documents
   and here-strings, `sudo`/`env`/`timeout`/`script`/`watch`/`find -exec` wrappers, `cd`, `git -C`,
   quoting tricks (`pu''sh`), case (`GIT PUSH`), git's option prefixes (`--forc`), `-c alias.*` and
   repository aliases — and resolves a bare `git push` the way git does (`push.default`,
   `remote.*.push`, upstream). It **denies** force pushes, `--all`/`--mirror`/`--prune`,
   `--no-verify`, pushes or deletions targeting a protected branch, `gh api` writes to branch refs
   or protection, `gh pr merge --admin`, and agents changing the guard's own policy
   (`srn config set protected_branches|git_hooks`, writes to its config file or git hooks);
   **asks** when the target is only known at runtime (`$BRANCH`, `xargs`, `python -c …`, a shell
   reading stdin), for git aliases or subcommands it cannot see, settings or `GIT_*` variables that
   make git run a program or use another repository, and other GitHub writes; **allows** the
   rest. Any failure or timeout asks, never allows. Codex hooks cannot ask (Codex lets the
   command run), so for Codex accounts the guard denies instead and tells you to run the command
   yourself. On Windows Codex starts `squadrn.exe` through `cmd.exe` and lets the command run
   when the hook cannot start or fails, so there layer 1 fails open for Codex (`doctor` says so);
   layers 2 and 3 still hold.
2. **git pre-push hook.** Sessions squadrn starts (`srn`, `wt`, `grid`, `swap`) get
   `core.hooksPath` pointed at `~/.local/share/squadrn/git-hooks` through `GIT_CONFIG_*`. Git's
   pre-push hook sees the exact refs, so pushes layer 1 cannot see (scripts, interpreters) are
   checked too, including non-fast-forward updates. The repository's own hooks (husky, lefthook,
   pre-commit) still run after the check. The hook reads the protected list from the config file
   fixed at install, not from the pushing process's environment. Turn it off with
   `srn config set git_hooks false` (in your own terminal: agents are not allowed to).
3. **The server.** Only branch protection on the remote stops pushes made outside squadrn.
   `srn doctor` checks it for the current GitHub repository (classic protection or a ruleset
   that blocks force pushes and deletion).

Protected branches default to `main master develop release/*`; set your own list with
`srn config set protected_branches …`. A repository can add branches — never remove them:

```sh
git config squadrn.protectedBranches "production staging"
```

Known limit: layer 1 denies `--no-verify`, but when it cannot see the command (a script or an
interpreter runs git), git's `--no-verify` skips layer 2's pre-push hook. Branch protection on
the server is the real backstop; turn it on for every branch that matters.

Decisions other than allow are logged to `~/.local/state/squadrn/guard.log` (`0600`, credentials
masked) with the layer that made them. `srn doctor` also suggests `permissions.deny` rules so
agents cannot edit squadrn's own files with their file tools (`Edit(…)` rules, which cover every
file-editing tool).

## Configuration

`~/.config/squadrn/config.toml` (unknown keys and invalid values are errors):

```toml
schema = 1
command = "ai"                               # set at install; change with: config set command
aliases = ["srn"]
default_provider = "claude"
shared_mcp = ["codegraph"]                   # MCP servers copied from main into new accounts
protected_branches = ["main", "release/*"]   # repositories can only add to this list
git_hooks = true                             # layer 2 of the push guard
update_check = true                          # daily background check, notice only
update_channel = "stable"                    # or "beta" for pre-releases

[notify]
enabled = true
events = ["input", "failed", "done"]         # and "info"
sound = "input"                              # input (input and failed) | all | none
when = "unfocused"                           # or "always"
quiet_hours = ""                             # e.g. "22:00-08:00"
preview = false                              # show the agent's text
method = "auto"                              # auto | desktop | off
```

`srn config [--json]` shows every setting; `srn config get|set <key> [value]` reads or
changes one. In JSON, lists (`aliases`, `shared_mcp`, `protected_branches`) are arrays and
`git_hooks`/`update_check` are booleans. Notification settings are `notify.<key>`
(`srn config set notify.sound none`).

MCP servers whose config has a non-empty `env` or `headers` are never copied; add those per account.

| Path | Contents |
|---|---|
| `~/.local/share/squadrn/` | binary, plugin, local plugin marketplace, `git-hooks/`, `previous/` (rollback) |
| `~/.local/state/squadrn/` | guard log, usage records, notification state and log |
| `~/.cache/squadrn/` | login status cache, update check |

## Exit codes

`0` ok · `1` error, or "no" at a confirmation · `2` usage or invalid input · `130` cancelled
(ctrl-c, esc) · running the agent (`srn [<account>]`, `wt`, `swap`) passes the agent's exit
status through, as `deps` does the package manager's. Commands with `--json` print
machine-readable output on stdout, with snake_case keys: `grid ls --json` gives
`[{"name", "repo", "panes", "attached"}]`, `ps --json` gives
`[{"id", "session", "account", "name", "command"}]`.

## Development

```sh
make build      # bin/squadrn
make check      # lint, race tests, coverage gates, plugin validation
make vuln       # govulncheck
```

Architecture: [docs/architecture.md](docs/architecture.md) · decisions: [docs/adr](docs/adr) ·
[CHANGELOG](CHANGELOG.md). Actions are pinned by commit.

## License

[Apache License 2.0](LICENSE); see [NOTICE](NOTICE). Contributions: [CONTRIBUTING.md](CONTRIBUTING.md);
security reports: [SECURITY.md](SECURITY.md).
