# 0011 — Provider model v2, per-account settings, profiles, account flow and setup

Status: accepted (v1.0.0-rc.3)

## Context

Up to v1.0.0-rc.2 Squadrn drove one agent CLI, Claude Code. Every secondary account linked
main's `settings.json`, so all accounts shared one settings file: a `/model` in one account
changed every account, a usage-tracking status line written for one account was written for all,
and a provider with a different settings format (Codex: TOML) could not follow the same model.
`cli` imported `provider/claude` directly for the status line and the hooks, and the reserved
account names included every Claude Code subcommand, a list that would grow with each provider.

rc.3 adds a second provider (OpenAI Codex CLI, `codex`) and reworks how accounts are created.
This ADR records the model both build on. Copilot, OpenCode and others come later on the same
interfaces.

Facts checked against the official docs on 2026-11-20 (Claude Code 2.1.329,
code.claude.com/docs; Codex CLI 0.171.0, learn.chatgpt.com/docs). **D** = documented, **I** =
internal (observed, may change; isolated in the provider's package). A key that could not be
verified is not used.

| Fact | |
|---|---|
| Claude: `CLAUDE_CONFIG_DIR` holds settings, history, plugins and `.credentials.json`; the macOS Keychain item is keyed to it | D |
| Claude: permission rules `Read(path)`, `Edit(path)`, `Bash(cmd *)` in `permissions.deny/ask/allow`; `~/` is home, `./` the working dir, `//` absolute; deny beats ask beats allow in any scope | D |
| Claude: `Edit(...)` rules cover Write, NotebookEdit and MultiEdit; a `Write(...)` rule never matches | D |
| Claude: Read deny rules also stop the Bash file commands Claude Code recognises (`cat`, `head`, …), not arbitrary programs; with the sandbox on they become OS-level `sandbox.filesystem.denyRead` | D |
| Claude: `enableAllProjectMcpServers`, `cleanupPeriodDays` (default 30), `permissions.defaultMode`, `permissions.disableBypassPermissionsMode: "disable"`, `permissions.blockReadsOutsideWorkingDirectories` (2.1.257+), `sandbox.enabled`, `sandbox.allowUnsandboxedCommands`, `sandbox.failIfUnavailable`, `attribution.commit` / `attribution.pr`, `theme`, `statusLine`, `env` | D |
| Claude: sandboxing runs on macOS, Linux and WSL2, not native Windows | D |
| Claude: hook event `ConfigChange` (matchers `user_settings`, `project_settings`, `local_settings`, `policy_settings`, `skills`) runs on settings-file edits | D |
| Claude: `DISABLE_TELEMETRY`, `DISABLE_ERROR_REPORTING`, `CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC` (any non-empty value) | D |
| Claude: `<config>/.claude.json` holds account state, MCP servers, per-project trust; onboarding is recorded there (`hasCompletedOnboarding`, `lastOnboardingVersion`) | D (file), I (keys) |
| Codex: `CODEX_HOME` (default `~/.codex`) holds `config.toml`, `auth.json` (file store), `history.jsonl`, `rules/*.rules`, `hooks.json` | D |
| Codex: `cli_auth_credentials_store = "file" \| "keyring" \| "auto" \| "ephemeral"`; `file` keeps the login in `$CODEX_HOME/auth.json` | D |
| Codex: `approval_policy` (`on-request`, `never`, granular), `sandbox_mode` (`read-only`, `workspace-write`, `danger-full-access`), `[sandbox_workspace_write] network_access`, `[shell_environment_policy] inherit = "all" \| "core" \| "none"`, `exclude`, `ignore_default_excludes` (KEY/SECRET/TOKEN patterns) | D |
| Codex: `rules/*.rules` with `prefix_rule(pattern = [...], decision = "allow" \| "prompt" \| "forbidden")` (experimental) | D |
| Codex: `hooks.json` / `[hooks]` with `PreToolUse`; a hook denies with `hookSpecificOutput.permissionDecision = "deny"` or exit 2; non-managed hooks run only once trusted (hash-based, `/hooks`); managed hooks (`requirements.toml`) are trusted by policy | D |
| Codex: `codex login`, `codex login status` (exit 0 when logged in), `codex logout`, `codex resume <id>`, `codex app-server` with `account/rateLimits/read` (`primary`/`secondary`: `usedPercent`, `windowDurationMins`, `resetsAt`) | D |
| Codex: where sessions are stored and where hook trust is recorded | not documented: Codex internals stay in `provider/codex` |

## Decision

### 1. Providers: a registry of specs and capabilities

A provider is **data** (`provider.Spec`: id, product name, binary, dir layout, config env var,
shared items, settings file, worktree dir, session ID form, install hint, `Caps`; one variable
per provider in package `provider`, e.g. `provider.Claude`) plus **code** behind small
capability interfaces, which the provider's own package registers as a driver:

```go
// package provider
type Driver struct {
	// New finds the CLI and returns its driver; extraEnv is added to every invocation
	// (the git-level guard's GIT_CONFIG_* entries).
	New func(extraEnv []string) (Agent, error)
	// StatusLine is nil when the provider reports no usage through a status line. It needs
	// no CLI binary: the hooks use it on every refresh.
	StatusLine StatusLine
}

func Register(id string, d Driver) // from the provider package's init; id must be a Spec
func DriverOf(id string) (Driver, bool)
func All() []Spec
func Get(id string) (Spec, bool)
```

`internal/provider/all` imports every provider package for its side effect; `cli` imports
`provider/all` and never a provider package. Tests may import a provider package directly.

Every driver implements `Agent`; everything else is optional and found with a type assertion
(`ag.(provider.SessionStore)`), so a provider implements only what it has:

| Interface | Methods | Claude | Codex |
|---|---|---|---|
| `Launcher` | `Output`, `Exec`, `Run` | ✓ | ✓ |
| `Authenticator` | `Auth` (normalised `AuthStatus`), `LoginArgs`, `LogoutArgs` | ✓ | ✓ |
| `Agent` | `Launcher` + `Authenticator` | ✓ | ✓ |
| `Worktrees` | `WorktreeArgs` | ✓ | — |
| `SessionStore` | `ResumeArgs`, `FindSession`, `LatestSessionIn`, `RunningSessions`, `SessionCwd`, `TransferSession`, `UnavailableModel` | ✓ | — (section 6) |
| `GuardInstaller` | `InstallPlugin`, `UpdatePlugin`, `RemovePlugin`, `PluginState`, `EnablePluginArgs` (the layer-1 hook per CLI; layer 2, the git pre-push hook, is universal) | ✓ (plugin) | ✓ (hooks) |
| `MCP` | `HasMCP`, `AddMCP` | ✓ | ✓ |
| `MCPLister` | `ListMCP`: main's servers when they are not in a JSON file (`Spec.MCPConfig` "") | — | ✓ (`codex mcp list --json`) |
| `CredentialStore` | `CheckCredentials(configDir)`: the login stays in the account dir (doctor) | — | ✓ |
| `Relocator` | `Relocate(oldDir, newDir)` | ✓ | ✓ (hook trust keys) |
| `Versioner` | `Version(ctx)` (account add's plan line) | ✓ | ✓ |
| `SettingsStore` | settings format, profile and onboarding seed (section 2) | ✓ JSON | ✓ TOML |
| `StatusLine` (driver field) | `Command`, `SetCommand`, `Wrap`, `Unwrap`, `Shell`, `Parse`, `ParseLimit` | ✓ | — |
| `RateLimits` | `RateLimits(ctx, configDir) (usage.Snapshot, error)`: usage on request | — | ✓ (`app-server`) |

`Caps` stays as data for what is decided without the CLI (worktree flag, swap, MCP sharing,
usage reporting). Swap only moves conversations between accounts of one provider.

Isolation is data: `Spec.ConfigEnv` (`CLAUDE_CONFIG_DIR`, `CODEX_HOME`), `Spec.MainDir` and
`Spec.DirPrefix` (`~/.claude` + `~/.claude-<name>`, `~/.codex` + `~/.codex-<name>`), the
`.squadrn-account` marker inside. The main account runs with the variable unset (Claude keys its
credential store to it). Each provider names its credential-store policy in its profile (Codex:
`cli_auth_credentials_store = "file"`, so each account's login stays in its own dir).

**Reserved account names are Squadrn's own commands only** (`account.Commands`). A provider
subcommand is not reserved: `srn <account> <subcommand>` passes it through, and a bare
`srn <subcommand>` still runs it for the main account unless an account has that name (accounts
are matched before agent subcommands). A word is passed through only when it is a subcommand of
the account's own CLI (the default provider's without an account): `srn work resume <id>` for a
Codex account, while a bare `srn resume` is not a Claude subcommand and is rejected.

### 2. Settings: per-account copies, merged

Secondary accounts get their **own** settings file, built from main's settings, the account's
profile and Squadrn's own keys. Links to main's settings file are no longer made.

- **Sources**, lowest to highest: main's settings → the profile → Squadrn-managed keys (the usage
  status line). Objects listed by the provider as *deep* (Claude: `permissions`, `sandbox`,
  `env`, `hooks`, `attribution`) merge one level down; arrays inside them are the union of the
  sources, in order, without duplicates (main's deny rules plus the profile's). Everything else
  is replaced whole.
- **Record**: `<account>/.squadrn-managed.json` (0600) lists every key Squadrn wrote, as a path
  (`model`, `permissions.deny`), with the SHA-256 of the value it wrote and its source
  (`main`, `profile`, `squadrn`), plus the profile and the options chosen. Format:
  `{"version":1,"profile":"standard","options":{...},"keys":{"<path>":{"source":"main","sha256":"…"}}}`.
- **Sync** runs before every session (cheap: one read of main's file and the account's) and on
  `account add`. For each key:
  - absent from the record and from the account → written;
  - recorded and still holding the value Squadrn wrote → updated to the new value (or removed
    when no source has it any more);
  - recorded but changed or removed in the account → **the user's change is kept** and reported
    once (`model was changed in claude/work (kept)`); the record follows the user's value, so
    the next sync reports nothing;
  - never recorded and present in the account (the user added it, e.g. `/model`) → untouched.
- Main-only keys never copied: none for Claude today; the provider lists them when needed.
- **Migration** (one time, on the first sync or `account add`): an account whose settings file
  is a link to main's becomes a copy. The link is moved aside as `settings.json.bak-<time>` (a
  backup, never deleted), the copy is built from main's content, and the whole result is recorded
  as main-sourced, so later edits in main keep flowing in. A usage-tracking status line found in
  main's file (written by rc.2 through the link) stays in main for main and is re-applied as a
  Squadrn key in each account that had it.
