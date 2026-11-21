# 0015 — Provider selection

Status: accepted

## Context

Squadrn ships one binary per platform with every provider built in. Measured on v1.0.0-rc.5
(darwin/arm64, `-s -w`): the full binary is 10,330,176 bytes, a build with only the Claude
provider 10,142,576 — Codex and Gemini cost 188 KB, 72 KB gzipped (1.7% of a 4 MB archive).
Per-provider downloads would save nothing worth a second artifact, signature and update path.

The cost users see is elsewhere. A provider's main account exists as soon as its dir does
(`~/.codex`, `~/.gemini`), so:

1. the installer and `self-update` write squadrn's guard into `~/.codex/hooks.json` and
   `~/.gemini/settings.json` of a CLI tried once and never used with squadrn, and warn on every
   update once that CLI is gone from PATH;
2. `codex/main` and `gemini/main` show up in the account list, completion, routing and setup;
3. `doctor` reports every agent CLI on PATH.

Comparable installers (CodeGraph's `codegraph install`, `npx skills`, ruler, rulesync, rustup)
ask which targets to configure in a multi-select pre-filled from detection, take one
comma-separated flag (`--target`, `--agents`) with `auto`/`all`, never prompt with `--yes` or
without a terminal, store the choice, and change it later with a symmetric command.

## Decision

### 1. The `providers` setting

```toml
schema = 2
providers = ["claude", "codex"]
```

- The providers squadrn manages, in display order. Not empty, no duplicates, and it contains
  `default_provider` when that is set.
- An id this release does not know (a config written by a newer one) is kept and ignored;
  `doctor` warns.
- The config schema becomes 2. A release before this one refuses it (`self-update --rollback`
  past this release): delete the `schema` and `providers` lines to go back.
- Absent: a config written before this ADR. It is migrated (§5); until then every provider is
  enabled, as before.

### 2. What respects it

The account store lists only enabled providers. That one filter covers the account list, `get`
and routing (`srn codex/work`), passthrough, completion and suggestions, setup's adopt step,
the plugin and hook installation of the installer and `self-update`, and doctor's account checks.
A reference to an account of a disabled provider fails: `codex is not enabled (srn provider
enable codex)`.

Deliberately not filtered:

- **hooks** (guard, usage, notifications): a squadrn hook left in a disabled provider's account
  still guards, failing closed (ADR 0006);
- `self-uninstall`, which cleans every provider;
- the help text, which documents what squadrn supports.

### 3. Changing it

```
srn provider                    providers: enabled or not, CLI version or "not found", accounts
srn provider enable <id>...     enable; install the guard into the provider's accounts
srn provider disable <id>...    disable; remove squadrn's hooks from its accounts (asked when
                                it has accounts; -y); folders and logins stay
```

- Disabling the last provider is refused; a disabled `default_provider` moves to the first
  remaining one.
- `config get providers` prints the list; `config set providers` is refused with a pointer to
  `srn provider`, because the change installs or removes hooks.
- `account add <provider>/<name>` of a disabled provider enables it (an explicit intent).

### 4. The installer

Questions, at a terminal, on a first install only:

| # | Question | Default |
|---|---|---|
| 1 | Which agent CLIs will you use with Squadrn? (multi-select) | see below |
| 2 | Install as `srn` (alias `squadrn`)? Yes / Choose names | Yes |
| 3 | `~/.local/bin` is not on your PATH. Add it to `~/.zshrc`? (macOS, Linux; only when needed) | Yes |

**Pre-selection**: Claude, plus every provider whose CLI is on PATH and that has a managed
account or its main dir. A CLI that is detected but never used is listed, not selected.

Flags (install.sh option, environment variable, install.ps1 parameter; all passed to
`self-install`):

| | |
|---|---|
| `--providers claude,codex\|auto\|all` | `SQUADRN_PROVIDERS`, `-Providers` |
| `--no-modify-path` | `SQUADRN_NO_MODIFY_PATH=1`, `-NoModifyPath` |
| `--yes` | `SQUADRN_YES=1`, `-Yes`: ask nothing, take the defaults |

An unknown id is a usage error listing the known ones. Without a terminal (`curl | sh` in CI) the
installer takes `auto` and **stores the result**, so a CLI installed later never changes what
squadrn touches; `doctor` says `gemini CLI found, not enabled (srn provider enable gemini)`.
Re-running the installer keeps the stored list unless `--providers` is given.

**PATH**: the line goes into the rc file of the user's login shell (zsh `~/.zshrc`, bash
`~/.bashrc`, or `~/.bash_profile` on macOS, fish `~/.config/fish/conf.d/squadrn.fish`) inside a
marked block, written atomically and idempotently:

```
# >>> squadrn path >>>
export PATH="$HOME/.local/bin:$PATH"
# <<< squadrn path <<<
```

`self-uninstall` removes the block. Windows already sets the user PATH in `install.ps1`. Package
`internal/shellrc` owns marked blocks (`Ensure`, `Remove`), shared with shell completion.

Not asked, and why: completions (written automatically, harmless); profile, usage tracking,
notifications, adopting dirs (per account: `srn setup` and the account wizard, ADR 0014); update
channel (follows the release tag); telemetry (there is none).

### 5. Existing installs

The first install or `self-update` that finds no `providers` in an existing config stores:
`default_provider`, every provider with a managed secondary account, and every provider whose
CLI is on PATH and whose main dir exists. That is today's behaviour minus providers whose CLI is
gone. It says so once:

```
providers: claude, codex, gemini — not using one? srn provider disable gemini
```

Nothing is removed without asking.

### 6. Edge cases

| Case | Behaviour |
|---|---|
| No agent CLI on PATH | Claude is selected; the installer prints the install hints |
| Nothing selected | refused: pick at least one |
| CLI installed later | `doctor` info line; `srn provider enable`, or `account add` enables it |
| squadrn hook left in a disabled provider's account | it still guards; `doctor` warns with the fix |
| `default_provider` disabled | moves to the first remaining provider, said |
| unknown id in the config | ignored, `doctor` warns |

## Plan

1. This ADR.
2. Config, store filter, migration, `srn provider`, the routing error.
3. `doctor`: enabled-only install checks, the info line, drift and unknown-id warnings.
4. Installer flags, the stored no-terminal choice, the PATH block (`internal/shellrc`).
5. The installer questions on the prompt components of ADR 0014.
6. `srn setup` and the account wizard use the same provider choice; `account add` enables a
   disabled provider (until then it fails with the `provider enable` fix).

## Consequences

- Squadrn no longer writes into the config of an agent CLI the user did not choose.
- A new config key and a new command; `config set providers` is not allowed.
- The installer may edit one shell rc file, inside a marked block, unless `--no-modify-path`.
