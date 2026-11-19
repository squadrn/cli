# Changelog

## 1.0.0-rc.1

First public release of Squadrn: run Claude Code as several of your own accounts, in parallel git
worktrees, with a guard against destructive pushes. Apache-2.0.

- **Accounts:** `squadrn <account>` runs Claude Code as `provider/name` (`claude/main`,
  `claude/work`, …); `account add|login|logout|ls|rename|rm|adopt`; settings, skills, plugins and MCP
  servers shared with the main account.
- **Parallel work:** `wt <name>` runs an agent in its own worktree; `grid` lays several out in tmux
  (macOS, Linux); `ps`, `gc` (never deletes unmerged or in-use work), `deps`.
- **Usage limits:** `usage` shows each account's 5-hour and weekly usage from Claude Code's status
  line (`usage setup`), with the best account for the current repository.
- **Swap:** `swap` continues a conversation on another of your accounts by session ID, after
  stopping the old session; checkpoints for `/rewind` move with it.
- **Push guard:** a Claude Code hook (Bash and PowerShell) and a git pre-push hook stop force pushes
  and pushes to protected branches (`main`, `master`, `develop`, `release/*` by default, plus
  `git config squadrn.protectedBranches`); `doctor` checks the guard and server-side protection. A
  safety net, not a sandbox.
- **Install and update:** one-line installers (`curl … | sh`, `irm … | iex`) that verify the release
  signature and checksum; `self-update` (stable or beta channel), `--rollback`, `self-uninstall`;
  your own command name and alias (`squadrn`/`srn` by default).
- **Platforms:** macOS and Linux (amd64, arm64); Windows 10 21H2+/11 (amd64, arm64) in beta, without
  `grid`. Git 2.31 or newer.
- Release signing key `squadrn-release-20261119`.