- `usage setup` writes each account's own copy and lists the accounts it changes; main is
  written only when main is selected.
- `doctor [<account>]` checks, per account (every account without an argument): the settings
  file is a regular file, the record parses, the record's profile is known; it reports keys the
  user changed (ok, informational) and an account still linked (warn, fix:
  `srn account add <account>`).

**Shared by link** (read-only content): `skills/` (per entry; `skills/synced` stays per
account), `agents/`, `commands/`, `rules/`, `output-styles/`, `hooks/`, `keybindings.json`, and
the global instruction file (Claude `CLAUDE.md`; Codex `AGENTS.md`). `hooks/` and
`keybindings.json` keep the links rc.2 made: they are content (scripts, key maps), not state.
**Never shared**: credentials, `.claude.json` / `auth.json`, history, `projects/`,
`file-history/`, plugin state, org policy caches.

**Onboarding seed**: so a new account does not re-run the CLI's first-run setup, an allowlist
of onboarding keys is copied from main into a *new* account only, when absent (Claude:
`.claude.json` → `hasCompletedOnboarding`, `lastOnboardingVersion`; `theme` arrives through
settings). Codex documents no first-run flag: none.

### 3. Profiles

Asked at account creation when interactive (Enter = `standard`), `--profile` otherwise. Shown as
one line each: `minimal` (secrets denied), `standard` (secrets denied, push guard, 30-day
history), `strict` (sandboxed, reads limited to the project, 14-day history).

