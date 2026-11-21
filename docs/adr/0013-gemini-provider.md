# 0013 — Gemini CLI provider

Status: accepted (implemented in four phases, #47–#50; corrections from the implementation are
marked *Implementation note*)

## Context

Squadrn drives Claude Code and Codex CLI through the provider model of
[ADR 0011](0011-provider-model-v2.md). Google's Gemini CLI (`@google/gemini-cli`, command
`gemini`) is the third provider. It differs from both in four ways that shape this design:
its account variable relocates a *home*, not a config dir; it has no login, logout or status
command; it disables git hooks in every shell command it runs; and its hooks are synchronous and
read only their own output format.

Facts checked on 2026-11-21 against Gemini CLI 0.70.0 (npm `latest`; `preview` 0.71.0-preview.0),
its bundled docs and source, and a probe run (P0, below). **D** = documented, **S** = read in
the source of 0.70.0, **P** = observed in the probe. A key that could not be verified is not used.

| Fact | |
|---|---|
| `GEMINI_CLI_HOME` replaces the home dir for all CLI state: state lives in `$GEMINI_CLI_HOME/.gemini/` (`settings.json`, `oauth_creds.json`, `google_accounts.json`, `installation_id`, `trustedFolders.json`, `projects.json`, `history/`, `tmp/<project>/chats/`, `extensions/`, `policies/`); nothing is written to `HOME`. Since 0.25.0 | D, P |
| `gemini --version` prints the bare version (`0.70.0`) | P |
| Subcommands: `mcp`, `extensions`, `skills`, `hooks`, `gemma`, `models`; no `login`, `logout` or auth status | P |
| `settings.json` may hold comments: it is read through `strip-json-comments` (default options: trailing commas are **not** accepted) | S, P |
| Precedence: system defaults < user `settings.json` < project `.gemini/settings.json` < system settings; env vars and flags on top | D |
| Auth type is `security.auth.selectedType`: `oauth-personal`, `gemini-api-key`, `vertex-ai`, `cloud-shell`, `compute-default-credentials`, `gateway` | S |
| Google login tokens are kept in `<state>/oauth_creds.json` by default. With `GEMINI_FORCE_ENCRYPTED_FILE_STORAGE=true` they go to the OS keychain under a fixed entry (`gemini-cli-oauth` / `main-account`) shared by every home, falling back to `<state>/gemini-credentials.json`, encrypted with a key derived from host and user name. An API key typed in the UI goes to a fixed keychain entry (`gemini-cli-api-key`) | S |
| Login is interactive only (first run or `/auth`); `NO_BROWSER=true` prints a URL and takes a pasted code | D |
| Hooks: `hooks.<Event>[] = {matcher, hooks: [{type: "command", command, name, timeout (ms, default 60000), env}]}` in `settings.json`; events `BeforeTool`, `AfterTool`, `BeforeAgent`, `AfterAgent`, `BeforeModel`, `AfterModel`, `BeforeToolSelection`, `SessionStart`, `SessionEnd`, `PreCompress`, `Notification`; no `async` | D |
| `BeforeTool` stdin: `session_id`, `transcript_path`, `cwd`, `hook_event_name`, `timestamp`, `tool_name: "run_shell_command"`, `tool_input: {command, description, dir_path}`; `dir_path` is passed as the model wrote it (relative to `cwd`) | P |
| A `BeforeTool` hook blocks with exit 2 (stderr is the reason) or exit 0 with `{"decision":"deny","reason":…}`, also in `--approval-mode yolo`. Any other exit code lets the call through with a warning. Claude Code's `hookSpecificOutput.permissionDecision: "deny"` is **ignored**: the call runs | D, P |
| `ask` is not a documented decision; the policy engine has `decision = "ask_user"` (`commandPrefix`, `argsPattern` regex over the tool's JSON arguments), in `<state>/policies/*.toml` or `--policy <file>` (the flag is in the source, not the CLI reference) | D, S |
| `AfterAgent` fires once per turn (`prompt`, `prompt_response`, `stop_hook_active`); a `deny` or exit 2 there **rejects the response and retries the turn** | D |
| `Notification` (`notification_type: "ToolPermission"`, `message`, `details`) fires on permission prompts and is observe-only; no event fires when a turn waits for input otherwise, nor on the usage-limit dialog | D |
| Hooks get the CLI's environment (sanitized only when redaction is on) plus `GEMINI_PROJECT_DIR`, `GEMINI_CWD`, `GEMINI_SESSION_ID`, `GEMINI_PLANS_DIR`, `CLAUDE_PROJECT_DIR`, and the hook's own `env`; they run through the platform shell (PowerShell on Windows, with `; if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }` appended) | S, P |
| Every shell command the agent runs gets git config overrides appended after the inherited `GIT_CONFIG_*` entries: `credential.helper`, `core.fsmonitor`, **`core.hooksPath`**, `core.sshCommand`, `core.editor`, `sequence.editor` = `""`, `core.pager` = `cat`; plus `GIT_CONFIG_NOSYSTEM=1`, `GIT_TERMINAL_PROMPT=0`. Unconditional; no setting turns it off. An empty `core.hooksPath` runs no hooks at all (git 2.54) | S, P |
| Inherited `GIT_CONFIG_*` and `TMUX_PANE` reach shell commands and hooks; names starting `GEMINI_CLI_` are never redacted | S, P |
| `--skip-trust` exports `GEMINI_CLI_TRUST_WORKSPACE=true` to every child | P |
| Folder trust is on by default; `trustedFolders.json` maps absolute paths to `TRUST_FOLDER` / `TRUST_PARENT` / `DO_NOT_TRUST`. An untrusted folder ignores project settings, hooks, MCP and `.env` | S |
| `general.enableAutoUpdate` (default true) runs `npm install -g @google/gemini-cli@latest` | D, S |
| `gemini -w <name>` creates `.gemini/worktrees/<name>` on branch `<name>` when `experimental.worktrees` is true | D |
| Quota exhaustion shows "Usage limit reached for <model>" and a fallback-model dialog; remaining quota is readable only through the internal `cloudcode-pa.googleapis.com/v1internal:retrieveUserQuota` | D, S |
| npm installs `gemini` as a Node script; on Windows that is `gemini.cmd` | D |

## Decision

### 1. Spec and account layout

`provider.Gemini`: ID `gemini`, binary `gemini`, `MainDir` `.gemini`, `DirPrefix` `.gemini-`,
`ConfigEnv` `GEMINI_CLI_HOME`, settings `settings.json`, subcommands as probed, install hint
`npm install -g @google/gemini-cli`, `Caps{Worktree: true, Plugins: true, SharedMCP: true}` (each
turned on by the phase that implements it). Floor: 0.70.0,
the probed version (`doctor` warns below it).

A secondary account's dir is the `GEMINI_CLI_HOME` value (`~/.gemini-work`, holding the
`.squadrn-account` marker); the CLI's state is in its `.gemini/` subdir. Main is `~/.gemini`
itself, run with the variable unset. A new `Spec.StateDir` (`.gemini`) and `Account.StateDir()`
(main: `Dir`; others: `Dir/StateDir`) carry this, and every place that joins `Settings`,
`Shared`, `SharedPerEntry`, `Credentials` or `MCPConfig` to an account dir uses `StateDir()`
instead (`account/provision.go`, `account/settings.go`, `cli/statusline.go`,
`cli/account_add.go`, `doctor/doctor.go`). For Claude and Codex `StateDir()` is `Dir`. The
driver maps the `configDir` it is given the same way. The rejected alternative, treating `~` as
main's dir, breaks `exists()` (gemini/main would always exist), purge, relocate and the record.

