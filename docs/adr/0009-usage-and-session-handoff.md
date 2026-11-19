# 0009 — Usage limits and session handoff

## Status

Accepted. U1 ships with this ADR; S1 implemented; S2 is later.

## Context

Subscription accounts stop at their usage limits (a 5-hour session window, a 7-day window, and
model-specific weekly limits). `swap` (0005) already moves a conversation between accounts, but
the user has to notice the limit, guess which account still has room and type the swap. squadrn
needs to know each account's usage, and, later, to hand a stopped session over with less typing.

Facts about Claude Code (Claude Code 2.1.327, official docs read 2026-11-18). **D** = documented,
**I** = internal (observed, may change without notice; isolated in `provider/claude`).

| Fact | |
|---|---|
| The status line command (`settings.json` → `statusLine: {type: "command", command, padding, refreshInterval}`) runs through a shell (Git Bash, or PowerShell without it, on Windows) and receives JSON on stdin: `session_id`, `transcript_path`, `cwd`, `model`, `workspace`, `cost`, … | D |
| `rate_limits.five_hour` / `.seven_day` carry `used_percentage` (0–100) and `resets_at` (Unix seconds); only for claude.ai Pro/Max/Ultra subscribers (or a gateway with a spend limit: `rate_limits.spend_limit.{used_percentage, resets_at, used_usd, limit_usd, period}`), only after the session's first API response; each window may be absent and is dropped once `resets_at` has passed | D |
| The status line re-runs after each assistant message, on `refreshInterval`, and when a window reaches `resets_at`; updates are debounced (300 ms) and a running command is cancelled by a newer update; output shows only on exit 0 | D |
| Hook event `StopFailure` replaces `Stop` when a turn ends on an API error; matcher on `error` (`rate_limit`, `authentication_failed`, `billing_error`, `server_error`, …); input `session_id`, `transcript_path`, `cwd`, `error`, optional `error_details` ("429 Too Many Requests"), optional `last_assistant_message`; no reset time; output ignored | D |
| Interactive sessions show "You've hit your session limit · resets 3:45pm" (weekly/Opus/Sonnet variants, "resets Mon 12:00am") and continue on their own at the reset | D (behaviour), I (exact wording) |
| `claude auth status` JSON: `loggedIn`, `email`, `orgName`, `subscriptionType`, `authMethod`, … | D (command), I (field values) |
| There is no non-interactive `/usage`. An undocumented OAuth usage endpoint exists; squadrn never calls it and never reads credentials, tokens or the keychain | D / policy |
| Conversations live at `<config>/projects/<cwd key>/<session>.jsonl`, checkpoints at `<config>/file-history/<session>/` | I |
| `<config>/sessions/<pid>.json` lists running sessions (`pid`, `sessionId`, `cwd`, `status`, `kind`) | I |

## Decision

### U1 — `squadrn usage` (this change)

- **Package `usage`** (no provider knowledge, never prints): `Snapshot` (windows, spend, last
  observation, last limit hit with session, cwd and reset estimate), `Merge` (the newer
  observation wins per window; an older one fills gaps that have not reset; a recorded limit stays
  until a later observation shows every reported window below 100% or its reset passes),
  `Effective(now)` → `limited | high | ok | reset | stale | nodata` (plus `metered | na |
  loggedout` from the account), `Rank` (drop logged-out, limited, the source, other providers and
  — on request — other organisations; then reset/ok before nodata/stale before high before
  metered; then lower 5h, lower 7d, fresher). Thresholds are constants for now: high at 5h ≥ 80%
  or 7d ≥ 90%, stale after 6 hours.
- **Store**: one file per account, `~/.local/state/squadrn/usage/<provider>-<name>.json` (fixed
  identifiers, `0600`, atomic writes). A status line update is written only when a value changed
  or a minute has passed.
- **Recording**: `squadrn hook statusline [--then <cmd>]` parses the status line input
  (`provider/claude`), identifies the account from `CLAUDE_CONFIG_DIR` (unset → main; an unknown
  dir is not recorded), records, then runs the wrapped command through the same kind of shell with
  the same input and passes its output and exit status through — or prints
  `work · 5h 42% · 7d 18%`. A recording failure never changes what is shown. The plugin's
  `StopFailure` hook (matcher `rate_limit`, async) runs `squadrn hook limit`: it records the limit,
  estimates the reset from "resets 3:45pm" in the last message (local time, next occurrence;
  fallback: the stored 5-hour `resets_at`) and notifies "claude/work hit its usage limit (resets
  15:45) — sqd swap".