Claude (`settings.json`):

| Profile | Keys |
|---|---|
| minimal | `permissions.deny`: `Read(./.env)`, `Read(./.env.*)`, `Read(~/.ssh/**)` |
| standard | `permissions.deny`: `Read(...)` for `./.env`, `./.env.*`, `./secrets/**`, `~/.ssh/**`, `~/.gnupg/**`, `~/.aws/**`, `~/.azure/**`, `~/.kube/**`, `~/.docker/config.json`, `~/.npmrc`, `~/.pypirc`, `~/.git-credentials`, `~/.config/gh/**`, `~/.password-store/**`, every *other* account's credential file (`~/.claude/.credentials.json`, `~/.claude-<other>/.credentials.json`, `~/.codex*/auth.json`; regenerated per account); `Edit(...)` for `~/.ssh/**`, `~/.bashrc`, `~/.bash_profile`, `~/.zshrc`, `~/.zprofile`, `~/.profile`, `~/.config/fish/config.fish`, Squadrn's data and config dirs (`Edit` only: Write rules never match). `permissions.ask`: `Bash(git push *)`, `Bash(git reset --hard *)`, `Bash(rm -rf *)`. `enableAllProjectMcpServers: false`, `cleanupPeriodDays: 30` |
| strict | standard, plus `permissions.blockReadsOutsideWorkingDirectories: true`, `permissions.defaultMode: "default"`, `permissions.disableBypassPermissionsMode: "disable"`, `cleanupPeriodDays: 14`, a `ConfigChange` hook running `squadrn hook config-change` (appends to `~/.local/state/squadrn/config-changes.log`); where the sandbox exists (not native Windows): `sandbox.enabled: true`, `sandbox.allowUnsandboxedCommands: false`, `sandbox.failIfUnavailable: true` |