### 2. Credentials and isolation

- `Spec.Credentials` becomes a list: `oauth_creds.json`, `google_accounts.json`,
  `gemini-credentials.json`. The encrypted file's key derives from host and user, so any account
  could decrypt another's; every profile (of every provider) denies all three for the other
  accounts.
- Sessions, logins and queries run with `GEMINI_FORCE_ENCRYPTED_FILE_STORAGE` removed from the
  environment, so tokens stay in the account's file. `doctor` (`CredentialStore`) warns when it is
  set in the user's environment, when `GOOGLE_APPLICATION_CREDENTIALS` is set, and that API keys
  belong in the environment, not the UI (fixed keychain entry).
- Shared from main: `GEMINI.md`, `commands/`, `agents/`, `skills/` (per entry). Never shared:
  the credential files, `installation_id`, `projects.json`, `tmp/`, `history/`,
  `trustedFolders.json` and `trusted_hooks.json` (the CLI rewrites them, which would break a
  link), `policies/` (a squadrn file there would apply to every account; the lesson of Codex
  `rules/`), `extensions/` (later).

### 3. Settings

`SettingsFormat` for JSON with comments: decode strips comments; an account's own copy is
written without them. Main's `settings.json` is never re-encoded: changes to it (the guard hook)
are made only when it has no comments, else squadrn stops and prints the hook to add.

