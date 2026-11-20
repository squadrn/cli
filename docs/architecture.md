# Architecture

```
                 ┌────────────────────────────── squadrn (one binary) ──────────────────────────────┐
 user ─────────▶ │ cli: route · account · wt · grid · ps · swap · usage · gc · deps · doctor        │
                 │      config · self-update · self-install · self-uninstall                        │
 Claude Code ──▶ │ hook guard-git · notify · limit  ◀── plugin hooks (fail closed)                  │
                 │ hook statusline                  ◀── statusLine.command (usage setup)            │
 git ──────────▶ │ hook git <name>                  ◀── ~/.local/share/squadrn/git-hooks/<name>     │
                 └──────────────────────────────────────────────────────────────────────────────────┘
   domain:  account · provider (+/claude) · grid · worktree · guard · githook · install
            release · update · doctor · inuse · usage
   uses:    ~/.claude*, claude CLI, tmux, git, mvdan/sh, HTTPS (GitHub REST API)
   shared:  proc (process trees, deadlines) · fsx (atomic files, locks) · config · gitver
```

## Packages

| Package | Owns | Never does |
|---|---|---|
| `cli` | argument routing, flags, output (text, `--json`, colors), exit codes | business rules |
| `account` | `provider/name` refs, resolution, provisioning (links, MCP allowlist, plugin), rename | printing |
| `provider` | `Spec` (data: dirs, env var, shared items, worktree dir, subcommands, capabilities), the `Agent` and capability interfaces, the driver registry, the auth cache ([ADR 0011](adr/0011-provider-model-v2.md)) | provider internals |
| `provider/claude` | everything about Claude Code: `CLAUDE_CONFIG_DIR`, auth status, transcripts, plugin registry paths, the status line; registers its driver | — |
| `provider/codex` | everything about the Codex CLI: `CODEX_HOME`, the file credential store in `config.toml`, `login status`, the guard hook in `hooks.json` and its trust, `mcp`, the app server (account, rate limits); registers its driver | — |
| `provider/all` | imports every provider package so its driver is registered (`cli` imports this, never a provider package) | — |
| `grid` | agent assignment (`--spread`), tmux sessions and pane tags (batched, race-safe) | git |
| `worktree` | repo root, agent worktrees, gc classification, removal, lockfile detection | tmux |
| `guard` | shell parsing, push decisions (pre-tool and pre-push), protected-branch policy, decision log | I/O beyond git and the log |
| `githook` | git hook shims, `core.hooksPath` injection, chaining to the repository's own hooks | decisions |
| `install` | config loading, command-name checks, plugin rollout | printing, network |
| `release` | signature and checksum verification, bounded extraction, installed layout, snapshots for rollback, version order | network (cli downloads) |
| `update` | when to check for releases, channel selection, the cached result | network, printing |
| `doctor` | structured health checks, including all three guard layers | printing |
| `inuse` | which sessions run in a directory (`<dir>/.squadrn-inuse/<pid>.json`, live by pid and start time), per-repository lock files | printing |
| `usage` | per-account usage snapshots (limits, last limit hit), merge rules, states, ranking for a swap, the handoff ledger ([ADR 0009](adr/0009-usage-and-session-handoff.md)) | provider knowledge, printing |
| `proc` | running programs under a deadline that kills the whole process tree (process group on Unix, Job Object on Windows) | — |
| `fsx` | atomic writes, copies, directory swaps, lock files, replacing a running executable, directory ACLs on Windows | — |
| `gitver` | the Git version floor (2.31) and the upgrade hint | — |

Domain packages return values and typed errors; only `cli` prints. Sentinel errors map to exit
code 2 in one place (`cli/exit.go`). Every external program runs through `proc`, so a deadline
also stops its grandchildren.

## Key flows

**Routing.** `srn [globals] [<account>] <command|agent args|-- prompt>`. Subcommands win over
account names, agent subcommands (`Spec.Subcommands`) pass through, anything else is a usage error
with suggestions. `hook …` is dispatched before config, store or cobra, so it cannot fail open.

**Run as an account.** `cli.app.agent(spec)` returns the provider's registered `Agent` (Claude: `claude.CLI`)
with the git-level guard's `GIT_CONFIG_*` entries in its environment → `Exec` replaces the
process (on Windows: runs it as a child and passes its exit status through), with
`CLAUDE_CONFIG_DIR` set, or *unset* for main (Claude Code keys credentials to the variable).

