# 0010 — Name: Squadrn (`squadrn`, alias `sqd`)

Status: accepted (v2.0.0); supersedes the name in 0005 — the structure from 0004 stays

## Context

The project moves to a public home, `github.com/squadrn/cli`, and needs a name it can own there.
"Sprawling Agents" (0005) had an accepted risk that became real: `2youg1/sprawling-agents` uses the
same name, and a "Sprawl CLI" already exists in the coding-agent space, so `sprawl` reads as
someone else's tool in searches, package registries and conversation.

## Decision

- Product name **Squadrn**, pronounced "squadron": a squad of agents flying in formation. It keeps
  the lineage of squad (0004) without its collisions (`claude-squad`, `github/squad`).
- Checked before choosing: the `squadrn` organisations on GitHub and npm are ours; no command
  `squadrn` or `sqd` in Homebrew, Debian/Ubuntu or on a default macOS/Linux PATH.
- Accepted risk: the spelling invites the typo "squadron". The command is what people type, and
  the default alias `sqd` sidesteps it.

| Thing | Before (sprawl) | Now |
|---|---|---|
| Product name | Sprawling Agents | Squadrn |
| Go module | `sprawling-agents` | `github.com/squadrn/cli` |
| Binary, `cmd/` dir | `sprawl` | `squadrn` (`squadrn.exe`) |
| Default command, alias | `sprawl`, `sra` | `squadrn`, `sqd` |
| Slug: config/data/state/cache dirs | `~/.config/sprawl` … | `~/.config/squadrn` … |
| Environment variables | `SPRAWL_*` | `SQUADRN_*` |
| Plugin, marketplace, hook shim | `sprawl@sprawl`, `sprawl-hook` | `squadrn@squadrn`, `squadrn-hook` |
| Repository git key | `sprawl.protectedBranches` | `squadrn.protectedBranches` |
| tmux options | `@sprawl_*` | `@squadrn_*` |
| Markers | `.sprawl-account`, `.sprawl-inuse`, `.sprawl-copies.json` | `.squadrn-account`, `.squadrn-inuse`, `.squadrn-copies.json` |
| Release assets | `sprawl_<ver>_<os>_<arch>.*` | `squadrn_<ver>_<os>_<arch>.*` |
| Signature namespace | `sprawl-release` | `squadrn-release` (same key, `signers.pub` unchanged) |
| Signing key secret | `SPRAWL_SIGNING_KEY` | `SQUADRN_SIGNING_KEY` |

`SPRAWL_RELEASES_*` (releases repository settings) stayed until the separate releases
repository went away with the move to the public repository (ADR 0007).

## Previous names: no compatibility

Squadrn is a clean break. It neither migrates nor honours anything of sprawl or squad: no old
config, dirs, plugins, command links, markers, tmux options, environment variables, git keys or
release signatures, and the code carries no list of previous names. Compatibility code is
security-relevant (the guard would have to keep trusting old keys and paths) for a user base
small enough to reinstall once.

Coming from sprawl:

1. `sprawl self-uninstall --purge` removes the old tool, its plugin in every account and its
   files.
2. Install Squadrn fresh with the installer.
3. Repositories that set `git config sprawl.protectedBranches` must set
   `git config squadrn.protectedBranches` instead: that protection is **not carried over**.

The release workflow's upgrade test skips predecessors without `squadrn_*` assets: an earlier
release cannot update itself to Squadrn.

## Consequences

- Version 2.0.0: the command, paths, environment variables and plugin id change, and nothing is
  migrated.
- Code, docs and help say Squadrn; the old names appear only in ADR history and the changelog.
