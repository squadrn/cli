# squadrn plugin

Claude Code side of [squadrn](../README.md). Installed into every Claude account by `install.sh`
and `account add`.

- **guard-git** (PreToolUse, Bash): denies force pushes, `--all`/`--mirror`/`--prune`, and pushes
  or deletions targeting protected branches (default `main master develop release/*`, per repo:
  `git config squadrn.protectedBranches "main production"`). Asks when the target is only known at
  runtime. Decisions are logged to `~/.local/state/squadrn/guard.log` with credentials masked.
- **notify** (Notification, Stop; async): desktop notification (macOS, Linux with notify-send).
- **agents**: `squadrn:locator` (read-only code locator), `squadrn:reviewer` (diff review).

The hooks call the installed `squadrn` binary through `bin/squadrn-hook`; without it, pushes are denied.
