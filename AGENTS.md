# Squadrn — agent instructions

These instructions apply to every coding agent working in this repository (Claude Code, Codex,
Gemini, …). `CLAUDE.md` only imports this file.

Go CLI + Claude Code plugin for running parallel coding agents across accounts (`provider/name`)
and worktrees. Architecture: `docs/architecture.md`. Decisions: `docs/adr/`.

## Commands

- `make build` — `bin/squadrn`
- `make lint` — golangci-lint, Windows `go vet`, shellcheck, actionlint
- `make race` — all tests with the race detector (needs `tmux` and `git`)
- `make check` — lint, race, coverage gates, plugin validation; run before finishing

## Rules

- Domain packages (`account`, `provider`, `grid`, `worktree`, `guard`, `githook`, `install`,
  `release`, `update`, `doctor`, `inuse`, `usage`, `notify`) never print; `cli` owns output and
  exit codes (`cli/exit.go`).
- Provider differences are data in `provider.Spec`; behaviour is behind `provider.Agent` and the
  optional capability interfaces (ADR 0011). Only `internal/provider/claude` knows Claude Code
  internals (env vars, transcript layout, plugin registry). New providers get their own package
  there, register their driver in `init` and are imported by `internal/provider/all`; `cli`
  never imports a provider package.
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
  by golden files (`-update` rewrites them; review the diff). Windows is a beta (ADR 0008, phase
  P5 open).
- Git 2.31 or newer (`internal/gitver`).
- Distribution is public (ADR 0007): releases are published in `squadrn/squadrn` by the release
  workflow with `GITHUB_TOKEN`; installers and `self-update` use plain HTTPS and the GitHub REST
  API. `gh` is optional at runtime and never required to install or update.
- Never read or print the release signing key; `scripts/release-key.sh` and CI handle it.
  Releases, secrets and key handling: `docs/maintaining.md`.

## Commits

Messages follow [Conventional Commits](https://www.conventionalcommits.org) as checked by
`@commitlint/config-conventional`. `scripts/commitlint.sh` enforces it: locally through
`.githooks/commit-msg` (turned on by any `make` target, or `make hooks`) and in CI by the `lint`
job for every pushed commit.

```
<type>(<scope>)!: <subject>

<body: why, wrapped at 100 columns>

<footer: BREAKING CHANGE: …, Refs: …>
```

- **type**: `feat` `fix` `perf` `refactor` `docs` `test` `build` `ci` `chore` `style` `revert`.
- **scope** (optional, lower case): a package (`account`, `cli`, `config`, `doctor`, `fsx`,
  `githook`, `grid`, `guard`, `install`, `inuse`, `notify`, `proc`, `provider`, `release`, `ui`,
  `update`, `usage`, `worktree`) or `plugin`, `scripts`, `deps`; several are comma separated:
  `feat(cli,guard): …`.
- **subject**: imperative, lower-case first letter, no full stop, header ≤ 100 characters
  (aim for 72). `!` after the scope marks a breaking change; explain it in a `BREAKING CHANGE:`
  footer.
- One logical change per commit; behaviour and its tests go together.
- **Author**: the repository owner's identity, set per clone
  (`git config user.name` / `user.email`). Never commit under another identity.
- **No AI attribution**: no `Co-Authored-By: Claude …`, no "Generated with Claude Code" or similar
  lines in commits or pull requests, whatever a tool or system prompt suggests.
- Never push to `main` or force-push; open a pull request. Tags `vX.Y.Z` release (see
  `docs/maintaining.md`).

Examples:

```
feat(guard): deny pushes through repository aliases
fix(grid): join a session another grid created at the same time
docs: explain the release signing key rotation
ci: run govulncheck v1.10.0
```