Codex (`config.toml`, implemented with the Codex provider):

| Profile | Keys |
|---|---|
| minimal | `cli_auth_credentials_store = "file"`; `[shell_environment_policy] ignore_default_excludes = false` (KEY/SECRET/TOKEN variables are not passed to commands). Codex has no per-path read rules: the `.env`/`~/.ssh` denials of minimal have no equivalent |
| standard | minimal, plus `approval_policy = "on-request"`, `sandbox_mode = "workspace-write"`, `[sandbox_workspace_write] network_access = false`, `[shell_environment_policy] inherit = "core"` |
| strict | standard with `sandbox_mode = "read-only"`, plus `rules/squadrn.rules` with `forbidden` prefix rules for `git push --force`, `git push -f`, `git reset --hard`, `rm -rf` |

Never set by default: model, reasoning effort, the permission mode in `standard` (the CLI
default stays), attribution.

**Opt-in toggles** (`--with <name>`, repeatable; recorded in the record's `options`; never on
by default). rc.3 ships `privacy` (`env`: `DISABLE_TELEMETRY`, `DISABLE_ERROR_REPORTING`,
`CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC` = `1`) and `no-attribution` (`attribution.commit`
and `attribution.pr` = `""`). Planned on the same mechanism: `notify` (desktop notification
hook), `trash` (an `rm` → trash hook), `gitleaks` (a pre-commit scan when `gitleaks` is on
PATH), `shared-instructions` (one instruction file for both providers, `CLAUDE.md` ↔
`AGENTS.md`), `mcp` (an MCP bundle beyond the `shared_mcp` allowlist). Attribution is never
touched unless `no-attribution` is chosen.

### 4. Account flow

`srn account add [provider/]name [--profile P] [--with T]... [--login|--no-login]
[--usage|--no-usage] [-y]` runs in four steps:

1. **Plan**: resolve the provider and the CLI version (in parallel with the login check of
   main), the target dir, the profile, what will be shared and from where.
2. **Ask** everything up front (only at a terminal, only what flags did not answer): profile,
   usage tracking (naming the accounts it applies to), log in now.
3. **Apply** with one line per step. A *new* account is built in a temp dir next to the target
   (`~/.codex-work.tmp-<pid>`) and renamed into place, so a failure leaves no half account.
