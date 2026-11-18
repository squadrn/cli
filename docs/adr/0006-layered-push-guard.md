# 0006 — Layered push guard

## Status

Accepted (1.0.0).

## Context

The pre-tool hook was the only guard and decided from the command text. A review reproduced
bypasses that all came back *allow*: quoting (`pu''sh`), case (`Git push`), git's option prefixes
(`--forc`), `-c alias.p=push`, repository aliases, `heads/main`, wildcard refspecs, here-strings,
`| bash`, brace expansion, and a repository's `sprawl.protectedBranches` replacing the user's list.
Timeouts and git errors also ended in *allow*. Text analysis of a Turing-complete shell cannot be
complete; the question is what happens when it is not.

## Decision

1. **Layer 1 fails closed.** Words are normalised the way the shell would (quotes, escapes,
   `$'…'`, case); options resolve by prefix like git; aliases (command line and repository) and
   `push.default`/`remote.*.push`/upstream are resolved. Anything only known at runtime, any parse
   error, git failure or the 3 s deadline yields *ask*. No text pre-filter.
2. **Layer 2 sees the real refs.** Sessions sprawl starts get `core.hooksPath` through
   `GIT_CONFIG_COUNT/KEY/VALUE` (appended, never replacing the user's entries). The shim calls
   `sprawl hook git <name>`; `pre-push` judges each ref update (protected destination, deletion,
   non-fast-forward via `merge-base --is-ancestor`) and *denies* anything it cannot verify, then
   chains to the repository's own hook with the same arguments and input. Other hook names only chain.
   Overriding `core.hooksPath`, `--no-verify`, or unsetting/exporting `GIT_CONFIG_*` in a command is
   denied or asked about in layer 1.
3. **Layer 3 is the server.** `doctor` checks GitHub branch protection of the current repository.
4. **Policy is additive.** Repository config can add protected branches, never remove them.

## Consequences

- Some legitimate commands now ask (interpreters with inline code, `xargs`, `bash -s`). That is the
  intended trade: a prompt is cheap, a force push to `main` is not.
- Every git hook in a sprawl session starts the sprawl binary (~5 ms). Hot-path hooks
  (`reference-transaction`, `post-index-change`, `fsmonitor-watchman`) are not shimmed, so a
  repository's own hooks of those names do not run inside sprawl sessions.
- Pushes started outside sprawl sessions are only stopped by layer 3.

## Hardening 2026-11

A security audit found commands that reached git or GitHub without a decision. Layer 1 now also:

- **Aliases and unknown subcommands.** `git <name>` that is neither a built-in nor an alias visible
  in `-c` or the repository config asks (it may be defined earlier on the same line, or be an
  external `git-<name>`). Writing `alias.*` with `git config` (any scope, `-f`, `git config set`)
  asks.
- **Settings and variables that run programs.** `-c` or `git config` writes of command-valued keys
  (`core.fsmonitor|sshCommand|pager|editor|askPass|hooksPath|gitProxy`, `sequence.editor`,
  `credential.*helper`, `diff.external`, `diff.*.command|textconv`, `filter.*`, `merge.*.driver`,
  `gpg.*program`, `protocol.*.allow`, `include.path`, `includeIf.*`, `alias.*`, `pager.*`,
  `remote.*.uploadpack|receivepack|vcs`, `uploadpack.packObjectsHook`, …) ask for any subcommand;
  reads stay allowed. Setting, exporting or unsetting `GIT_DIR`, `GIT_WORK_TREE`, `GIT_CONFIG_*`,
  `GIT_EXEC_PATH`, `GIT_TEMPLATE_DIR`, `GIT_SSH(_COMMAND)`, `GIT_EDITOR`, `GIT_SEQUENCE_EDITOR`,
  `GIT_PAGER`, `GIT_ASKPASS`, `GIT_EXTERNAL_DIFF`, `GIT_PROXY_COMMAND` before any command, and
  `env -i`, ask. Other `GIT_*` (`GIT_TRACE`, `GIT_TERMINAL_PROMPT`) stay allowed, also in PowerShell.
- **Policy is the user's.** `sprawl|sra|sprawl-hook|<configured names> config set
  protected_branches|git_hooks` is denied, as are writes (redirections, `tee`, `sed -i`, `cp`/`mv`/
  `ln`/`install`, `rm`, `chmod`, `truncate`, `Set-Content`/`Out-File`/`Copy-Item`/`Remove-Item`, …)
  to the config file or the git hooks; other writes into sprawl's config, data or state dirs ask.
  Layer 2 no longer reads the policy from the pushing process's environment: the shims pass the
  config file's absolute path (`sprawl hook git --config <file> <hook>`), fixed at install;
  repository additions (`git config sprawl.protectedBranches`) still apply.
- **Executors.** `trap` bodies are judged; `busybox <applet>` is the applet; `git-bash`, `bash.exe`
  and `sh.exe` are shells; `git-cmd`, `ssh`, `tmux` (commands that run something), `expect`, `su`,
  `xargs` and `parallel` ask when their words mention git or push; `source`/`.` ask except for the
  user's shell rc files; inline code of interpreters, `awk` (`system`, pipes), `sed` (`e`) and
  editors asks when it mentions git (no longer for "push" alone). Words that look like shell push
  aliases (`gp`, `gpf!`, `ggpush`, `gpsup`, …) ask.
- **GitHub CLI.** `gh api` writes (a method other than GET, or fields) to `git/refs`, `merges`,
  `branches/*/rename` or `*/protection` are denied, other writes ask; `gh repo sync --force` and
  `gh pr merge --admin` are denied; `gh repo sync`, `gh repo delete`, `gh repo edit
  --default-branch`, ruleset changes, `gh alias set` and unknown gh commands (aliases, extensions)
  ask.
- **Transports and mapping.** `git remote-<x>` and `git http-push` are denied like `send-pack`;
  `git subtree push <remote> <ref>` is judged as a push to `<ref>`; `git p4 submit` and
  `git svn dcommit` ask. A refspec without a colon is mapped like git does (`remote.<r>.push`,
  upstream with `push.default=upstream`); pushing to a remote with `remote.<r>.mirror` is denied;
  writing push configuration (`push.default`, `remote.*.push|mirror`, `branch.*.merge|remote|
  pushRemote`, `url.*.(push)insteadOf`, `git branch -u`) before a push on the same line asks.
  `--git-dir`/`GIT_DIR` select the repository that is judged; one that cannot be resolved asks.
- **Fewer prompts.** `git push origin "$(git branch --show-current)"` (and the `rev-parse
  --abbrev-ref HEAD` / `symbolic-ref --short HEAD` forms) is judged as the current branch; a
  destination like `HEAD~1` is allowed because git rejects it.
- **PowerShell** command names are matched without path or `.exe` (`C:\Windows\System32\cmd.exe`,
  `bash.exe`, `git.exe`); `env:`, `env:\`, `env:/`, `$env:` forms are normalised.

`doctor` runs the installed hook through the command its `hooks.json` declares (on every platform),
requires the git hook shims to match byte for byte and be executable, and reads branch protection:
force pushes or deletions allowed on the default branch warn, as does `enforce_admins` off; a
ruleset with `non_fast_forward` and `deletion` rules also counts as protected.

The transport-level layer 2 that `--no-verify` cannot skip is not part of this round.
