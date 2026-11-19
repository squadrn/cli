# 0004 — User-chosen command and provider/name accounts

Status: accepted

## Context

Squadrn drives agent CLIs (Claude Code today, Codex, Gemini and others later), so neither the
product nor its accounts are named after one of them. People want their own command name, and an
account name alone stops being unique once two providers each have a `work` account.

## Decision

Separate three kinds of name:

| Kind | Chosen by | Examples |
|---|---|---|
| Product identity | fixed (ADR 0010) | `~/.config/squadrn`, `SQUADRN_*`, plugin `squadrn@squadrn`, git key `squadrn.protectedBranches`, tmux options `@squadrn_*` |
| Command name | each user at install (default `squadrn`, alias `sqd`), renamable, with aliases | `ai`, `sx` |
| Account | provider + name | `claude/main`, `claude/personal`, later `codex/work` |

- The binary has a fixed name and path (`~/.local/share/squadrn/bin/squadrn`); command names are
  symlinks in `~/.local/bin`. The plugin shim calls the fixed path, so hooks work whatever the
  command is called. Renaming relinks and regenerates completions; nothing else changes.
- Name collisions with other programs are refused when a name is chosen (override with
  `--force`); names accepted once are not re-checked on upgrade.
- Grid sessions are named `<command>-<repo>` but found by the `@squadrn_repo` tag, so a rename
  never orphans a running grid and two repositories with the same directory name get separate
  sessions.
- Account labels are `provider/name`; a bare name resolves when unique across providers, `main`
  means the default provider's main. The command name is not part of labels.
- Providers are data (`provider.Spec`: dirs, config env var, CLI, capabilities); behaviour is
  behind `provider.Agent`. Only Claude Code is registered.

## Consequences

- One install per machine with any number of command names; configs and plugins stay stable.
- Fixed identifiers never follow the command name; user-facing text does.
- For Codex/Gemini: Squadrn manages worktrees itself and adds a git-level `pre-push` guard for
  providers without pre-tool hooks. Their config-dir variables and resume semantics still need to
  be verified.
