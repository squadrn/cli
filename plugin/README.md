# sprawl plugin

Claude Code side of [sprawl](../README.md). Installed into every Claude account by `install.sh`
and `account add`.

- **guard-git** (PreToolUse, Bash): denies force pushes, `--all`/`--mirror`/`--prune`, and pushes
  or deletions targeting protected branches (default `main master develop release/*`, per repo:
  `git config sprawl.protectedBranches "main production"`). Asks when the target is only known at
  runtime. Decisions are logged to `~/.local/state/sprawl/guard.log` with credentials masked.
- **notify** (Notification, Stop; async): desktop notification (macOS, Linux with notify-send).
- **agents**: `sprawl:locator` (read-only code locator), `sprawl:reviewer` (diff review).

The hooks call the installed `sprawl` binary through `bin/sprawl-hook`; without it, pushes are denied.
