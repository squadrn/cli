# 0003 — Distribute through a private GitHub repository and gh

Status: accepted (v2.0.0)

## Context

Squadrn is shared only with invited people. Access must be revocable, installs must not need a Go
toolchain, and downloads must be verifiable.

## Decision

- The repository is private; access is a collaborator invite (read).
- GoReleaser publishes per-platform archives, `checksums.txt` and `install.sh` on `v*` tags.
- `install.sh` and `self-update` download through `gh`, so they reuse the user's GitHub login
  and stop working when access is revoked. Both verify the SHA-256 before running anything.
- Placement logic lives in Go (`self-install`), so the shell script stays small and the
  install is covered by tests.
- The installed release dir doubles as a local plugin marketplace that every account registers.

A private Homebrew tap was rejected: every user would have to configure a token.

## Consequences

- One-line install for invited users; no toolchain, no extra secrets.
- Repository slug is a build-time value (`-X github.com/squadrn/cli/internal/cli.Repo`), set by the release workflow.
- Plugin updates require a version change in `plugin.json`; installs stamp the release version.
