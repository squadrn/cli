# 0004 — Product name squad, user-chosen command, provider/name accounts

Status: accepted (v3.0.0); the product name is superseded by 0005 — the structure stays

## Context

`ccx` named the product after Claude Code while the goal is to drive several agent CLIs (Codex,
Gemini, …). It was also the command everyone had to type, and the prefix of account labels
(`ccx-personal`). People want their own command name, and an account name alone stops being
unique once two providers each have a `work` account.

## Decision

Separate three kinds of name:

| Kind | Chosen by | Examples |
|---|---|---|
| Product identity | fixed | repo/module `squad`, `~/.config/squad`, `SQUAD_*`, plugin `squad@squad`, git key `squad.protectedBranches`, tmux options `@squad_*` |
| Command name | each user at install (default `squad`), renamable, with aliases | `ai`, `sq`, `ccx` |
| Account | provider + name | `claude/main`, `claude/personal`, later `codex/work` |

- The binary has a fixed name and path (`~/.local/share/squad/bin/squad`); command names are
  symlinks in `~/.local/bin`. The plugin shim calls the fixed path, so hooks work whatever the
  command is called. Renaming relinks and regenerates completions; nothing else changes.
- Name collisions with other programs are refused at the time a name is chosen (override with
  `--force`); names accepted once are not re-checked on upgrade.
- Grid sessions are named `<command>-<repo>` but found by the `@squad_repo` tag, so a rename never
  orphans a running grid and two repositories with the same directory name get separate sessions.
- Account labels are `provider/name`; a bare name resolves when unique across providers, `main`
  means the default provider's main. The command name is not part of labels.
- Providers are data (`provider.Spec`: dirs, config env var, CLI, capabilities). Only Claude Code is
  registered. The behavioural interface (exec, auth, sessions, worktrees, guard installation) is
  extracted when the second provider lands, so it is shaped by two real implementations.

Name candidates were checked against PATH and Homebrew; `squad` had no collision, while `herd`,
`fleet`, `relay`, `hive`, `hydra`, `flock`, `orca`, `maestro` did.

## Consequences

- One install per machine with any number of command names; configs and plugins stay stable.
- ccx installs migrate automatically (settings, plugin, git key fallback, files backed up).
- For Codex/Gemini (R2/R3): squad will manage worktrees itself (`<repo>/.squad/worktrees`) and add a
  git-level `pre-push` guard for providers without pre-tool hooks. Their config-dir variables and
  resume semantics still need to be verified.
