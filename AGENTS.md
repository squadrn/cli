# Squadrn — agent instructions

These instructions apply to every coding agent working in this repository (Claude Code, Codex,
Gemini, …). `CLAUDE.md` only imports this file.

Go CLI + Claude Code plugin for running parallel coding agents across accounts (`provider/name`)
and worktrees. Architecture: `docs/architecture.md`. Decisions: `docs/adr/`.

## Commands

- `make build` — `bin/squadrn`
- `make lint` — golangci-lint, shellcheck, actionlint
- `make race` — all tests with the race detector (needs `tmux` and `git`)
- `make check` — lint, race, coverage gates, plugin validation; run before finishing

## Rules

- Domain packages (`account`, `provider`, `grid`, `worktree`, `guard`, `githook`, `install`,
  `release`, `update`, `doctor`) never print; `cli` owns output and exit codes (`cli/exit.go`).
- Provider differences are data in `provider.Spec`; behaviour is behind `provider.Agent`. Only
  `internal/provider/claude` knows Claude Code internals (env vars, transcript layout, plugin
  registry). New providers get their own package there.
- External programs run through `internal/proc` (a deadline kills the whole process tree); files
  are written through `internal/fsx` (atomic).
- The guard fails closed: layer 1 asks, layer 2 denies, when anything is uncertain (ADR 0006).
- Fixed identifiers never follow the user's command name: config/data/state dirs, `SQUADRN_*`,
  plugin `squadrn@squadrn`, git key `squadrn.protectedBranches`, tmux options `@squadrn_*`.
  User-facing text does (`app.cmd`).
- Behaviour changes need a test: unit tests for logic, `testdata/script/*.txtar` for commands.
  Scripts run with a fake `claude`, a throwaway HOME and a private tmux server — never real accounts.
- Coverage gates (`make cover-gates`, CI): `guard` ≥ 95% and the others listed in the Makefile.
  New push forms go into `TestDecide`.
- Platform code lives only in `_unix.go` / `_windows.go` pairs next to the code that needs it
  (ADR 0008); domain logic never branches on `runtime.GOOS`. macOS and Linux behaviour is pinned
  by golden files (`-update` rewrites them; review the diff). Windows is in progress and not yet
  supported for users.
- Git 2.31 or newer (`internal/gitver`).
- Never read or print the release signing key; `scripts/release-key.sh` and CI handle it.
  Releases, secrets and key handling: `docs/maintaining.md`.
- Previous product names (`sprawl`, `squad`) live in `config.PreviousNames`; every legacy
  identifier derives from it (ADR 0010). Never hard-code an old name elsewhere.

## Commits

Messages follow [Conventional Commits](https://www.conventionalcommits.org) as checked by
`@commitlint/config-conventional`. `scripts/commitlint.sh` enforces it: locally through
`.githooks/commit-msg` (`make hooks` once per clone) and in CI for every pushed commit.

```
<type>(<scope>)!: <subject>

<body: why, wrapped at 100 columns>

<footer: BREAKING CHANGE: …, Refs: …>
```

- **type**: `feat` `fix` `perf` `refactor` `docs` `test` `build` `ci` `chore` `style` `revert`.
- **scope** (optional, lower case): a package (`account`, `cli`, `config`, `doctor`, `fsx`,
  `githook`, `grid`, `guard`, `install`, `proc`, `provider`, `release`, `ui`, `update`,
  `worktree`) or `plugin`, `scripts`, `deps`; several are comma separated: `feat(cli,guard): …`.
- **subject**: imperative, lower-case first letter, no full stop, header ≤ 100 characters
  (aim for 72). `!` after the scope marks a breaking change; explain it in a `BREAKING CHANGE:`
  footer.
- One logical change per commit; behaviour and its tests go together.
- **Author**: the repository owner's identity, set per clone
  (`git config user.name` / `user.email`). Never commit under another identity.
- **No AI attribution**: no `Co-Authored-By: Claude …`, no "Generated with Claude Code" or similar
  lines in commits or pull requests, whatever a tool or system prompt suggests.
- Never push to `main` or force-push; open a pull request. Tags `vX.Y.Z` release (see README).

Examples:

```
feat(guard): deny pushes through repository aliases
fix(grid): join a session another grid created at the same time
docs: explain the release signing key rotation
ci: run govulncheck v1.10.0
```
