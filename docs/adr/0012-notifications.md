# 0012 — Notifications

Status: accepted (v1.0.0)

## Context

Up to v1.0.0-rc.3 the plugin's `Notification` hook (every type) and `Stop` hook (every turn) ran
`squadrn hook notify 'Needs input'|'Done'`, which showed *"Claude: Done"* with the directory
name through `osascript` (macOS) or `notify-send` (Linux), and the `StopFailure` hook (matcher
`rate_limit`) notified usage limits. There was no dedupe, throttle, focus check, quiet hours or
setting; every turn of every session notified, even while the user was typing in it; on macOS the
notification came from Script Editor, which needs its own permission and does nothing on click;
Windows showed nothing, and the agent CLIs' own notifications (OSC sequences in iTerm2, Ghostty,
kitty) came on top.

Facts (Claude Code hooks reference, checked against Claude Code 2.1.329; **D** = documented):

| Fact | |
|---|---|
| Every hook gets `session_id`, `prompt_id`, `transcript_path`, `cwd`, `permission_mode`, `hook_event_name` | D |
| `Notification`: `message`, optional `title`, `notification_type` ∈ `permission_prompt`, `idle_prompt`, `auth_success`, `elicitation_dialog`, `elicitation_url_dialog`, `elicitation_complete`, `elicitation_response`, `agent_needs_input`, `agent_completed`, `quota_auto_resume_*`, `plugin_notification`, …; the matcher filters on it. Permission, idle and elicitation dialogs already fire only when the user appears away | D |
| `Stop`: `stop_hook_active`, `last_assistant_message`, `background_tasks[]`, `session_crons[]`; no matcher | D |
| `StopFailure`: `error` ∈ `rate_limit`, `authentication_failed`, `oauth_org_not_allowed`, `account_on_hold`, `billing_error`, `invalid_request`, `model_not_found`, `server_error`, `max_output_tokens`, `cloud_credential_error`, `unknown`; `error_details`; `last_assistant_message` | D |
| A sync hook may answer `{"terminalSequence": "…"}` (OSC 9/99/777, BEL), written to the terminal by Claude Code in interactive sessions; async hooks cannot | D |
| `preferredNotifChannel: "notifications_disabled"` in `settings.json` turns Claude Code's own terminal notifications off | D |

## Decision

**Event model** (package `notify`, no printing, no delivery). Kinds by priority: `input` (needs
you) > `failed` > `done` > `info`. Each provider maps its hook input through the optional
`provider.HookEvents` capability (ADR 0011). Claude Code: `Notification` `permission_prompt`,
`idle_prompt`, `elicitation_dialog`, `elicitation_url_dialog` → input; `auth_success`,
`quota_auto_resume_*`, `plugin_notification` → info; any other type → nothing; `StopFailure` →
failed with the error as reason; `Stop` → done unless `stop_hook_active` or background tasks or
session crons remain. The plugin's `Notification` matcher names exactly the mapped types and
`StopFailure` has no matcher.

**Content.** Title `<agent> · <account> · <repo>/<worktree>` (the grid pane name instead of
the worktree inside a squadrn grid; the worktree from `cwd` under any provider's worktree dir,
the repository from the nearest `.git`, no git call). Body by kind: *Needs approval: Bash* (tool
from the message when it names one), *Waiting for your reply*, *Needs your input*, *Done*,
*Usage limit · resets 15:45 · srn swap*, *Login expired · srn account login claude/work*,
*Billing problem · check the account's plan*, *Stopped: server error*. Commands follow the
user's command name. The agent's own text (prompt, reply) appears only with `notify.preview`.

**Policy** (`notify.Decide`, table-tested). In order: not a notification; off (`enabled =
false` or `method = "off"`); kind not in `events` (default input, failed, done); quiet hours
(local time, may wrap midnight). Then per session, from `state/notify.json` (atomic, locked,
pruned after 24 h): an idle prompt right after the same turn's *Done* is dropped; the same kind,
reason and turn twice is dropped; anything but `input` within 30 s of the session's last
notification is throttled. With `when = "unfocused"` (default): nothing is shown while the
session's tmux pane is the active pane of the active window of an attached session and a client
has focus (or, without focus reporting, while the terminal app is frontmost); outside tmux on
macOS, `done` and `info` are dropped while the terminal app running the session
(`__CFBundleIdentifier`, else `TERM_PROGRAM`) is frontmost (`lsappinfo front`). When focus is
unknown nothing is dropped. OS Do Not Disturb / Focus is respected because the OS applies it;
squadrn never overrides it. Sound for `input` and `failed` (`sound = "input"`), `all` or `none`.