4. **Verify** and summarise:

```
$ srn account add codex/work
Codex CLI 0.171.0 · new account codex/work → ~/.codex-work
  profile        standard (secrets denied, push guard, 30-day history)
  shared         skills, agents, AGENTS.md from codex/main
  usage limits   track for codex/work? [Y/n]
  log in now?    [Y/n]
✓ created ~/.codex-work (credentials: file, private)
✓ settings from codex/main + standard profile
✓ push guard: pre-tool hook + git pre-push
✓ usage tracking on
↗ opening login… logged in as you@example.com (ChatGPT Pro)
done in 1.2s · start: srn work · check: srn doctor codex/work
```

A re-run on an existing account prints `codex/work is up to date` or the repairs it made (one
`✓` line each). A failure prints which step failed, what state the account is in (nothing
created, or created and what is missing) and the command to retry. Without a terminal nothing is
asked: defaults apply (`standard`, no usage tracking, no login) and the summary says how to
change them.

Targets: first add < 1.5 s, a re-run < 0.3 s (excluding login): CLI calls run in parallel, the
plugin/MCP/auth state is read once and cached for the run.

`account adopt` uses the same plan/apply/verify steps on an existing dir (its own settings become
the account copy, recorded as user-owned, so nothing of it is overwritten); `--relink` is
replaced by the settings sync and remains only for the linked content items.

### 5. `srn setup`

A guided first run, idempotent, every step skippable:

1. detect agent CLIs on PATH (`claude`, `codex`) and their versions;
2. choose the provider(s) and the default profile;
3. offer to adopt existing `~/.claude-*` / `~/.codex-*` dirs;
4. offer usage tracking (naming the accounts);
5. run `doctor` and print next steps.

The installer runs it after the **first** install only, and only when there is a terminal; never
on `self-update`. Without a terminal the installer prints the detected CLIs, the existing
accounts and the next steps instead.

### 6. Codex CLI provider

Verified against Codex CLI 0.171.0: its `--help` output, a sandboxed run (`CODEX_HOME` in a
temp dir, a failing `security` first on PATH, never logged in), `codex app-server
generate-json-schema`, and the source at tag `rust-v0.171.0` where the docs are silent.
Package `internal/provider/codex`; `provider.Codex` is the spec.

- **Isolation.** Accounts are `CODEX_HOME` dirs; main runs with `CODEX_HOME` unset (Codex's
  default `~/.codex`). Before Codex runs for an account (session, login, any query) squadrn
  makes sure its `config.toml` has `cli_auth_credentials_store = "file"`. It replaces only that
  top-level line, or adds it at the top, and leaves a file that does not parse alone. The
  keyring (`keyring`, or `auto` where one exists) would not follow the account's dir.
  `doctor` checks the setting (`CredentialStore`). Shared from main: `AGENTS.md`, `rules/` and
  `skills/` entry by entry, without `skills/.system`, which Codex installs and updates in every
  `CODEX_HOME` itself.
