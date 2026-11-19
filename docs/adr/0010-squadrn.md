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

`SPRAWL_RELEASES_*` (releases repository settings) stay until the separate releases repository
goes away with the move to the public repository.

## Compatibility

Previous names are `config.PreviousNames = ["sprawl", "squad"]`, newest first; every legacy
identifier is derived from that list. Through the whole 2.x line:

- The first install of Squadrn migrates sprawl's (and squad's) config, data and state dirs into a
  backup under `~/.local/state/squadrn/legacy-<name>-*`, uninstalls `sprawl@sprawl` and
  `squad@squad` from every account, installs `squadrn@squadrn`, and removes command links (or, on
  Windows, the copies listed in the old manifest) only where they point to the old install.
- Command names the user chose are kept (`ai` stays `ai`); the old defaults (`sprawl` + `sra`,
  `squad` + `sq`) become `squadrn` + `sqd`.
- `sprawl.protectedBranches` and `squad.protectedBranches` keep protecting branches in both guard
  layers: a rename must never drop protection.
- Old markers are read and rewritten under the new names; live entries of `.sprawl-inuse` move
  into `.squadrn-inuse`; grids tagged with `@sprawl_*` options are still found and joined.
- `SPRAWL_*` variables are read when the `SQUADRN_*` one is unset, with a one-line warning on
  stderr (never from hooks, whose output the agent CLI reads).
- The guard treats the old names (`sprawl`, `sra`, `sprawl-hook`, `squad`, …) and the old dirs
  like the current ones: changing the policy through them is denied.

Not carried over: release signatures in the `sprawl-release` namespace. A sprawl binary cannot
update itself to Squadrn (other asset names, other namespace); users install Squadrn once with the
installer and the migration above runs then. The release workflow's upgrade test skips
predecessors without `squadrn_*` assets for the same reason.

## Consequences

- Version 2.0.0: the command, paths and plugin id change, which is a breaking change for scripts
  that call `sprawl` or read its paths, even though installs migrate themselves.
- Code, docs and help say Squadrn; the old names appear only in `config.PreviousNames`, the legacy
  handling and its tests, ADR history and the changelog.