**Grid.** `grid.Assign` turns specs into agents → one tmux session per repo, created and tagged
(`@squadrn_repo`) in a single tmux invocation so concurrent `grid` runs join instead of
duplicating; one pane per agent running `squadrn -a <provider/name> wt <name>`; panes carry
`@squadrn_acct`/`@squadrn_name`, which `ps`, `wt ls`, re-runs and `swap` read back.

**In use.** Before a session starts, `cli` records it with `inuse.Acquire` in the account's config
dir and in the worktree it works in (in the main checkout, tagged with the worktree name, when
`wt <name>` has not created it yet; `.squadrn-inuse/` is added to `info/exclude`). On Unix the
recorded pid becomes the agent CLI, so the entry lives exactly as long as the session. `gc` keeps,
and `wt rm`, `account rm` and `account rename` refuse (unless `--force`), what a live entry names.

**Swap.** Claude Code stores conversations at `<config>/projects/<folder>/<session>.jsonl` (the
folder encodes the working directory; long ones are shortened by an undocumented rule), with a
`<session>/` dir (subagents, tool results) and checkpoints in `<config>/file-history/<session>/`.
`swap` finds a conversation by session ID with a glob over every account of the provider
(`SessionStore.FindSession`), or, with `--worktree <name>`, the newest one of that worktree.
Without `--session` the candidates come from the usage store (the last limit hit in or below the
working directory, unless the ledger shows it moved since) and from Claude Code's session registry
`<config>/sessions/<pid>.json` (read as a hint). Before copying, every live holder is stopped
(a grid pane is respawned; otherwise the registry and the in-use records name the process, which
gets an interrupt, then — asked again — termination, through the `cli/stop_unix.go` /
`stop_windows.go` pair). `SessionStore.TransferSession` (`provider/claude`) copies under the same folder name, refuses a
target copy that is not a prefix of the source (`--force` overrides), verifies size and SHA-256
and renames the transcript into place last. The handoff is appended to the state dir's
`handoffs.jsonl` (`usage.Ledger`). Only `provider/claude` knows the layout; `cli` reaches it
through `provider.SessionStore`.

**Usage.** Claude Code's status line runs `squadrn hook statusline --then '<old command>'` once
`usage setup` has wrapped `statusLine.command` in the account's `settings.json`. The hook parses
the `rate_limits` it receives (`provider/claude`), finds the account from `CLAUDE_CONFIG_DIR`,
merges the observation into `~/.local/state/squadrn/usage/<provider>-<name>.json` (`usage.Store`)
and passes the wrapped command's output through. The plugin's `StopFailure` hook (matcher
`rate_limit`) runs `squadrn hook limit`, which records the limit with a reset estimate and sends a
desktop notification. `srn usage` reads the store, derives each account's state
(`usage.Effective`) and ranks the accounts a conversation could move to (`usage.Rank`), the same
ranking `swap` offers ([ADR 0009](adr/0009-usage-and-session-handoff.md)).

**Guard.** See [ADR 0006](adr/0006-layered-push-guard.md). Layer 1: `guard.Decide` parses with
`mvdan.cc/sh`, normalises words, follows wrappers, nested shells and aliases, and judges each push
against git facts (branch, config) read once per directory under one deadline. Layer 2:
`guard.DecidePrePush` judges the ref updates git hands the pre-push hook. Unknown → ask (layer 1)
or deny (layer 2).

**Update.** At a terminal, `cli.updateNotice` reads `~/.cache/squadrn/update.json`, prints one line
when a newer release is known (at most every 6 h), and when a check is due (24 h) starts a detached
`self-update --background` that lists releases through the GitHub REST API (`update.GitHub`) and
writes the cache. `self-update`
downloads the archive, `checksums.txt` and its signature, verifies the signature against the
embedded keys, then the checksum, snapshots the installed release to `previous/`, and installs
(ADR 0007).

## Install layout

```
~/.local/share/squadrn/          bin/squadrn, plugin/, .claude-plugin/ (local marketplace), git-hooks/,
                                VERSION, previous/ (rollback)
~/.local/bin/<command|alias> → ~/.local/share/squadrn/bin/squadrn   (copies on Windows)
~/.config/squadrn/config.toml    schema, command, aliases, default provider, MCP allowlist,
                                protected branches, git_hooks
~/.local/state/squadrn/          guard.log (0600, dir 0700, credentials masked)
~/.local/state/squadrn/usage/    <provider>-<name>.json usage per account (0600)
~/.local/state/squadrn/handoffs.jsonl  one line per swap (time, session, from, to, cwd)
~/.cache/squadrn/auth.json       login status cache (60 s; failures 15 s; 0600)
~/.cache/squadrn/update.json     last update check and notice
```

All of these honour absolute `XDG_*` variables; on Windows `~` is `%USERPROFILE%`.
