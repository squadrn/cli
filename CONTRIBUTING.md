# Contributing

Thanks for helping. Issues and pull requests are welcome; for larger changes open an issue first
so the design can be agreed (decisions live in [docs/adr](docs/adr)).

## Setup

Go (version in `go.mod`), `git` 2.31+, `tmux`, `golangci-lint` v2, `shellcheck`, `actionlint`.

```sh
make hooks      # commit-msg check; any make target also turns it on
make check      # lint, race tests, coverage gates, plugin validation; run before every push
```

Read [AGENTS.md](AGENTS.md) (the rules every change follows, for people and coding agents alike)
and [docs/architecture.md](docs/architecture.md).

## Changes

- Behaviour changes come with tests: unit tests for logic, `testdata/script/*.txtar` for
  commands. Tests never touch real accounts, the network or your tmux server.
- macOS and Linux behaviour is pinned by golden files; regenerate with `-update` and review every
  changed line.
- New push forms the guard must judge go into `TestDecide`.
- Windows code lives in `_windows.go` files; CI runs the Windows tests.

## Commits and pull requests

- [Conventional Commits](https://www.conventionalcommits.org): `<type>(<scope>): <subject>`,
  lower-case subject, header ≤ 100 characters, body explaining why (see AGENTS.md for types and
  scopes). `scripts/commitlint.sh` checks them locally and in CI.
- One logical change per commit, with its tests.
- No AI attribution: no `Co-Authored-By` lines for AI tools and no "Generated with …" lines, in
  commits or pull request descriptions.
- Pull requests target `main`; never force-push a branch under review.

Releases, signing keys and repository settings: [docs/maintaining.md](docs/maintaining.md).

By contributing you agree that your contribution is licensed under the
[Apache License 2.0](LICENSE).
