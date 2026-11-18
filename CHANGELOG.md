# Changelog

## 1.2.0-rc.2

- `install.ps1` no longer fails when the PowerShell profile cannot be written (Documents redirected
  or protected by Controlled Folder Access); it warns and prints the line to add instead.

## 1.2.0-rc.1

Native Windows support, beta (ADR 0008). macOS and Linux behave as in 1.1.0 (golden tests).

- Windows 10 21H2+/11, amd64 and arm64, with Git for Windows 2.31+: install with `install.ps1`
  (signature and checksum verified), sessions as accounts, account management, worktrees, `gc`,
  `deps`, `doctor`, updates, rollback and uninstall. `grid`, `ps` and `swap` inside a pane need
  tmux and are not available on Windows.
- Push guard on Windows: Claude Code's PowerShell tool is judged by an allow-list analyser that
  asks whenever git may run in a way it cannot follow; the shell parser understands `git.exe`,
  Windows paths, `wsl`, `winpty`, `cmd /c`, `start` and `pwsh -Command`; hooks run through
  `cmd.exe` so a binary that cannot start blocks the call; layer 2 chains to repository hooks
  through Git for Windows' `sh.exe`.
- Windows specifics: command names are copies of `sprawl.exe` refreshed on update (replacing a
  running binary is supported); shared settings use symbolic links, or junctions and hash-checked
  copies without Developer Mode; directories outside the profile get a protected ACL; PowerShell
  tab completion (`install.ps1 -AddToProfile`).
- `doctor` on Windows checks Git for Windows, Git Bash, the signature tool, the guard hook as Claude
  Code runs it, stale command copies, the execution policy and long paths.
- Fixes found while porting, on every platform: archive member names are validated the same way
  everywhere; `account rename` rewrites plugin paths written with backslashes; recommended
  `permissions.deny` rules use `/`.
- Install a release candidate on Windows with `-Version v1.2.0-rc.1`; it follows the beta channel.


Groundwork for native Windows support (ADR 0008); no behaviour change on macOS or Linux.

- sprawl needs Git 2.31 or newer, as it already did in practice; an older Git now gets a clear
  message with the upgrade command for your system instead of a raw git error. `doctor` reports
  it, `install.sh` refuses older Git, and `self-update` only warns.
- Platform-specific code moved behind small seams (`_unix.go` files); golden tests pin the help,
  completion, guard decisions, install layout and release archives on macOS and Linux.
- Line endings are LF on every platform (`.gitattributes`).

## 1.0.0 — Sprawling Agents

First published release (ADR 0005). Earlier versions below were internal under the names squad and ccx.

- Product renamed **Sprawling Agents**; default command `sprawl` with alias `sra`.
- Slug `sprawl` for all fixed identifiers: `~/.config/sprawl`, `~/.local/share/sprawl`,
  `~/.local/state/sprawl` (guard log, backups), `SPRAWL_*`, plugin `sprawl@sprawl`,
  git key `sprawl.protectedBranches`, tmux options `@sprawl_*`.
- Installing over squad migrates settings (not command names), replaces their plugins in
  every account, removes their command links and backs their files up; their git keys are still read.
- A command name that resolves to the binary being installed is no longer reported as a clash.
- ccx compatibility removed (it was never published): no `ccx-`/`cld-` labels, no migration from
  ccx, `ccx.protectedBranches` no longer read. The bash reference implementation (`legacy/`) is
  gone; its behaviour is covered by the Go test suite.
- Migration from squad also moves its guard log to `~/.local/state/sprawl/legacy-logs/` and drops
  its cache.

### Security

- Push guard rebuilt in layers (ADR 0006). Layer 1 normalises shell words and follows git option
  prefixes, aliases, here-strings, wrappers and push configuration; it asks whenever something is
  only known at runtime and never fails open (3 s deadline, crash recovery, hooks dispatched before
  anything else). Layer 2 is a git `pre-push` hook injected into sprawl sessions through
  `GIT_CONFIG_*`, chaining to the repository's own hooks. `doctor` checks layer 3 (GitHub branch
  protection) and recommends `permissions.deny` rules for sprawl's files.
- Repository config can only add protected branches. Branch matching is case-insensitive.
- Guard log masks more credential formats (URL userinfo, GitHub/GitLab/Slack/npm/OpenAI/Anthropic/
  Hugging Face/PyPI tokens, AWS keys, `--token`/`--password` flags, `Authorization` values), records
  the layer, refuses symlinks and keeps its directory `0700`.
- Plugin shim without sprawl: denies anything mentioning push (case and quotes ignored), asks about
  other git commands.
- Release extraction caps sizes and entry counts and normalises file modes; the binary is replaced
  before the plugin; account dirs are created `0700`.
- `self-update`: repository only via `--repo` (no environment override), refuses downgrades without
  `--allow-downgrade`, verifies GitHub build provenance when the release has it.
- Release pipeline: actions pinned by commit, `persist-credentials: false`, full CI matrix before
  publishing, govulncheck, SBOM, build provenance, reproducible archive timestamps.

### Reliability

