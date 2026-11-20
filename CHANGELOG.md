# Changelog

## Unreleased

- **`srn account rm <account> --purge`:** logs the account out, deletes its dir permanently
  instead of moving it to the trash (links inside are removed, never followed; a linked dir is
  refused) and removes every record squadrn keeps of it: usage record, handoff ledger entries,
  in-use records, config change log entries and cached login status. The output lists what was
  removed. At a terminal it asks you to type the account unless `-y` is given; without one it
  needs `--purge --yes`. Works for Claude Code and Codex accounts.

## 1.0.0-rc.3

- **OpenAI Codex CLI:** `codex/<name>` accounts (`CODEX_HOME`, credentials kept per account),
  login, runs, `--worktree`, resume and swap between Codex accounts, MCP, usage limits in
  `srn usage` (read from Codex, cached for 5 minutes), and the push guard as a Codex pre-tool hook
  plus the git pre-push hook. Codex cannot ask, so its guard denies what it would ask about; on
  Windows a hook that fails to start lets the command run (`srn doctor` says so).
- **Profiles:** `srn account add` offers `minimal`, `standard` (default) and `strict`, with the
  `privacy` and `no-attribution` toggles (`--profile`, or `default_profile` in the config). The
  standard profile denies reading secrets (`.env`, `~/.ssh`, cloud and package credentials, the
  password store, other accounts' credential files) and asks before `git push`, `git reset --hard`
  and `rm -rf`.
- **Own settings per account:** an account's `settings.json` / `config.toml` is now its own copy
  built from main's settings and its profile, never a link. Squadrn keeps only the keys it owns in
  sync and keeps your own changes (`/model`, `/config`). Existing links are turned into copies on
  the first session (backup kept).
- **`account add` rewritten:** it plans, asks everything up front, applies with progress and
  verifies; a re-run says the account is up to date; a failed step leaves nothing half-created and
  names the retry command. A first add takes about 1.5s with Claude Code (was 4.2s), a re-run
  0.3s.
- **`srn setup`:** a guided first run (agent CLIs found, default provider and profile, adopting
  existing account dirs, usage tracking, doctor). The installer runs it after the first install at
  a terminal; `self-update` never does.
- **Windows:** the push-guard hook now starts `squadrn.exe` directly and blocks when it cannot run
  (Claude Code 2.1.295 or newer; doctor warns on older ones). With rc.1/rc.2 every Bash and
  PowerShell tool call could be blocked. Git for Windows is found under Git Bash's own `git.exe`
  too, and alias copies are recognised after a rename.
- Account names only exclude squadrn's own commands; agent subcommands go through
  `srn <account> <subcommand>`.

## 1.0.0-rc.2

- **Breaking:** the default command is now `srn`, with `squadrn` as its alias (was `squadrn` with
  the alias `srn`); the installers and `self-install --interactive` offer the new defaults. Existing
  installs keep their configured names; to switch, run `squadrn config set command srn`, then
  `srn config set aliases squadrn`. Making an alias the command (`config set command`, the
  installer's `--command` or its prompt) swaps the two names instead of failing.
- `doctor` recommends only `Edit(…)` deny rules for squadrn's files: Claude Code warned at every
  start that the `Write(…)` rules it also suggested are not matched. Settings that have the `Edit`
  rules pass, with or without `Write` ones; remove the `Write(~/.local/share/squadrn/**)` and
  `Write(~/.config/squadrn/**)` rules to silence the warning.
- **Breaking (JSON):** `grid ls --json` and `ps --json` use snake_case keys (`name`, `repo`,
  `panes`, `attached`; `id`, `session`, `account`, `name`, `command`); `config --json` and
  `config get --json` give lists as arrays and `git_hooks`/`update_check` as booleans.
- `wt rm` decides before removing anything: an unmerged branch is refused up front, or with
  `--force-branch` the worktree and branch go together ("deleted branch …"); a branch left without
  its worktree follows the same rule (merged: deleted; unmerged: `--force-branch`). Refusals name
  the real reason and the worktree.
- `account add` creates main's `settings.json` (`{}`) when it is missing, so accounts always link
  to it; `account add <account> --relink` moves an account's own copies of shared files aside
  (`.bak-<time>`) and links main's, and is what `doctor` and the session warning suggest.
- `account login` says who is logged in; an unmanaged `~/.claude-<name>` gets `account adopt
  <name>` suggested; `account rm` uses `~/.Trash` on macOS.
- `usage`: the current account counts for "best for this repo", resets not today show the weekday,
  logged-out accounts are not listed as untracked; `usage setup --all` asks once for every file.
- `config set` prints the value that applies (defaults marked); alias changes no longer read
  `command: srn → srn`; invalid names are reported once.
- `completion <shell>` covers the command and every alias, and an unknown shell is a usage error;
  `grid attach` explains a missing terminal and keeps tmux's own connection errors.
- `self-uninstall` checks that the marketplace registration is gone; `--purge` also removes the
  session registries it knows of. Development builds check `squadrn/squadrn` for updates, report
  "no release yet" instead of failing, and call themselves `dev`.

## 1.0.0-rc.1

First public release of Squadrn: run Claude Code as several of your own accounts, in parallel git
worktrees, with a guard against destructive pushes. Apache-2.0.

- **Accounts:** `squadrn <account>` runs Claude Code as `provider/name` (`claude/main`,
  `claude/work`, …); `account add|login|logout|ls|rename|rm|adopt`; settings, skills, plugins and MCP
  servers shared with the main account.
- **Parallel work:** `wt <name>` runs an agent in its own worktree; `grid` lays several out in tmux
  (macOS, Linux); `ps`, `gc` (never deletes unmerged or in-use work), `deps`.
- **Usage limits:** `usage` shows each account's 5-hour and weekly usage from Claude Code's status
  line (`usage setup`), with the best account for the current repository.
- **Swap:** `swap [--session <id> | --worktree <name>] [--to <account>]` continues a conversation on
  another of your accounts, after stopping the old session; checkpoints for `/rewind` move with it.
- **Push guard:** a Claude Code hook (Bash and PowerShell) and a git pre-push hook stop force pushes
  and pushes to protected branches (`main`, `master`, `develop`, `release/*` by default, plus
  `git config squadrn.protectedBranches`); `doctor` checks the guard and server-side protection. A
  safety net, not a sandbox.
- **Install and update:** one-line installers (`curl … | sh`, `irm … | iex`) that verify the release
  signature and checksum (`--command`/`-Command`, `--alias`/`-Alias`, `--version`/`-Version`);
  `self-update [--tag <tag>]` (stable or beta channel), `--rollback`, `self-uninstall`; your own
  command name and alias (`squadrn`/`srn` by default).
- **Platforms:** macOS and Linux (amd64, arm64); Windows 10 21H2+/11 (amd64, arm64) in beta, without
  `grid`. Git 2.31 or newer.
- Release signing key `squadrn-release-20261119`.
