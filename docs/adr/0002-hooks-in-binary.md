# 0002 — Hooks run inside the squadrn binary, behind a fail-closed shim

Status: accepted (v1.0.0)

## Context

The Claude Code plugin needs a PreToolUse guard and notifications. Shipping them as separate
scripts lets the guard's policy drift from the CLI's version, and a hook that errors is treated by
Claude Code as non-blocking — the tool call goes through.

## Decision

Hooks are `squadrn hook guard-git` and `squadrn hook notify`. The plugin ships only `bin/squadrn-hook`,
which execs the binary at its fixed install path (else `squadrn` on PATH), independent of the user's
command name. If squadrn is missing, the shim denies any command mentioning `push`. Inside squadrn, every internal failure (panic, unreadable input) becomes an `ask`
decision, never an error exit.

## Consequences

- Policy and CLI always share one version; `self-update` updates both.
- The guard cannot silently disappear: missing binary → pushes blocked; crash → user asked.
- Hooks start a process per Bash call (~15 ms), acceptable next to tool execution.