Deep keys: `general`, `ui`, `tools`, `privacy`, `context`, `security`, `hooks`, `hooksConfig`,
`experimental`. Private (never taken from main): `mcpServers` (may carry tokens; the
`shared_mcp` allowlist copies servers), and **squadrn's own entries inside `hooks`**: they are
filtered out of main's `hooks` before the merge and written per account, so a profile without the
guard stays without it and removing it from one account sticks. This filter also applies to
Claude's `hooks` key.

The seed for a new account copies `security.auth.selectedType` and `ui.theme` from main when
present, so the CLI skips its auth-method and theme dialogs.

### 4. Auth

No CLI command exists, so auth is file-based behind the `Authenticator` interface:

- **Status**: logged in when `<state>/oauth_creds.json` holds a `refresh_token` and
  `selectedType` is `oauth-personal`, or when `selectedType` is `gemini-api-key` / `vertex-ai`
  and the environment provides the key (metered). Email from `google_accounts.json` (`active`),
  best effort. Token values are never read into output or logs.
- **Login**: runs `gemini --skip-trust` interactively with `selectedType` from main's settings
  (*Implementation note*: the settings merge already carries it; there is no separate seeder),
  after printing
  "finish the Google login, then type /quit". `NO_BROWSER` is passed through for SSH sessions.
  squadrn does not kill the CLI when the token file appears.
- **Logout**: moves the credential files to the trash. A new optional interface, `FileLogout`,
  replaces `LogoutArgs` for providers without a logout command.

### 5. Push guard

**Layer 2 does not exist in Gemini sessions.** Gemini appends `core.hooksPath=""` after
squadrn's `GIT_CONFIG_*` entries in every shell command, which disables all git hooks, ours and
the repository's. It cannot be turned off. Layer 1 and the server's branch protection are the
boundary; `doctor` says so for every Gemini account, and `account add` prints it. A git wrapper
first on the session's `PATH` would restore a second layer; deferred (it intercepts every git
call and must be exact on every platform).

**Layer 1** is a `BeforeTool` hook (matcher `run_shell_command`, `timeout: 5000`, name
`squadrn-guard`) in each account's own `settings.json`, running
`"<data dir>/plugin/bin/squadrn-hook" guard-git --no-ask --agent gemini`.

- `guard-git --agent gemini` reads `tool_input.command` and resolves `tool_input.dir_path`
  against `cwd`. It denies with exit 2 and the reason on stderr. Ask becomes deny (`--no-ask`),
  as for Codex.
- **The shim's fail-closed path changes to exit 2 with the reason on stderr**, for every agent.
  Its JSON reply today is Claude's format, which Gemini ignores: a missing squadrn would let
  pushes through. Exit 2 blocks in Claude Code, Codex and Gemini alike.
- Windows: the command runs under PowerShell, so it is
  `& "<data dir>\bin\squadrn.exe" hook guard-git --no-ask --agent gemini`. A hook that cannot
  start fails open (exit code other than 0/2); `doctor` warns, as for Codex.
- The standard profile adds a policy rule `commandPrefix = "git push"`,
  `decision = "ask_user"`, passed per session with `--policy <state>/squadrn-policy.toml`, so the
  user still confirms every push as in Claude Code.
- Not an extension: `gemini extensions install` needs a trusted cwd and a consent prompt, and
  settings hooks need neither. Revisit if squadrn ships Gemini commands.

### 6. Notifications

