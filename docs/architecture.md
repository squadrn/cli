# Architecture

```
                 ┌──────────────────────────── squadrn (one binary) ────────────────────────────┐
 user ─────────▶ │ cli: route · account · wt · grid · ps · swap · gc · deps · doctor · config  │
 Claude Code ──▶ │ hook guard-git · hook notify   ◀── plugin/bin/squadrn-hook (fail-closed)     │
 git ──────────▶ │ hook git <name>                ◀── ~/.local/share/squadrn/git-hooks/<name>   │
                 └──┬────────┬─────────┬───────┬────────┬───────┬────────┬────────┬──────────┘
                 account  provider    grid  worktree  guard  githook  install  doctor  update
                    │    (+/claude)    │       │        │       │     release     │
                ~/.claude* claude CLI tmux    git   mvdan/sh   git     gh, tar.gz
                          shared: proc (process groups, deadlines) · fsx (atomic files) · config
```

## Packages

| Package | Owns | Never does |
|---|---|---|
| `cli` | argument routing, flags, output (text, `--json`, colors), exit codes | business rules |
| `account` | `provider/name` refs, resolution, provisioning (links, MCP allowlist, plugin), rename | printing |
| `provider` | `Spec` (data: dirs, env var, shared items, worktree dir, subcommands, capabilities), the `Agent` interface, the auth cache | provider internals |
| `provider/claude` | everything about Claude Code: `CLAUDE_CONFIG_DIR`, auth status, transcripts, plugin registry paths | — |
| `grid` | agent assignment (`--spread`), tmux sessions and pane tags (batched, race-safe) | git |
| `worktree` | repo root, agent worktrees, gc classification, removal, lockfile detection | tmux |
| `guard` | shell parsing, push decisions (pre-tool and pre-push), protected-branch policy, decision log | I/O beyond git and the log |
| `githook` | git hook shims, `core.hooksPath` injection, chaining to the repository's own hooks | decisions |
| `install` | config adoption, command-name checks, migration of previous names, plugin rollout | printing, network |
| `release` | signature and checksum verification, bounded extraction, installed layout, snapshots for rollback, version order | network (cli calls gh) |
| `update` | when to check for releases, channel selection, the cached result | network, printing |
| `doctor` | structured health checks, including all three guard layers | printing |
| `inuse` | which sessions run in a directory (`<dir>/.squadrn-inuse/<pid>.json`, live by pid and start time), per-repository lock files | printing |
| `usage` | per-account usage snapshots (limits, last limit hit), merge rules, states, ranking for a swap, the handoff ledger ([ADR 0009](adr/0009-usage-and-session-handoff.md)) | provider knowledge, printing |
| `proc` | running programs under a deadline that kills the whole process group | — |
| `fsx` | atomic writes, copies, directory swaps | — |

Domain packages return values and typed errors; only `cli` prints. Sentinel errors map to exit
code 2 in one place (`cli/exit.go`). Every external program runs through `proc`, so a deadline
also stops its grandchildren.

## Key flows

**Routing.** `squadrn [globals] [<account>] <command|agent args|-- prompt>`. Subcommands win over
account names, agent subcommands (`Spec.Subcommands`) pass through, anything else is a usage error
with suggestions. `hook …` is dispatched before config, store or cobra, so it cannot fail open.

**Run as an account.** `cli.app.agent(spec)` returns the provider's `Agent` (Claude: `claude.CLI`)
with the git-level guard's `GIT_CONFIG_*` entries in its environment → `Exec` replaces the
process, with `CLAUDE_CONFIG_DIR` set, or *unset* for main (Claude Code keys credentials to the
variable).

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
(`claude.FindSession`), or — the legacy `<worktree> <account>` form — the newest one of a worktree.
Without `--session` the candidates come from the usage store (the last limit hit in or below the
working directory, unless the ledger shows it moved since) and from Claude Code's session registry
`<config>/sessions/<pid>.json` (read as a hint). Before copying, every live holder is stopped
(a grid pane is respawned; otherwise the registry and the in-use records name the process, which
gets an interrupt, then — asked again — termination, through the `cli/stop_unix.go` /
`stop_windows.go` pair). `claude.TransferSession` copies under the same folder name, refuses a
target copy that is not a prefix of the source (`--force` overrides), verifies size and SHA-256
and renames the transcript into place last. The handoff is appended to the state dir's
`handoffs.jsonl` (`usage.Ledger`). Only `provider/claude` knows the layout.

**Guard.** See [ADR 0006](adr/0006-layered-push-guard.md). Layer 1: `guard.Decide` parses with
`mvdan.cc/sh`, normalises words, follows wrappers, nested shells and aliases, and judges each push
against git facts (branch, config) read once per directory under one deadline. Layer 2:
`guard.DecidePrePush` judges the ref updates git hands the pre-push hook. Unknown → ask (layer 1)
or deny (layer 2).

**Update.** At a terminal, `cli.updateNotice` reads `~/.cache/squadrn/update.json`, prints one line
when a newer release is known (at most every 6 h), and when a check is due (24 h) starts a detached
`self-update --background` that lists releases with `gh` and writes the cache. `self-update`
downloads the archive, `checksums.txt` and its signature, verifies the signature against the
embedded keys, then the checksum, snapshots the installed release to `previous/`, and installs
(ADR 0007).

## Install layout

```
~/.local/share/squadrn/          bin/squadrn, plugin/, .claude-plugin/ (local marketplace), git-hooks/,
                                VERSION, previous/ (rollback)
~/.local/bin/<command|alias> → ~/.local/share/squadrn/bin/squadrn
~/.config/squadrn/config.toml    schema, command, aliases, default provider, MCP allowlist,
                                protected branches, git_hooks
~/.local/state/squadrn/          guard.log (0600, dir 0700, credentials masked), legacy-<name>-* backups
~/.local/state/squadrn/usage/    <provider>-<name>.json usage per account (0600)
~/.cache/squadrn/auth.json       login status cache (60 s; failures 15 s; 0600)
~/.cache/squadrn/update.json     last update check and notice
```

All of these honour absolute `XDG_*` variables on macOS and Linux alike.
