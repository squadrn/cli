# 0014 — Account creation: guided `account add`, `repair`, `default`

Status: accepted (implemented in #55, #56, #59, #60 and the setup PR; see Implementation notes)

## Context

`srn account add [provider/]name` (ADR 0011 §4) creates an account and, run again, repairs it.
It works, but:

1. The reference is required up front, so a new user must know the `provider/name` syntax and
   which agent CLIs are installed.
2. One command creates, repairs and (through `adopt`) takes over directories; a typo in an
   existing name silently turns a create into a repair.
3. Questions are `[Y/n]` lines: no going back, no review before anything is written.
4. There is no `--dry-run` and no machine-readable result.
5. A name used by two providers (`claude/work`, `codex/work`) is not flagged, and `srn work`
   then fails as ambiguous.
6. There is no default account: every secondary account must be named on each launch.

This ADR redesigns creation from the user's side. Comparable flows reviewed: `gh auth login`,
`gcloud init`, `op account add`, `fly launch`, `create-vite` (clack prompts), and clig.dev.

## Principles

1. Two required answers (provider, name); everything else has a good default.
2. Ask everything, review, then write. Cancelling anywhere changes nothing.
3. Every question has a flag and every flag a question; the wizard prints the equivalent command.
4. Creating and repairing are different intents, so they are different commands.
5. The end state is usable: logged in, guarded, and the start command shown.
6. Without a terminal nothing is asked: missing input is a usage error (exit 2) with the fix.

## Decision

### 1. Commands

```
srn account                         list accounts
srn account add [<ref>]             create; without <ref> at a terminal: the wizard
srn account repair [<ref>|--all]    bring existing accounts up to date
srn account default [<ref>|--unset] show or set the account a bare `srn` runs
srn account login|logout|rename|rm  unchanged
```

- `add` on an existing account is an error: `codex/work already exists (repair: srn account
  repair codex/work)`; the wizard offers "Repair it instead". The installer, `self-update` and
  `doctor` fixes use `repair`.
- `adopt` is folded into `add`: an unmanaged `~/.<provider>-<name>` dir is taken over with
  `add --adopt` (asked in the wizard). `account adopt` is removed, with no alias.
- `account default` stores `default_account` in the config. A bare `srn` (and `srn <subcommand>`
  passthrough without an account) runs it instead of the default provider's main account.
  `--unset` clears it. `doctor` warns when it names an account that no longer exists.

These are breaking changes for `add` on an existing account and for `adopt`, made before 1.0.0.

### 2. `account add` flags

| Flag | Meaning |
|---|---|
| `<ref>` | `[provider/]name` |
| `-p, --provider <id>` | `claude`, `codex`, `gemini`; must agree with a provider in `<ref>` |
| `--profile <name>` | `minimal`, `standard`, `strict` (default: config profile, else `standard`) |
| `--with <opt>[,<opt>]` | `privacy`, `no-attribution`; repeatable or comma separated |
| `--usage`, `--no-usage` | usage tracking (providers with a status line) |
| `--login`, `--no-login` | log in at the end (terminal default: asked, Enter = yes; otherwise no) |
| `--default` | make it the default account |
| `--adopt` | take over an existing unmanaged dir |
| `--dry-run` | print the plan and change nothing |
| `--json` | print the result as JSON on stdout; implies `--yes` |
| `-y, --yes` | ask nothing: defaults and flags only |

`--dry-run` output:

```
plan: codex/work → ~/.codex-work (new)
  profile   standard   workspace-write sandbox, no network, asks before leaving it
  shared    AGENTS.md, skills ← codex/main
  settings  copy of codex/main + standard (follows main)
  guard     pre-tool hook + git pre-push
  login     no (--login to log in)
nothing was changed
```

`--json` result (one object; errors keep the exit codes of `cli/exit.go` and print
`{"error": "...", "fix": "..."}`):

```json
{"account":"codex/work","provider":"codex","dir":"/Users/me/.codex-work","created":true,
 "adopted":false,"profile":"standard","options":[],"guard":"installed","usage":false,
 "loggedIn":false,"default":false,"start":"srn codex/work"}
```

### 3. The wizard

Runs for `srn account add` without `<ref>` when stdin and stdout are terminals that understand
ANSI. Flags given on the command line pre-answer their questions (they are skipped).

| # | Question | Kind | Required | Asked when | Default |
|---|---|---|---|---|---|
| 1 | Provider | select | yes | two or more CLIs on PATH | `default_provider` |
| 2 | Account name | text, validated per key | yes | always | empty, placeholder `e.g. work` |
| 2a | "codex/work already exists" | select: Repair it · Choose another name | | name exists | Choose another |
| 2b | "~/.codex-work exists, not managed" | select: Adopt it · Choose another name | | unmanaged dir | Choose another |
| 2c | "main is the default account" | select: Set up codex/main · Choose another name | | name `main` | Set up |
| 3 | Setup | select: Recommended · Customize | yes | always | Recommended |
| 4 | Profile | select, provider summaries | | Customize | `standard` |
| 5 | Options | multiselect | | Customize, provider has options | none |
| 6 | Track usage limits | confirm | | Customize, provider has a status line | yes |
| 7 | Make it the default account | confirm | | Customize | no (yes for the provider's first account) |
| 8 | Review, then "Create codex/work?" | confirm | yes | always | yes |
| 9 | Log in now | confirm | | not logged in | yes |

- **Name before login.** Suggesting a name from the logged-in email would read better, but
  Claude Code keys its Keychain item to `CLAUDE_CONFIG_DIR`: logging in inside a temp dir and
  renaming it loses the login. The name must exist first.
- **Login after Review.** The account is complete before the login; an aborted login leaves a
  valid account and `srn account login` to finish.
- **Name validation** per key with `account.ValidateName`; the hint line shows the dir and the
  start command (`→ ~/.codex-work · start with: srn work`). A name another provider already uses
  warns (yellow, not blocking): `"work" is also claude/work: start it with srn codex/work`, and
  the start command shown is the qualified one.
- **Providers not installed** are listed dimmed and cannot be chosen, with their install hint.
  Each installed provider shows its version and account count (`3 accounts` / `new`).

Screens:

```
┌  New account
│
◇  Provider ·································· Codex CLI 0.171.0
│
◆  Account name
│  work▌
│  → ~/.codex-work · start with: srn work
│
◇  Setup ····································· Recommended
│
◆  Review
│  account   codex/work → ~/.codex-work (private)
│  profile   standard · workspace-write sandbox, no network
│  shared    AGENTS.md, skills ← codex/main
│  settings  copy of codex/main, follows its changes
│  guard     pre-tool hook + git pre-push
│
│  Create codex/work?  ● Yes  ○ No
│
◇  Created codex/work
│  ✓ ~/.codex-work (private)
│  ✓ settings: codex/main + standard
│  ✓ push guard
│
◆  Log in now?  ● Yes  ○ No
│  ↗ codex login …
│  ✓ logged in as you@example.com · Pro
│
└  Ready in 3.2s · start: srn codex/work
   Same without questions: srn account add codex/work --login
```

Cancelled: `■  Cancelled · nothing changed` (exit 130). Cancelled login:
`▲  not logged in · srn account login codex/work` (exit 0).

### 4. Interface conventions

- Glyphs `┌ │ ◆ ◇ ▲ ■ └`, `● ○`, `◻ ◼`; ASCII fallback `+ | > * ! x \ ( ) [ ]` when the
  locale is not UTF-8, `TERM=dumb`, or a Windows console whose output code page is not 65001.
- Answered questions collapse to one line with a dotted leader to the answer.
- Keys: `↑ ↓` / `j k` move, `Space` toggles, `Enter` confirms, `y` / `n` answer a confirm,
  `Esc` goes back one question (cancels on the first), `Ctrl-C` cancels, `Ctrl-U` clears text.
  A dim key hint sits under the active question.
- Colour: cyan active, dim hints and answers, red errors, yellow warnings, green `✓`; never
  colour alone. `NO_COLOR` and `--no-color` apply.
- Streams: the frame and prompts go to stderr; stdout carries only `--json` or the plain result
  lines without a terminal.
- A terminal without ANSI gets line prompts in the same order (today's `askYes` style).
- The screen is measured before every draw; long lines are cut with `…`.

### 5. Architecture

```
internal/ui/prompt/   Prompter and components; each component is a pure model (update/view)
  select, multiselect, text, confirm, frame, glyphs; term (raw mode), lines (no ANSI), script (tests)
internal/cli/
  planAdd(input, opts) (addPlan, error)        reads only: --dry-run, Review, --json
  applyAdd(plan, reporter) (addResult, error)   the only writer: temp dir, then rename
  wizard(prompter) (input, opts, error)         step machine; Esc = back
  planRepair / applyRepair                      account repair, same provisioner
```

- One write path: the wizard, the flag path, `--json` and `setup` all run `planAdd` then
  `applyAdd`.
- `reporter` is an interface with three renderers: frame, plain lines, JSON. Logic never
  knows where it prints.
- The wizard is a slice of steps with `skip(state)` and `run(state)`; `ErrBack` returns to the
  nearest step not skipped; answers stay in the state, so going back shows them.
- CLI versions are read in parallel behind a spinner (target under one second; Gemini runs on
  Node). Existing accounts' login state comes from `AuthCache`.
- No new dependency: `golang.org/x/term` and the key decoding and screen code of the current
  picker, which becomes `prompt.Select`.

### 6. Edge cases

| Case | Behaviour |
|---|---|
| No agent CLI on PATH | `▲ No agent CLI found` with each provider's install command, exit 1 |
| `add` without `<ref>` and no terminal | usage error, exit 2 |
| `add` of an existing account, no terminal | exit 1, fix: `account repair` |
| unmanaged dir, no terminal, no `--adopt` | exit 1, fix: `--adopt` |
| login fails or is cancelled | account kept, `▲` with `account login`, exit 0 |
| Ctrl-C while applying | temp dir removed, exit 130 |
| EOF on stdin | cancel |
| run inside an agent session (no TTY) | the no-terminal path: never hangs |

### 7. Not done, and why

| Idea | Decision |
|---|---|
| Choosing the login method in the wizard | No: each CLI has its own chooser and its flags change across versions |
| Per-account MCP selection | No: Review shows the servers `shared_mcp` copies; per-account choice needs new state and sync |
| Per-account colour or emoji (pane borders, notifications) | Later, in its own ADR (grid and notify feature) |
| "Start a session now?" at the end | No: the start command is printed; starting mixes two contexts |

## Testing

- Components: key sequences → state and `view()` snapshots, UTF-8 and ASCII, no colour.
- Wizard with `prompt.Script`: happy path per provider, Customize, `Esc` at every step, cancel
  at Review leaves the tree unchanged, branches 2a/2b/2c, cross-provider name warning.
- `--dry-run` changes nothing (tree compared before and after); `--json` against its schema.
- Script tests: `add` on an existing account fails, `repair`, `--adopt`, `account default`,
  `adopt` gone; existing goldens of the flag path stay unchanged except where the commands
  change.
- E2E through a pty (`testSetupTTY` infrastructure): happy path and cancel.
- Windows: added to the manual checklist in `docs/maintaining.md`.

## Plan

1. This ADR.
2. `internal/ui/prompt` (components, fallbacks); the picker moves onto `Select`.
3. `planAdd` / `applyAdd` / reporter; `--dry-run`, `--json`.
4. Commands: `repair`, `add --adopt` (and `adopt` removed), `account default`; installer,
   `self-update` and `doctor` use `repair`.
5. The wizard.
6. `srn setup` runs the wizard ("Add another account?"); remaining questions use the Prompter.

## Consequences

- `add` stops being idempotent; scripts that re-run it must call `repair`. `adopt` is gone.
- A bare `srn` may run a secondary account when `default_account` is set.
- New package `internal/ui/prompt`; the picker becomes one of its components.
- Every creation path, interactive or not, goes through one plan and one apply.

## Implementation notes

Where the implementation differs from the decision above, and why:

- `add <provider>/main` keeps setting up the default account (push guard, usage, login) rather
  than failing as an existing account: the CLI creates `~/.<provider>` itself, so "set it up" is
  the only meaning `add` can have for it, and branch 2c of the wizard offers exactly that.
  `repair <provider>/main` does the same.
- With `default_account` set, a bare `srn` at a terminal runs that account instead of opening the
  account picker; without one the picker stays. `wt`, `grid` and `usage` without an account keep
  using the default provider's main account.
- `--relink` moved from `add` to `repair` (only an existing account has copies to relink).
- The terminal code uses `github.com/charmbracelet/x/term` (already a dependency, which the picker
  used) rather than `golang.org/x/term`; still no new dependency.
- "Ready in Ns" counts from the review's Yes: the time squadrn spent applying and verifying,
  without the questions and the login.
- In the wizard, Recommended applies every default of the Customize questions, including "Make it
  the default account" for the provider's first account.
- Adopting (2b), repairing (2a) and setting up main (2c) skip the Setup questions: an adopted dir
  keeps its own settings (no profile unless `--profile`), a repair keeps the account's.
- A question asked on its own (outside the wizard) has nowhere to go back to, so Esc cancels it.
- `--dry-run` in the wizard stops after the review and prints the command without questions.
- The `--json` result also has `dryRun: true` for a dry run; errors carry `fix` only when there is a
  command that fixes them.
- `setup` asks "Add an account?" (default yes when there is no account besides main), runs the
  wizard, then asks "Add another account?" (default no). A cancelled wizard adds nothing and setup
  goes on. The account picker's "+ add a new account" runs the wizard and starts the new account.
