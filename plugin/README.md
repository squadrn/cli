# squadrn plugin

Claude Code side of [squadrn](../README.md). Installed into every Claude account by the installer
(`install.sh`, `install.ps1`) and `account add`.

- **guard-git** (PreToolUse, matcher `Bash|PowerShell`): denies force pushes,
  `--all`/`--mirror`/`--prune`, and pushes or deletions targeting protected branches (default
  `main master develop release/*`, per repo: `git config squadrn.protectedBranches "main
  production"`). Asks when the target is only known at runtime, and on other git commands that
  could push without saying so (aliases, unknown subcommands, settings that run programs).
  Decisions are logged to `~/.local/state/squadrn/guard.log` with credentials masked.
- **notify** (Notification, Stop; async): desktop notification (macOS, Linux with notify-send).
- **limit** (StopFailure, matcher `rate_limit`; async): records that the account hit its usage
  limit and notifies with the estimated reset, for `squadrn usage` and `squadrn swap`.
- **agents**: `squadrn:locator` (read-only code locator), `squadrn:reviewer` (diff review).

On macOS and Linux the hooks call the installed `squadrn` binary through `bin/squadrn-hook`;
without it, pushes are denied. On Windows the installed plugin uses its own `hooks/hooks.json`,
which runs `squadrn.exe` through `cmd.exe` and blocks the tool call when the binary cannot start.
