# Architecture

```
                 ┌──────────────────────────── sprawl (one binary) ────────────────────────────┐
 user ─────────▶ │ cli: route · account · wt · grid · ps · swap · gc · deps · doctor · config  │
 Claude Code ──▶ │ hook guard-git · hook notify   ◀── plugin/bin/sprawl-hook (fail-closed)     │
 git ──────────▶ │ hook git <name>                ◀── ~/.local/share/sprawl/git-hooks/<name>   │
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
| `proc` | running programs under a deadline that kills the whole process group | — |
| `fsx` | atomic writes, copies, directory swaps | — |

Domain packages return values and typed errors; only `cli` prints. Sentinel errors map to exit
code 2 in one place (`cli/exit.go`). Every external program runs through `proc`, so a deadline
also stops its grandchildren.

## Key flows

**Routing.** `sprawl [globals] [<account>] <command|agent args|-- prompt>`. Subcommands win over
account names, agent subcommands (`Spec.Subcommands`) pass through, anything else is a usage error
with suggestions. `hook …` is dispatched before config, store or cobra, so it cannot fail open.

**Run as an account.** `cli.app.agent(spec)` returns the provider's `Agent` (Claude: `claude.CLI`)
with the git-level guard's `GIT_CONFIG_*` entries in its environment → `Exec` replaces the
process, with `CLAUDE_CONFIG_DIR` set, or *unset* for main (Claude Code keys credentials to the
variable).

**Grid.** `grid.Assign` turns specs into agents → one tmux session per repo, created and tagged
(`@sprawl_repo`) in a single tmux invocation so concurrent `grid` runs join instead of
duplicating; one pane per agent running `sprawl -a <provider/name> wt <name>`; panes carry
`@sprawl_acct`/`@sprawl_name`, which `ps`, `wt ls`, re-runs and `swap` read back.

**Swap.** Claude Code stores conversations at `<config>/projects/<cwd key>/<session>.jsonl`.
`swap` copies the newest one across accounts (with its subagent dir, mtime preserved) and resumes
it — in place via `respawn-pane` inside a grid. Only `provider/claude/transcript.go` knows this layout.

**Guard.** See [ADR 0006](adr/0006-layered-push-guard.md). Layer 1: `guard.Decide` parses with
`mvdan.cc/sh`, normalises words, follows wrappers, nested shells and aliases, and judges each push
against git facts (branch, config) read once per directory under one deadline. Layer 2:
`guard.DecidePrePush` judges the ref updates git hands the pre-push hook. Unknown → ask (layer 1)
or deny (layer 2).

**Update.** At a terminal, `cli.updateNotice` reads `~/.cache/sprawl/update.json`, prints one line
when a newer release is known (at most every 6 h), and when a check is due (24 h) starts a detached
`self-update --background` that lists releases with `gh` and writes the cache. `self-update`
downloads the archive, `checksums.txt` and its signature, verifies the signature against the
embedded keys, then the checksum, snapshots the installed release to `previous/`, and installs
(ADR 0007).

## Install layout

```
~/.local/share/sprawl/          bin/sprawl, plugin/, .claude-plugin/ (local marketplace), git-hooks/,
                                VERSION, previous/ (rollback)
~/.local/bin/<command|alias> → ~/.local/share/sprawl/bin/sprawl
~/.config/sprawl/config.toml    schema, command, aliases, default provider, MCP allowlist,
                                protected branches, git_hooks
~/.local/state/sprawl/          guard.log (0600, dir 0700, credentials masked), legacy-<name>-* backups
~/.cache/sprawl/auth.json       login status cache (60 s; failures 15 s; 0600)
~/.cache/sprawl/update.json     last update check and notice
```

All of these honour absolute `XDG_*` variables on macOS and Linux alike.
