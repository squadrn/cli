# 0007 — Private distribution and updates

## Status

Accepted (1.0.0).

## Context

The source stays private. Users (friends, colleagues, people the author invites) must be able to
install and update without seeing the source, and access must be revocable. Updates should be
announced, never forced, the way Claude Code does it.

Installing from the source repository would require read access to it, which is access to the
code. GitHub artifact attestations are not available for private repositories on personal plans,
so they cannot be the proof that a release is genuine. macOS ships LibreSSL without ed25519, so
`openssl` cannot verify signatures there.

## Decision

1. **Two repositories.** Source in `OWNER/cli`; releases (archives, checksums,
   signature, `install.sh`, notes from the CHANGELOG) in `OWNER/sprawl-releases`, which holds no
   source. Users install with `gh release download … install.sh | sh`; gh handles the
   authentication, so squadrn never stores a token. (A GitHub CLI extension would read better, but
   gh requires such repositories to be named `gh-*`.) Users are read collaborators of the releases
   repository only. CI publishes there with a fine-grained token limited to that repository.
   Revoking access stops updates.
2. **Signed releases.** CI signs `checksums.txt` with an ed25519 key in the SSH signature format
   (`ssh-keygen -Y sign -n squadrn-release`). Public keys are committed in
   `internal/release/signers.pub` and embedded in every build and in `install.sh`. `self-update`
   verifies in Go (stdlib only) and refuses unsigned or wrongly signed releases; `install.sh`
   verifies with `ssh-keygen -Y verify`, present on macOS and Linux. Several keys may be listed to
   rotate. Attestations are still produced and checked where GitHub supports them.
3. **Notices, not forced updates.** A detached `self-update --background` refreshes
   `~/.cache/squadrn/update.json` at most once a day; at a terminal, commands show a one-line notice
   at most every 6 hours. Never in hooks, CI, `--json` or non-terminals; opt out with
   `update_check = false` or `SQUADRN_NO_UPDATE_CHECK`. Channels: stable, beta (pre-releases).
4. **Reversible.** Each install keeps the previous release in `~/.local/share/squadrn/previous/`
   (`self-update --rollback`); `self-uninstall` removes everything squadrn installed.
5. **License.** Proprietary terms ship with every archive.

## Consequences

- Users need a GitHub account and `gh`. A self-hosted download service with per-user tokens can
  replace the releases repository later without changing the signature or update logic.
- Losing the signing key forces every user to reinstall once with `install.sh`; the key is backed
  up outside GitHub (pass or an encrypted file).
- Anything handed to users can be copied; the license, not technology, covers redistribution.

## Addendum (1.2.0): what the signature covers

`checksums.txt` also lists `install.sh` and `install.ps1`, so the signature covers the installers
too. That protects copies checked by hand (download the installer with `checksums.txt` and
`checksums.txt.sig`, then `ssh-keygen -Y verify` as `install.sh` does). The one-line install
pipes the installer from `gh` straight into a shell, so nothing verifies the installer itself
before it runs: that first step trusts GitHub, the releases repository's write access and the
user's gh login. Everything the installer then downloads is verified against the keys embedded
in it, and every later update against the keys embedded in the installed binary.

Each installer is rendered with the tag of the release it ships in and installs that release by
default, so `gh release download vX.Y.Z -p install.ps1` installs vX.Y.Z even when the latest
stable release has no build for the platform. Releases are published as drafts and made visible
only after the release workflow's upgrade jobs (install the previous release, update, roll back)
pass against the draft.