- Every external program (git, tmux, gh, claude) runs under a deadline that kills its process group.
- Auth status: at most 4 lookups at once, failures cached for 15 s, cache keyed by account label,
  atomic writes; `~/.cache/sprawl` on macOS too. All dirs honour `XDG_*` on both platforms.
- Grid sessions are created and tagged in one tmux call, so concurrent `grid` runs no longer create
  duplicate sessions; tmux errors are no longer ignored.
- Config files carry `schema = 1`; newer files are refused instead of misread.

### Commands

- Routing: subcommands win over account names; unknown words are usage errors with suggestions;
  prompts go after `--`; agent subcommands pass through; `--version`, `completion` and `-C <dir>`,
  `--no-color`/`NO_COLOR` work as global options.
- New: `account logout`, `account rename`, `wt ls`, `wt rm`, `grid ls|attach|stop`,
  `config get|set <key>` for every setting (`git_hooks`, `protected_branches`, …).
- `--json` for `account ls`, `ps`, `gc`, `doctor`, `config`, `grid ls`, `wt ls`, `version`.
- Help grouped into Agents / Accounts / Setup with examples; completions for zsh, bash and fish.
- Answering "no" at a confirmation exits 1 with "aborted"; `deps` passes the package manager's exit
  status through; `swap` asks before moving a conversation into another organization's account.
- Picker: scrolls in small terminals, `ctrl-j/k`, Home/End/PgUp/PgDn, offers login for an account
  that is not logged in. A spinner shows while account status loads.
- The picker talks to the terminal directly instead of through a TUI framework, which queried the
  terminal's colors on every command (latency, stray `]11;?` output in some terminals). The binary
  is about 10% smaller.
- `doctor`: checks the three guard layers, account dir permissions, `--json`, fix hints with `~`
  paths.

### Distribution and updates (ADR 0007)

- Releases are published to a separate binaries-only repository; users never need access to the
  source. Release notes come from this CHANGELOG.
- Releases are signed (ed25519, SSH signature format); `install.sh` and `self-update` fetch the
  signature first and refuse unsigned or wrongly signed releases with a clear reason. Keys: `scripts/release-key.sh`, `internal/release/signers.pub`.
- Update notices like Claude Code: a daily background check, a one-line notice at a terminal,
  never forced. `update_check`, `update_channel` (stable, beta), `SPRAWL_NO_UPDATE_CHECK`.
- `self-update --check` shows release notes; `self-update --rollback`; `self-uninstall [--purge]`.
- `doctor` reports pending updates and whether the build verifies release signatures.
- Proprietary LICENSE, shipped in every archive.

### Internals

- `provider.Agent` interface; Claude Code specifics (shared items, worktree dir, subcommands,
  install hint) are data in `provider.Spec`. New packages `install`, `githook`, `update`, `proc`,
  `fsx`.
  `grid.Plan` → `grid.Assign`, `worktree.Plan/Apply` → `Repo.Classify/RemoveAll`.

## History
### 3.0.0 — squad (unpublished)

ccx is renamed **squad** (ADR 0004).

- Choose your own command name at install (`--name`, prompt, or `SQUAD_COMMAND`; default `squad`),
  add aliases, rename later with `config set command|aliases`. Clashes with other programs are
  refused unless `--force`.
- Accounts are `provider/name` (`claude/personal`); bare names resolve when unique across providers.
  Provider specs (`internal/provider`) prepare Codex/Gemini support.
- Grid sessions are found by tag: renaming the command never orphans a grid, and two repositories
  with the same directory name no longer share one session.
- Paths: `~/.config/squad`, `~/.local/share/squad`, guard log in `~/.local/state/squad/guard.log`;
  env vars `SQUAD_*`; plugin `squad@squad`; git key `squad.protectedBranches` (`ccx.` still read).
- Installing over ccx migrates settings and plugins and backs the old files up.
- `swap` tags the restarted pane with the full account label (was the bare name).

### 2.0.1 — ccx (unpublished)

- `account add` removes links to items main no longer has instead of leaving them dangling;
  `doctor` reports dangling links.
- Every install (`install.sh`, `self-install`, `self-update`) refreshes the plugin in accounts
  that already have it, so Claude Code picks up the new version.

### 2.0.0 — ccx (unpublished)

Rewrite in Go; `legacy/` keeps the bash version and its test suite as the behavioural reference.

- One binary for the CLI and the Claude Code hooks; the plugin calls `ccx hook …` through a
  fail-closed shim (no ccx → pushes are denied).
- Git push guard parses commands as shell (pipelines, `$(…)`, `bash -c`, `eval`, here-documents,
  wrappers, `cd`, `git -C`) and answers deny / ask / allow. Protected branches can be set per
  repository with `git config ccx.protectedBranches`. Decisions are logged with credentials masked.
- Account picker without fzf; account labels `ccx-<name>` (and the old `cld-`) accepted everywhere.
- `grid`, `ps`, `swap`, `gc`, `deps`, `doctor`, `self-update`; releases via GoReleaser and a
  checksum-verifying `install.sh` for the private repository.
- Exit codes: 0 ok, 1 error, 2 usage, 130 cancelled.
- Config moved to `~/.config/ccx/config.toml` (unknown keys are errors).