`HookEvents` for Gemini: `BeforeAgent` → working, `AfterAgent` → done, `Notification`
(`ToolPermission`) → needs input. (*Implementation note*: an earlier draft said "or input when the
response ends in a question, as for the other CLIs"; the other CLIs do not do that, so neither
does Gemini.) Hooks are synchronous, so the notify hook for Gemini always exits 0 with empty stdout
(never exit 2 or a `decision`, which would retry the turn), has `timeout: 2000`, and hands the
delivery to `hook deliver` in the background. No `AfterTool` hook: it would add latency to every
tool call.

The usage-limit dialog and other waits fire no hook. `srn ps` reads the tmux `#{pane_title}`,
which Gemini sets to `✋ Action Required` / `⏲ Working…` / `◇ Ready` (`ui.dynamicWindowTitle`,
on by default), as a fallback state for Gemini panes. Squadrn sets no pane titles today.

When squadrn's notifications are on, the profile sets `general.enableNotifications: false`.

### 7. Profiles

| Profile | Keys |
|---|---|
| minimal | credential isolation (section 2); `security.allowedEnvironmentVariables` += `GIT_CONFIG_COUNT`, `GIT_CONFIG_KEY_0..7`, `GIT_CONFIG_VALUE_0..7`, `TMUX`, `TMUX_PANE` (so turning redaction on never breaks git or notifications); squadrn policy file denying `read_file`, `read_many_files`, `grep_search`, `glob` on `.env`, `.env.*`, `~/.ssh/**` and other accounts' credential files |
| standard | minimal, plus the full read-deny list of ADR 0011's Claude standard profile as policy rules, the guard hook, `ask_user` for `git push`, `general.sessionRetention.maxAge: "30d"`, `security.disableYoloMode: true`, `general.enableAutoUpdate: false` in secondary accounts (parallel panes would run `npm install -g` at once; main keeps the user's choice) |
| strict | standard, `general.sessionRetention.maxAge: "14d"`, approval mode `default` enforced; the sandbox is not used until verified (`sandbox-exec` moves state to `<home>/.cache/.gemini`; docker would hide squadrn from the hooks) |

Policy rules restrict tools, not shell programs: `cat .env` through `run_shell_command` is not
covered, as with Claude's read rules outside the sandbox.

`privacy`: `privacy.usageStatisticsEnabled: false`, `telemetry.enabled: false`.
`no-attribution`: none (Gemini adds no attribution).

### 8. Trust and worktrees

Folder trust is per account. On first use of a repository in a Gemini account, squadrn asks
once and writes `TRUST_FOLDER` for the repository root (so every worktree squadrn creates below it
is trusted) to that account's `trustedFolders.json`. (*Implementation note*: the draft said
`TRUST_PARENT`, which trusts the folder's *parent*, i.e. every sibling repository too.) A no leaves
the decision to Gemini's own dialog, which records it, so squadrn does not ask again. It never sets `GEMINI_CLI_TRUST_WORKSPACE`,
which would also trust hooks and MCP servers a repository brings.

`srn wt` for Gemini (`WorktreeDir` `.gemini/worktrees`, `-w <name>`) needs
`experimental.worktrees`, which every profile sets (main, which has no profile, needs it set by
hand). `srn wt ls` lists the default provider's worktree dir only.

### 9. Not supported

- Usage limits and swap: no public quota API (the internal Code Assist endpoint is not a
  contract), and no hook reports a limit. `srn usage` shows Gemini accounts as not reporting.
- Session transfer: `tmp/<project>/chats/` is keyed through `projects.json`; not documented.
  Resume passes through (`srn <account> -r latest`).
- Windows launch uses `node <package>/bundle/gemini.js`, not `gemini.cmd`, which `cmd.exe`
  would re-parse (quotes in prompts), as for Codex's `.cmd`.

### 10. Addendum (2026-11-21): consumer accounts are refused

This ADR missed that Google stopped serving Gemini CLI to consumer accounts on **2026-06-18**
(announced 2026-05-19, "Transitioning Gemini CLI to Antigravity CLI", Google Developers Blog;
deprecation page developers.google.com/gemini-code-assist/docs/deprecations/code-assist-individuals).
The 0.70.0 bundled docs, which the facts above relied on, still describe the free tier.

