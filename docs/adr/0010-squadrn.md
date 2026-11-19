# 0010 — Product name Squadrn (command `srn`, alias `squadrn`)

Status: accepted (v1.0.0); default command amended (v1.0.0-rc.2)

## Context

The project lives in a public repository and needs a name it can own there: in searches, package
registries and conversation, and on the PATH of the people who install it. The name should be
provider-neutral, since Squadrn drives several agent CLIs.

## Decision

- Product name **Squadrn**, pronounced "squadron": a group of agents flying in formation, each
  under its own account and worktree.
- Checked before choosing: the `squadrn` organizations on GitHub and npm are ours; no command
  `squadrn` or `srn` in Homebrew, Debian or on a default macOS/Linux PATH.
- Accepted risk: the spelling invites the typo "squadron". The command is what people type, and
  the default command `srn` sidesteps it.
- Default command `srn`, alias `squadrn` (amended in v1.0.0-rc.2; v1.0.0-rc.1 installed `squadrn`
  with the alias `srn`). The command is typed all day, so the default is the short name. The full
  name stays as the alias: it is what people find in the docs and search for, and the name to fall
  back to where `srn` is taken. `srn` is less unique: the npm package `srn` installs a command of
  that name. The installer's name check handles it, as for any name: a different program on PATH
  under a chosen name is refused (or confirmed at the terminal, or forced with `--force`), so the
  user picks another name instead of shadowing it.

Fixed identifiers (they never follow the user's command name, ADR 0004):

| Thing | Value |
|---|---|
| Repository, Go module | `github.com/squadrn/squadrn` |
| Binary, `cmd/` dir | `squadrn` (`squadrn.exe`) |
| Default command, alias | `srn`, `squadrn` (installs keep the names they were given) |
| Config/data/state/cache dirs | `~/.config/squadrn`, `~/.local/share/squadrn`, `~/.local/state/squadrn`, `~/.cache/squadrn` |
| Environment variables | `SQUADRN_*` |
| Plugin, marketplace, hook shim | `squadrn@squadrn`, `squadrn-hook` |
| Repository git key | `squadrn.protectedBranches` |
| tmux options | `@squadrn_*` |
| Markers | `.squadrn-account`, `.squadrn-inuse`, `.squadrn-copies.json` |
| Release assets | `squadrn_<ver>_<os>_<arch>.*` |
| Signature namespace | `squadrn-release` |
| Signing key secret | `SQUADRN_SIGNING_KEY` |

## Module path

The module path is `github.com/squadrn/squadrn`, matching the repository. The repository was
first published as `github.com/squadrn/cli`, and versions of that module path are recorded for
good in the Go checksum database (sum.golang.org). Reusing it would tie new code to tags that no
longer exist, so the repository and the module were renamed together and those tags removed.

## Consequences

- Code, docs and help say Squadrn; user-facing text uses the command name the user chose.
- The name and the identifiers above are a contract with installed copies: changing one later
  needs a new ADR and a migration plan.
- Numbers 0003 and 0005 are unused: 0003 (distribution) was folded into 0007, and 0005 (the
  earlier product name) is replaced by this ADR.
