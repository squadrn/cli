# Security policy

## Reporting a vulnerability

Please report vulnerabilities privately through GitHub:
[Report a vulnerability](https://github.com/squadrn/squadrn/security/advisories/new) (Security →
Advisories). Do not open a public issue.

Include what you found, how to reproduce it, the squadrn version (`srn version`), the OS and
the agent CLI version. You get an answer within a week; a fix and an advisory follow once the
issue is confirmed.

## In scope

- The push guard (ADR 0006): a command that pushes to a protected branch, force-pushes or changes
  the guard's policy while the guard allows it.
- Release integrity: anything that makes `install.sh`, `install.ps1` or `self-update` install a
  release not signed by a key in `internal/release/signers.pub`.
- Files squadrn writes with weaker permissions than documented, or credentials that reach logs.

The guard is a safety net against an agent's mistakes, not a sandbox: a process that can run
arbitrary code as you can bypass it outside the commands it sees. That alone is not a
vulnerability; branch protection on the server is the backstop.

## Supported versions

The latest release on the stable channel, and the latest pre-release on the beta channel.