- Login with Google for a personal account, Google AI Pro or Ultra fails with *"This client is no
  longer supported for Gemini Code Assist for individuals. To continue using Gemini, please migrate
  to the Antigravity suite of products"*. The server returns it (`UNSUPPORTED_CLIENT`); the CLI
  only wraps it (`Failed to sign in. Message: …`), so no CLI version helps.
- Gemini CLI still serves Code Assist Standard and Enterprise licenses (Login with Google plus
  `GOOGLE_CLOUD_PROJECT`), paid Gemini API keys and Vertex AI. Its README now reads "Enterprise
  Code Assist only". Code Assist Standard/Enterprise has its own sunset (no new purchases from
  2026-10-09, through 2027), with Antigravity as the replacement.
- The quota table of the Context (1,000/1,500/2,000 requests a day for Google login) no longer
  applies to consumer accounts.

Squadrn keeps the provider for those users and says the rest plainly: the login hint names the
supported licenses and Antigravity CLI (`agy`); `doctor` warns when an account selects
`oauth-personal` and neither `GOOGLE_CLOUD_PROJECT` nor `GOOGLE_CLOUD_PROJECT_ID` is set (Gemini
also reads them from `.env` files, which squadrn does not; the warning says what to set). squadrn
cannot see the refusal itself: it happens inside the interactive login. Antigravity CLI as a
provider needs its own decision (no config-dir variable yet, logins in the OS keyring).

## Probe (P0)

Gemini CLI 0.70.0 in a sandbox: `HOME` and `GEMINI_CLI_HOME` in a temp dir, a fake API key in
the environment (no keychain: the `@github/keytar` build script did not run), a local mock of the
Gemini API (`GOOGLE_GEMINI_BASE_URL`) answering the first request with a `run_shell_command`
call, `--approval-mode yolo --skip-trust -p`. Recorded:

- state written only under `$GEMINI_CLI_HOME/.gemini/`; a `settings.json` with comments loads;
- `BeforeTool`, `BeforeAgent`, `AfterAgent`, `SessionStart` payloads as in the table;
- exit 2 → "Tool execution blocked: <stderr>" returned to the model; JSON `deny` → blocked with
  the reason; Claude-format JSON → **ran**; exit 1 → ran;
- in the shell: `GIT_CONFIG_COUNT=8`, our entry at index 0, Gemini's seven overrides after it,
  including `core.hooksPath=""`; a pre-commit hook in both squadrn's hooks dir and `.git/hooks`
  did not run under those overrides;
- `TMUX_PANE` and `GIT_CONFIG_*` reach hooks; `GEMINI_CLI_TRUST_WORKSPACE=true` is exported by
  `--skip-trust`.

Not probed: `Notification` (interactive only), Windows, a real Google login.

## Plan

One PR each, in order:

1. **Core**: `StateDir`, credentials list, JSONC settings, squadrn-entry filter in `hooks`,
   file-based auth and `FileLogout`, Windows launch through node, `doctor` (keychain variable,
   credentials, Node and CLI version, layer-2 notice), fake `gemini` and `gemini.txtar`.
2. **Guard**: `--agent gemini` input and exit-2 reply, shim fail-closed through exit 2, the hook
   writer (`GuardInstaller`), PowerShell command, e2e against the mock API.
3. **Notifications**: `HookEvents`, background delivery, pane-title fallback in `srn ps`.
4. **Profiles, MCP, trust, worktrees**.

All four are implemented. The policy file is `<state>/squadrn-policy.toml` (not in `policies/`),
written from the profile and passed with `--policy` to sessions and logins, not to subcommands.
`srn ps` maps the ◇ title to `ready`.

## Consequences

- Gemini accounts are protected by one local layer, not two; the docs and `doctor` say so.
- The shim's fail-closed reply changes for every agent (exit 2 instead of JSON), with no change
  in what is blocked.
- `Spec.StateDir`, a credentials list and `FileLogout` are new provider-model surface; Claude and
  Codex keep their current behaviour.
- Squadrn's hook entries are no longer inherited from main by any provider's account settings.
