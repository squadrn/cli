# Sprawling Agents

Go CLI + Claude Code plugin for running parallel coding agents across accounts (`provider/name`)
and worktrees. Architecture: `docs/architecture.md`. Decisions: `docs/adr/`.

## Commands

- `make build` — `bin/sprawl`
- `make lint` — golangci-lint, shellcheck, actionlint
- `make race` — all tests with the race detector (needs `tmux` and `git`)
- `make check` — lint, race, coverage gates, plugin validation; run before finishing

## Rules

- Domain packages (`account`, `provider`, `grid`, `worktree`, `guard`, `githook`, `install`,
  `release`, `update`, `doctor`) never print; `cli` owns output and exit codes (`cli/exit.go`).
- Provider differences are data in `provider.Spec`; behaviour is behind `provider.Agent`. Only
  `internal/provider/claude` knows Claude Code internals (env vars, transcript layout, plugin
  registry). New providers get their own package there.
- External programs run through `internal/proc` (deadline kills the process group); files are
  written through `internal/fsx` (atomic).
- The guard fails closed: layer 1 asks, layer 2 denies, when anything is uncertain (ADR 0006).
- Fixed identifiers never follow the user's command name: config/data/state dirs, `SPRAWL_*`,
  plugin `sprawl@sprawl`, git key `sprawl.protectedBranches`, tmux options `@sprawl_*`. User-facing
  text does (`app.cmd`).
- Behaviour changes need a test: unit tests for logic, `testdata/script/*.txtar` for commands.
  Scripts run with a fake `claude`, a throwaway HOME and a private tmux server — never real accounts.
- Coverage gates (`make cover-gates`, CI): `guard` ≥ 95% and the others listed in the Makefile.
  New push forms go into `TestDecide`.
- Linux and macOS only; no Windows code paths.
- Never read or print the release signing key; `scripts/release-key.sh` and CI handle it.
- Previous product names live in `config.PreviousNames`; every legacy identifier derives from it.
