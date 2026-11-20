# End-to-end smoke harness

`make e2e` (CI workflow `e2e`, on macOS, Linux and Windows) installs a build of this checkout
through the real installer and drives the main flows with the installed command, the way a user
would. It complements the unit tests and the `testdata/script` scripts, which call the CLI in
process; here every step is a separate run of the installed binary.

    make e2e                                   # go test -tags e2e -count=1 -v ./e2e/
    go test -tags e2e -run 'TestE2E' ./e2e/    # the same, quietly

Needs Go, git 2.31+, ssh-keygen and, on Unix, tmux (the grid section is skipped without it).
On Windows it uses Windows PowerShell for `install.ps1`. It takes about 10 seconds plus the build.

## What it does

1. **Release**: builds `cmd/squadrn` as version 0.99.0, packs it with the plugin like the
   goreleaser archive for this platform, writes `checksums.txt`, signs it with a throwaway
   ed25519 key and renders `install.sh` / `install.ps1` to trust exactly that key (as
   `scripts/render-install.sh` does). The binary is built with `-X …/internal/cli.githubAPI` set
   to a local fake release server, so `self-update --check` never reaches the network.
2. **Sandbox**: a temp dir with its own `HOME`/`USERPROFILE`, `XDG_*` dirs, git config
   (`GIT_CONFIG_GLOBAL`, `GIT_CONFIG_NOSYSTEM`), a private tmux socket dir under `/tmp` and a
   local bare remote. The developer's `GIT_*`, `CLAUDE_*`, `SQUADRN_*`, `GH_*`, tokens and PATH
   entries under the real home are dropped. PATH starts with the fakes, then `~/.local/bin` of
   the sandbox. Commands run without a terminal (Unix: a new session; Windows: the installed commands
   are detached from the console), so prompts that fall back to the terminal behave as on a CI runner. On macOS a failing `security` stub keeps the login Keychain out of reach.
3. **Flows** (`e2e_test.go`, one subtest per section, in order): install (the first-install
   summary, `srn setup` without a terminal and, on Unix, answered at a pseudo terminal through
   `script(1)`), help/version/
   completion, config and command rename, accounts (add, ls, login, rename, adopt, `--relink`,
   rm), running as an account, Codex accounts (add, login, run, subcommands, the guard hook,
   layer 2), worktrees (wt, deps, in-use refusal, wt rm, gc), grid/ps/stop
   (Unix), usage (status line, limit hook, setup, undo), swap (no argument, `--session`,
   divergence, `--worktree`), the guard (layer 1 decisions; layer 2 with real pushes to the bare
   remote from a session: a feature branch goes through, `main` is denied by the pre-push hook),
   doctor and `srn setup -y` (twice: idempotent), `self-update --check`, `self-uninstall --purge`.

Each step asserts the exit code and key output. The first failure stops the run and prints the
transcript of every command so far (directory, environment overrides, exit code, stdout, stderr).

The guard section pushes for real, but only from inside the sandbox to its local bare remote,
through the hooks the installed squadrn wires into sessions. It never needs (or tries) to get
past a guard installed on the machine running it.

## Fake agent CLIs

The fakes live in `internal/fakeagent`, shared with the `testdata/script` scripts. A test binary
whose `TestMain` calls `fakeagent.Main()` first becomes the fake when it is started under the
CLI's name (`claude`, `claude.exe`) or with `FAKE_AGENT=<name>`. The harness puts one per
registered fake into the sandbox's `fakebin/`: a wrapper script on Unix
(`FAKE_AGENT=<name> exec <test binary>`), a copy named `<name>.exe` on Windows.

### Contract every fake follows

- The account is the provider's config-dir variable (Claude: `CLAUDE_CONFIG_DIR`), unset meaning
  the main account in its default dir under `$HOME`.
- State lives in files of that dir, never anywhere else: logged in when `.loggedin` exists (with
  `.org` and `.plan` for the organization and plan), plugin and MCP registries as small files.
- Queries squadrn makes (auth status/login/logout, plugin and MCP management) are
  answered from that state; login and logout and removals are appended to `$HOME/calls.log`.
- Any other invocation is a session: print `cfg=<config dir or <unset>> args=<args>` and append
  `cfg=… pwd=<physical cwd> args=…` to `$HOME/calls.log`, then hand over to `session()` for:
  - `FAKE_HOLD=<session>`: stay alive, listed in the account's session registry (not with
    `FAKE_NOREG`), pid in `$HOME/held.txt`; SIGINT/SIGTERM log `stopped <pid>` and exit 0.
  - `FAKE_EXEC=<JSON array>`: run that command in the session's environment and directory and
    exit with its status (the guard section pushes this way).
  - inside a tmux pane (`TMUX_PANE`): stay alive like an interactive session.
- `FAKE_LOGIN_FAILS=1` makes login leave the account logged out.
- No network, no real credentials, no keychain.

`codex` (`fakeagent.Codex`) follows the same contract with `CODEX_HOME` (default `~/.codex`):
`login status` prints on stderr like Codex, and `app-server` answers the stdio JSON-RPC methods
squadrn uses (`account/read`, `account/rateLimits/read` from `.limits`, `hooks/list` and
`config/batchWrite` for the guard hook's trust).

### Adding the fake of a new provider

1. Add `internal/fakeagent/codex.go` with `func Codex(args []string) int` implementing the
   contract for that CLI: its config-dir variable (`CODEX_HOME`, default `~/.codex`), its auth
   status output as squadrn's provider package parses it, and `session(<registry dir or "">)`
   for everything else.
2. Register it in `agents` in `fakeagent.go` (`"codex": Codex`). The harness and the scripts'
   `TestMain` then provide it automatically; `fakeagent.Names()` drives the harness.
3. For the scripts, add it to `TestScript`'s setup the way `claude` is (wrapper on Unix,
   `cmds["codex"]` on Windows).
4. Add an e2e section (or extend `accounts`, `run`, `swap`) for `codex/<name>` accounts. Keep
   provider specifics out of `harness_test.go`.