**Delivery.** The hook stays async and fast: it decides, then hands the notification to a
detached `squadrn hook deliver <payload>` and returns. Every outcome (sent, skipped and why,
failed) is logged in `state/notify.log` (titles only, rotated at 128 KiB).

| OS | Notifier |
|---|---|
| macOS | `terminal-notifier` when on PATH (`-group` per session so a newer one replaces the older, `-activate` the terminal's bundle id, `-sound default` when it sounds), else `osascript` with text passed as arguments, never as script source (`notify status` and `doctor` explain the Script Editor permission) |
| Linux | `notify-send -a Squadrn`, urgency critical for `input`, `--print-id`/`--replace-id` so a session's newer notification replaces the older (libnotify < 0.7.9 falls back to plain), else `gdbus` calling `org.freedesktop.Notifications.Notify` with escaped string literals |
| Windows | Windows PowerShell 5.1 (`powershell.exe`, which can load WinRT) shows a `ToastGeneric` toast through `ToastNotificationManager.CreateToastNotifier("Squadrn")`; the text comes from environment variables into XML text nodes; tag and group replace per session. `self-install` registers the AppUserModelID under `HKCU\Software\Classes\AppUserModelId\Squadrn` (`DisplayName`); `self-uninstall` removes it |

**Settings** (`[notify]` in `config.toml`, `config get|set notify.<key>`): `enabled`, `events`,
`sound`, `when`, `quiet_hours`, `preview`, `method` (`auto`, `terminal`, `desktop`, `tmux`,
`off`). Invalid values are errors like any setting.

**Terminal channel.** The Notification and StopFailure hooks run synchronously (`hook notify
--terminal`, 5 s timeout) so they can answer `{"terminalSequence": "…"}`: OSC 9 for iTerm2 and
WezTerm, OSC 777 for Ghostty and Warp, OSC 99 for kitty (from `TERM_PROGRAM`, `KITTY_WINDOW_ID`),
a BEL after it when the kind sounds; control characters are dropped from the text. With method
`auto` the sequence replaces the desktop notification outside tmux only (inside, tmux would need
`allow-passthrough`). Stop and the working hooks stay async and use the desktop. Windows hooks stay
async (no terminal channel there yet).

**Agent state and tmux.** UserPromptSubmit and PostToolUse (async) mark the session `working`;
the kinds above mark it `input`, `failed` or `done`. The state of each session (account, cwd, tmux
pane and socket, terminal bundle id) is kept in `state/notify.json` (written only on change,
pruned after 24 h) and set as the pane option `@squadrn_state`; a grid's `pane-border-format`
colours it. `input` and `failed` ring the bell on the pane's tty (tmux flags the window). `srn
ps` lists every running agent: grid panes, panes that reported a state, and sessions in the in-use
registry (now with their cwd), so a plain-terminal session shows too. `srn next` jumps to the
first pane in `input`, else `failed` (switches the client inside tmux, attaches outside). The
desktop notification's click action (`terminal-notifier -execute`) runs the hidden `srn focus
<session>`: select the recorded pane, switch the most recently active client to it, then `open -b`
the terminal app. Session ids are accepted only as plain tokens.

**Agent-native notifications.** While squadrn's notifications are on, every account profile
sets Claude Code's `preferredNotifChannel` to `notifications_disabled` (a profile key: the
user's own value in an account wins, ADR 0011). Main's settings are the user's and are not
changed.

**Commands.** `srn notify status` (settings, the notifier found and its caveats, focus sources,
the last five outcomes), `srn notify test [input|failed|done]` (sends one through the same
notifier, ignoring the policy), and a `doctor` line (notifier found, or what to install).

**Compatibility.** rc.3 plugins still run `hook notify '<title>'` and `hook limit`; both now go
through the same path (the title argument is ignored, the event comes from the input).

## Consequences

- One notification per thing that needs the user, not one per turn; no notification while the
  user looks at the session; no doubles from the agent CLI.
- `terminal-notifier` is recommended on macOS, not required; a signed helper app (own icon,
  click actions without Homebrew) is left for later.
- The ADR 0008 row "Desktop notifications: no-op" on Windows is superseded.
- Two hook runs more per turn (prompt, each tool): async, and they write state only when it
  changes.
- `next` and `focus` are reserved account names.
- Planned in the same model: Codex (`notify` program and `PermissionRequest` hook).
