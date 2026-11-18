# Sprawling Agents

*A sprawl of coding agents. Any provider, any account, in parallel.*

Run many coding agents in parallel on one machine — across several accounts and isolated git
worktrees — with a guard that stops agents from force-pushing or pushing to protected branches.

Providers: Claude Code today; the account model (`provider/name`) is ready for Codex and Gemini.
Platforms: macOS and Linux (amd64, arm64); Windows 10 21H2+/11 (amd64, arm64) in beta, without
`grid` (ADR 0008).

## Install

Sprawling Agents is private software (see [LICENSE](LICENSE)). Releases live in a separate
repository that holds binaries only; access is by invitation. With [gh](https://cli.github.com)
logged in to the account that was invited:

```sh
gh release download -R OWNER/sprawl-releases -p install.sh -O - | sh
```

The default command is **`sprawl`** with the short alias **`sra`**. The installer lets you pick
other names, or pass them up front (`--alias ''` installs no alias):

```sh
gh release download -R OWNER/sprawl-releases -p install.sh -O - | sh -s -- --name ai --alias ''
```

The installer checks the release signature (`ssh-keygen`) and checksum, installs the binary to
`~/.local/share/sprawl/bin/sprawl`, links your command name(s) from `~/.local/bin`, and installs
the sprawl plugin into every Claude account.

### Windows (beta)

Requires [Git for Windows](https://git-scm.com/download/win) 2.31+ and `gh`. In PowerShell:

```powershell
gh release download -R OWNER/sprawl-releases -p install.ps1 -O - | Out-String | Invoke-Expression
# with options:
& ([scriptblock]::Create((gh release download -R OWNER/sprawl-releases -p install.ps1 -O - | Out-String))) -Name ai -Alias ''
```

Everything above works the same, with these differences:
- `grid`, `ps` and `swap` inside a pane need tmux and are not available; run each agent with
  `sprawl wt <name>` in its own terminal tab.
- Command names are copies of `sprawl.exe` in `%USERPROFILE%\.local\bin`, refreshed on update.
- Shared settings use symbolic links with Developer Mode enabled; without it, directories become
  junctions and files hash-checked copies (an edit in a secondary account is reported, never
  overwritten).
- The push guard covers both the Bash (Git Bash) and PowerShell tools of Claude Code.
- Until releases are code-signed, SmartScreen may warn on first run.

### Updates

Like Claude Code, sprawl tells you about a new release and never installs it by itself. Once a day
it checks in the background (never delaying a command) and, at a terminal, shows one line:

```
✦ sprawl v1.1.0 is available (you have v1.0.0) · sprawl self-update · what's new: sprawl self-update --check
```

```sh
sprawl self-update --check       # what is new (release notes)
sprawl self-update               # install it: signature, checksum, then the plugin in every account
sprawl self-update --rollback    # back to the release installed before (run again to go forward)
sprawl config set update_channel beta    # also pre-releases; stable is the default
sprawl config set update_check false     # no checks, no notices (also: SPRAWL_NO_UPDATE_CHECK=1)
```

Updates keep your command names, refuse to downgrade unless `--allow-downgrade`, and refuse any
release not signed by a key built into the copy you have. No notice is shown in CI, with `--json`
or outside a terminal. `sprawl doctor` mentions a pending update too.

To remove sprawl: `sprawl self-uninstall` (plugin from every account, command links, completions,
installed files; accounts are kept). `--purge` also removes the config, guard log and caches.

### Names and completions

```sh
<command> config set command ai          # relinks ~/.local/bin and the shell completions
<command> config set aliases sra,sx
```

Completions for zsh (`~/.zsh/completions`), bash (`~/.local/share/bash-completion/completions`)
and fish (`~/.config/fish/completions`) are written when those directories exist; otherwise
`sprawl completion zsh|bash|fish` prints them.

Requirements: `claude` (native build), `git` 2.31 or newer; `tmux` for `grid`/`ps`/`swap`; `gh` for installing
and updating. Examples below use `sprawl`; `sra` works the same.

## Accounts

An account is a provider's config dir, written `provider/name`: `claude/main` is `~/.claude`,
`claude/personal` is `~/.claude-personal`. A bare name works when it is unique across providers;
`main` always means the default provider's main account. Settings, `CLAUDE.md`, agents, hooks and
skills are shared from the provider's main account through symlinks; logins, history and plugins
stay per account.

```sh
sprawl                              # pick an account interactively (offers login when needed)
sprawl personal                     # run as claude/personal (also: claude/personal, -a personal)
sprawl personal -p "summarize"      # agent flags go to the agent CLI unchanged
sprawl personal -- "fix the bug"    # a prompt goes after --
sprawl personal mcp list            # agent subcommands pass through
sprawl account add work             # create or repair claude/work
sprawl account login work
sprawl account ls [--json]
sprawl account logout work
sprawl account rename work job      # usually needs a new login
sprawl account rm work              # logs out and moves the dir to the trash
```

A word that is neither a sprawl command, an account nor an agent subcommand is a usage error with
a suggestion (`sprawl acount` → *did you mean account?*), so a typo never becomes a prompt.
Global options before the command: `-a <account>`, `-C <dir>`, `--no-color` (also `NO_COLOR=1`).

Account names: `^[a-z][a-z0-9-]{1,23}$`, e.g. `personal`, `comacpro-2`. Names that are sprawl or
provider subcommands (`mcp`, `wt`, …) are rejected. Account dirs are created private (`0700`).

## Parallel agents

```sh
sprawl wt feat-login                                    # agent in .claude/worktrees/feat-login
sprawl wt ls [--json]                                   # worktrees, modified or not, running as whom
sprawl wt rm feat-login [--force]                       # remove one (branch only if merged)
sprawl grid ~/repo feat-a personal:fix-b                # tmux grid, one agent per pane
sprawl grid ~/repo feat-a claude/personal:fix-b         # account as provider/name
sprawl grid ~/repo feat-1 feat-2 feat-3 --spread        # spread over logged-in accounts
sprawl grid ~/repo personal:chore-c                     # add a pane to the running grid
sprawl grid ls | attach [repo] | stop [repo]            # manage running grids
sprawl ps [--json]                                      # which agent runs as which account
sprawl swap fix-b personal                              # account hit its limit: move the conversation
sprawl deps feat-a                                      # install dependencies (exit status passes through)
sprawl gc [--json]                                      # merged/empty worktrees; --yes removes them
```

Worktree names are `<type>-<slug>` (`feat-otp-login`, `fix-cart-total`); Claude Code creates branch
`worktree-<name>`. `swap` moves conversations between accounts of the same provider and asks first
when the target account belongs to another organization.

## Git push guard

Three layers keep agents from force-pushing or pushing to protected branches:

1. **Pre-tool hook.** The sprawl plugin runs `sprawl hook guard-git` before every Bash command an
   agent runs. It parses the command as shell — pipelines, `$(…)`, `bash -c`, `eval`, here-documents
   and here-strings, `sudo`/`env`/`timeout`/`script`/`watch`/`find -exec` wrappers, `cd`, `git -C`,
   quoting tricks (`pu''sh`), case (`GIT PUSH`), git's option prefixes (`--forc`), `-c alias.*` and
   repository aliases — and resolves a bare `git push` the way git does (`push.default`,
   `remote.*.push`, upstream). It **denies** force pushes, `--all`/`--mirror`/`--prune`,
   `--no-verify`, and pushes or deletions targeting a protected branch; **asks** when the target is
   only known at runtime (`$BRANCH`, `xargs`, `python -c …`, a shell reading stdin); **allows** the
   rest. Any failure or timeout asks, never allows.
2. **git pre-push hook.** Sessions sprawl starts (`sprawl`, `wt`, `grid`, `swap`) get
   `core.hooksPath` pointed at `~/.local/share/sprawl/git-hooks` through `GIT_CONFIG_*`. Git's
   pre-push hook sees the exact refs, so pushes layer 1 cannot see (scripts, interpreters) are
   checked too, including non-fast-forward updates. The repository's own hooks (husky, lefthook,
   pre-commit) still run after the check. Turn it off with `sprawl config set git_hooks false`.
3. **The server.** Only branch protection on the remote stops pushes made outside sprawl.
   `sprawl doctor` checks it for the current GitHub repository.

Protected branches default to `main master develop release/*`; set your own list with
`sprawl config set protected_branches …`. A repository can add branches — never remove them:

```sh
git config sprawl.protectedBranches "production staging"
```

Decisions other than allow are logged to `~/.local/state/sprawl/guard.log` (`0600`, credentials
masked) with the layer that made them. `sprawl doctor` also suggests `permissions.deny` rules so
agents cannot edit sprawl's own files with their file tools.

## Configuration

`~/.config/sprawl/config.toml` (unknown keys and invalid values are errors):

```toml
schema = 1
command = "ai"                               # set at install; change with: config set command
aliases = ["sra"]
default_provider = "claude"
shared_mcp = ["codegraph"]                   # MCP servers copied from main into new accounts
protected_branches = ["main", "release/*"]   # repositories can only add to this list
git_hooks = true                             # layer 2 of the push guard
update_check = true                          # daily background check, notice only
update_channel = "stable"                    # or "beta" for pre-releases
```

`sprawl config [--json]` shows every setting; `sprawl config get|set <key> [value]` reads or
changes one.

MCP servers whose config has a non-empty `env` or `headers` are never copied; add those per account.

| Path | Contents |
|---|---|
| `~/.local/share/sprawl/` | binary, plugin, local plugin marketplace, `git-hooks/`, `previous/` (rollback) |
| `~/.local/state/sprawl/` | guard log, migration backups |
| `~/.cache/sprawl/` | login status cache, update check |

## Exit codes

`0` ok · `1` error, or "no" at a confirmation · `2` usage or invalid input · `130` cancelled
(ctrl-c, esc) · `deps` passes the package manager's status through. Commands with `--json` print
machine-readable output on stdout.

## Migrating from squad

Installing over squad (the previous name) adopts its settings but not its command names — you get
`sprawl` + `sra` unless you choose otherwise — replaces the `squad@squad` plugin with `sprawl@sprawl`
in every account, removes its command links, still honours `git config squad.protectedBranches`,
moves its guard log to `~/.local/state/sprawl/legacy-logs/` and backs its files up under
`~/.local/state/sprawl/legacy-squad-*`.

## Releasing (maintainer)

Source and releases live in two repositories (ADR 0007): this one (private, source) and
`hoangthaininh/sprawl-releases` (private, binaries only; users are invited there with read
access). Release steps, the secrets they depend on, renewing the token and handling the signing
key: [docs/maintaining.md](docs/maintaining.md). A monthly `maintenance` workflow checks those
secrets and fails (GitHub emails you) before anything expires.

## Development

```sh
make build      # bin/sprawl
make check      # lint, race tests, coverage gates, plugin validation
make vuln       # govulncheck
```

Architecture: [docs/architecture.md](docs/architecture.md) · decisions: [docs/adr](docs/adr) ·
[CHANGELOG](CHANGELOG.md). Actions are pinned by commit.
