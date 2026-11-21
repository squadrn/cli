# 0007 — Public distribution and updates

Status: accepted (v1.0.0)

## Context

The source is public under the Apache License 2.0 in `squadrn/squadrn-cli`. Anyone should be able to
install and update with one command, without a GitHub account or `gh`, and still get only
releases signed by the project. Updates should be announced, never forced, the way Claude Code
does it. macOS ships LibreSSL without ed25519, so `openssl` cannot verify signatures there.

## Decision

1. **One public repository.** Source and releases (archives, `checksums.txt`, its signature,
   `install.sh`, `install.ps1`, notes from the CHANGELOG) live in `squadrn/squadrn-cli`. The `release`
   workflow publishes with the workflow's own `GITHUB_TOKEN` (`contents: write` on that job only);
   no personal access token exists.
2. **One-line installers over HTTPS.**
   `curl -fsSL https://github.com/squadrn/squadrn-cli/releases/latest/download/install.sh | sh` and
   `irm https://github.com/squadrn/squadrn-cli/releases/latest/download/install.ps1 | iex`. Each installer
   is rendered with its release's tag and the trusted public keys, installs that release by
   default (`--version`/`-Version` or `SQUADRN_VERSION` for another), and downloads from
   `github.com/squadrn/squadrn-cli/releases/download` with curl, wget or `Invoke-WebRequest`.
3. **Signed releases.** CI signs `checksums.txt` with an ed25519 key in the SSH signature format
   (`ssh-keygen -Y sign -n squadrn-release`). Public keys are committed in
   `internal/release/signers.pub` and embedded in every build and installer. `self-update`
   verifies in Go (stdlib only) and refuses unsigned or wrongly signed releases; the installers
   verify with `ssh-keygen -Y verify`. Several keys may be listed to rotate. GitHub artifact
   attestations are produced too; the installers do not check them (verify by hand with
   `gh attestation verify`), `self-update` checks them as an extra when `gh` is installed and
   logged in.
4. **Updates from the REST API.** `self-update` and the background check read
   `api.github.com/repos/squadrn/squadrn-cli/releases` and download asset URLs with Go's `net/http`
   (environment proxy settings, timeouts). No token is needed; `GH_TOKEN`, `GITHUB_TOKEN` or gh's
   login only raise the rate limit (and let the release workflow test its own drafts). The
   repository is fixed at build time (`-X …/cli.Repo=squadrn/squadrn-cli`); only `--repo` overrides it.
5. **Notices, not forced updates.** A detached `self-update --background` refreshes
   `~/.cache/squadrn/update.json` at most once a day; at a terminal, commands show a one-line
   notice at most every 6 hours. Never in hooks, CI, `--json` or non-terminals; opt out with
   `update_check = false` or `SQUADRN_NO_UPDATE_CHECK`. Channels: stable, beta (pre-releases).
6. **Placement in Go.** The installers only download, verify and extract; `squadrn self-install`
   does everything else (layout, command names, completions, plugin in every account), so
   it is covered by tests and shared by `self-update`. The installed data dir doubles as a local
   plugin marketplace that every account registers, and the release version is stamped into the
   installed `plugin.json`, so Claude Code sees every release as a plugin update.
7. **Reversible.** Each install keeps the previous release in `~/.local/share/squadrn/previous/`
   (`self-update --rollback`); `self-uninstall` removes everything squadrn installed.

## Consequences

- The one-line install pipes the installer straight into a shell, so that first step trusts
  GitHub and the repository's write access. `checksums.txt` lists both installers, so a copy
  checked by hand (`ssh-keygen -Y verify` as `install.sh` does) is covered by the signature.
  Everything the installer then downloads is verified against the keys embedded in it, and every
  later update against the keys embedded in the installed binary.
- Releases are published as drafts and made visible only after the upgrade jobs (install the
  previous release, update to the draft, roll back) pass.
- Anonymous API use is rate limited (60 requests an hour per IP); one update check costs one
  request, an update two plus its downloads.
- Losing the signing key forces every user to reinstall once; the key is backed up outside GitHub.
- Signing verifies the new signature against `signers.pub` (`scripts/sign.sh`), so a signing
  secret that installed copies would refuse fails the release while it is a draft.