- **Auth.** `codex login status` prints on stderr, "Logged in using ChatGPT" (or "an API key -
  sk-…", never kept) with exit 0, or "Not logged in" with exit 1. Output that is not recognised
  falls back to the exit status. The email, plan and workspace come from the app server's
  `account/read` (best effort, 5 s). Login and logout are `codex login` and `codex logout`.
- **Push guard, layer 1.** A `PreToolUse` hook (matcher `Bash`) in `$CODEX_HOME/hooks.json`
  runs `"<data dir>/plugin/bin/squadrn-hook" guard-git --no-ask`, the plugin's entry point, so
  a missing squadrn still fails closed. Other hooks in the file are kept. Codex sends
  `tool_name: "Bash"` and `tool_input.command` for shell and unified-exec calls (source:
  `core/src/tools/hook_names.rs`, `handlers/unified_exec/exec_command.rs`), the same fields the
  guard reads from Claude Code. Codex parses `permissionDecision: "ask"` but does not support
  it: it fails the hook run and lets the call through. With `--no-ask` the guard therefore
  **denies** what it would ask about, and its reason tells the user to run the command
  themselves.
- **Hook trust.** Codex runs a non-managed hook only once its exact definition is trusted.
  Managed hooks in `requirements.toml` are trusted by policy, but they live in a system location
  (root) and would apply to every account, so squadrn does not use them. Instead squadrn records
  trust the way `/hooks` does, through the documented app server. `hooks/list` returns the
  hook's `key` (`<CODEX_HOME>/hooks.json:pre_tool_use:<group>:<handler>`) and its `currentHash`.
  `config/batchWrite` then upserts `hooks.state."<key>"` with `trusted_hash` and
  `enabled = true` in the account's `config.toml`. The hash is always Codex's own; squadrn never
  computes it. `doctor` reports a hook that is missing, untrusted or modified. `account add`, the
  installer and `self-update` (`UpdatePlugin`) re-trust it, and `account rename` rewrites the
  path-based keys (`Relocator`). We never use `--dangerously-bypass-hook-trust`.
- **Layer 2** stays the universal git pre-push hook (`GIT_CONFIG_*` in the session's
  environment).
- **Worktrees.** `codex --worktree` takes no name and picks its own location, so `srn wt
  <name>` and grid worktrees stay Claude-only (`Caps.Worktree` false). `srn <account> --worktree`
  passes the flag through.
- **Sessions.** Neither the transcript layout nor the session index is documented: Codex
  0.171 is migrating "legacy local sessions to paginated thread history" (`codex
  migrate-rollouts`, SQLite state). `Caps.SessionTransfer` is false and there is no
  `SessionStore`, so `srn swap` says "Codex CLI cannot transfer conversations". Resume goes
  through the CLI: `srn <account> resume <id>` or `resume --last`.
- **MCP.** `HasMCP` uses `codex mcp get <name>`. `AddMCP` turns a shared definition (Claude's
  `mcpServers` form) into `codex mcp add <name> [--env K=V]… -- <command> <args>…`, or
  `--url` for HTTP. Headers cannot be passed, so such a server is refused. Main's servers come
  from `codex mcp list --json` (`MCPLister`). As for Claude, entries carrying env or headers are
  never copied.
- **Usage.** `RateLimits` reads `account/rateLimits/read` from `codex app-server` over stdio
  JSONL, after the `initialize`/`initialized` handshake. It reads the `codex` bucket of
  `rateLimitsByLimitId`, else `rateLimits`. A window of at most a day maps to the five-hour
  window and a longer one to the seven-day window. A logged-out account gets an
  error ("authentication required"), which is reported as no data. The app server is stopped
  when the replies are in, and a deadline kills its process tree (`proc.Options.Stdout` feeds
  the client while the server runs).
- **Not verified without a login**: the logged-in `account/read` and
  `account/rateLimits/read` payloads (taken from the generated schema), and the hook against a
  real model. The hook path was exercised end to end with Codex driving a local mock Responses
  API: the hook ran trusted and blocked `git push origin "$BRANCH"`.

## Testing

- Unit tests per capability (fake runners), the merge rules (`account`/settings: every row of
  the sync table, migration, deep keys, union order), profile contents per provider.
- Script tests (`testdata/script/*.txtar`) with fake `claude` and `codex`, a throwaway HOME and
  a private tmux server; macOS/Linux output pinned by goldens.
- An end-to-end harness (`make e2e`) runs the real flows against fake CLIs and a stub
  `security` first on PATH: a sandbox HOME does not isolate the macOS Keychain, so no test ever
  reaches it.

## Consequences

- Each account's settings can diverge on purpose (`/model` in one account stays there) while
  main's changes keep flowing to every account that did not change that key.
- A new provider is one package, one `Register` call and the interfaces it can implement.
- `provider.Agent` shrinks to what every CLI has; code that used the old methods asks for the
  capability instead.
- Account names like `mcp` or `plugin` become valid; a bare `srn mcp` runs it for main unless
  such an account exists.
- The record file and `.bak-<time>` backups are new per-account files; `account rm` moves them
  to the trash with the account.
