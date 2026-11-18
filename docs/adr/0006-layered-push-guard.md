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