- **Setup**: `squadrn usage --setup [account | --all] [--undo] [-y]` wraps `statusLine.command`
  (other fields and keys kept in place). `settings.json` is usually a link to main's, so the change
  is written through the link and covers every account sharing it; the output names them. The
  command is `<installed binary> hook statusline --then '<old>'`, quoted for POSIX shells on Unix;
  on Windows a single form both Git Bash and PowerShell read the same (forward slashes, `~` for the
  home dir, single quotes; commands containing `'` or paths with spaces outside the home dir are
  refused). `account add` asks once at a terminal ("Track usage limits for this account in its
  status line? [Y/n]", `--usage`/`--no-usage`); `self-uninstall` restores; `doctor` shows
  "usage tracking: on/off" per account.
- **Display**: `squadrn usage [account] [--json] [--refresh] [--watch]`: ACCOUNT PLAN ORG 5H RESETS
  7D RESETS STATE SEEN, bars only with colors, footer "best for this repo: <account>" ranked from
  the account most recently limited or active in this directory. PLAN/ORG come from the auth
  cache. The JSON schema is documented in the README.

### S1 — swap by session (implemented)

- Find a conversation by session ID with a glob `<config>/projects/*/<id>.jsonl` instead of
  computing the project key (whose encoding is internal and differs on Windows), and copy
  `file-history/<id>/` with it.
- Quiesce before copying: the session must not be running, judged from squadrn's in-use registry and
  `<config>/sessions/<pid>.json` (internal: treated as a hint, a missing or unreadable file means
  "unknown", which asks).
- `squadrn swap` without arguments picks the session that just hit its limit in the current
  directory (from the usage store) and offers ranked target accounts, showing their usage.
- Every handoff is appended to `~/.local/state/squadrn/handoffs.jsonl` (time, session, from, to,
  cwd).
- Moving to an account of another organisation always asks.
- As built: the transcript moves under the source's project folder name (Claude Code shortens long
  keys with a hash, so the key is never computed for a session ID; the legacy worktree form
  computes it and falls back to the transcripts whose first records name the worktree as `cwd`).
  `<session>/` and `file-history/<session>/` are replaced through a staged copy; the transcript is
  staged, checked (size, SHA-256) and renamed last. A target copy that is not a prefix of the
  source (it went on there) is refused unless `--force`. Candidates without `--session`: the latest
  limit in or below the working directory not moved since (ledger), then registry sessions running
  there; several → picker, none at a terminal → an error listing `--session` values. Targets are
  ranked with `usage.Rank`; logged-out targets are refused; an Opus conversation moving to a Pro
  account warns. Holders: registry entries for the session, and sessions squadrn started as the
  source account in its directory (in-use records in both) unless the registry shows another
  session. Unix sends SIGINT, then — asked again after 10 s — SIGTERM; Windows cannot interrupt a
  process on another console (`GenerateConsoleCtrlEvent` only reaches the caller's console), so it
  uses `TerminateProcess` after confirmation. A holder still alive refuses the swap.

### S2 — supervisor (later)

- A supervisor watches limit events and acts by `swap_policy`: `ask` (default) offers the swap,
  `auto` performs it.
- `auto` never crosses organisations, and sends no continuation prompt unless one is configured.

### Decisions recorded

1. The status line wrapper is offered once at `account add` (asked) and through
   `usage --setup`; it is never installed silently.
2. S2's default policy is `ask`.
3. `auto` never moves a session to another organisation.
4. `auto` sends no continuation prompt by default.

## Consequences

- Usage is only as fresh as the last status line update of some session of that account; idle
  accounts age into `stale`, and API-billed accounts have no windows (`nodata`/`metered`).
- Model-specific weekly limits (Opus, Sonnet) are not in `rate_limits`: such a limit is recorded
  through `StopFailure`, but a later status line update below 100% clears it although that model
  may still be limited.
- The status line now depends on the installed squadrn binary; `self-uninstall` restores it, a
  manual removal of squadrn leaves a status line that prints nothing until `--setup --undo` or an
  edit.
- On Windows, a `settings.json` shared by hard link stops being shared when it is rewritten
  (atomic replace); symlinks and copies behave as on Unix.
- The limit message wording and the `subscriptionType`/`authMethod` values are internal; parsers
  degrade to "no reset estimate" / "not metered".
