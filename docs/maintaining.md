# Maintaining

Runbook for the owner: releases, the secrets they depend on, and what to do when one expires or
is lost. Design: [ADR 0007](adr/0007-private-distribution.md).

## What releases depend on

| Name | Where | What | Expires / lost |
|---|---|---|---|
| `SPRAWL_RELEASES_REPO` | variable, source repo | `hoangthaininh/sprawl-releases` | — |
| `SPRAWL_RELEASES_TOKEN` | secret, source repo | fine-grained token, *Contents: read and write* on the releases repository only, named `sprawl-releases-publish` | on the date chosen when it was created — [renew](#renew-the-release-token) |
| `SPRAWL_SIGNING_KEY` | secret, source repo | private ed25519 key that signs `checksums.txt` | never; backups below — [if lost](#signing-key-lost) |
| signing key backup | your password manager (and `pass` if installed) | copy of the private key | keep it there; GitHub cannot show a secret again |
| `internal/release/signers.pub` | source repo | public keys every build trusts | — |

**Reminders are automatic.** The `maintenance` workflow runs on the 1st of every month (and on
demand: *Actions → maintenance → Run workflow*). It fails — and GitHub emails you — when the
token cannot reach the releases repository, expires within 21 days, or the signing key no longer
matches `signers.pub`. GitHub also emails 7 days before a personal access token expires.

## Release

1. Add a `## X.Y.Z` section at the top of `CHANGELOG.md`; it becomes the release notes.
2. Commit (Conventional Commits, see AGENTS.md) and push `main`.
3. `git tag -a vX.Y.Z -m vX.Y.Z && git push origin vX.Y.Z` (`vX.Y.Z-rc.N` reaches the beta
   channel only).
4. The `release` workflow runs the full CI matrix, then builds, signs and publishes to the
   releases repository. Users get a notice within a day.

Pre-releases (`vX.Y.Z-rc.N`) need their own `## X.Y.Z-rc.N` CHANGELOG section; installs of a
pre-release follow the beta channel.

### Windows check (releases that touch Windows code)

CI installs, updates over a running command, rolls back and uninstalls on `windows-latest`. Before
announcing a release that changes Windows behaviour, also check by hand on Windows 10 and 11, as a
standard (non-admin) user with Defender on:

- `install.ps1` in Windows PowerShell 5.1 and PowerShell 7; a tampered `checksums.txt` is refused
- a new terminal finds `sprawl`; `sprawl doctor` is green
- `sprawl account add`/`login`, run as an account, ctrl-c in a session returns to the prompt
- `sprawl wt`, `sprawl gc`
- push to `main` from the Bash tool, the PowerShell tool and the plain terminal of a session:
  denied or asked each time
- `sprawl self-update` while a session runs; `--rollback`; `self-uninstall` leaves nothing behind
- colours and the account picker in Windows Terminal and the classic console
- `scripts/windows-spike.ps1` once per Claude Code major version (hook and tool behaviour)

If the release workflow fails before publishing, fix it, push `main`, then move the tag:
`git push origin :refs/tags/vX.Y.Z && git tag -d vX.Y.Z`, tag again and push it.

## Users

- Invite: releases repository → *Settings → Collaborators → Add people*, role **Read**. They
  install with the command in that repository's README.
- Revoke: remove the collaborator. Their copy keeps working but gets no updates.
- Never invite anyone to the source repository.

## Renew the release token

1. https://github.com/settings/personal-access-tokens → `sprawl-releases-publish` →
   *Regenerate token* (or create a new one: resource owner `hoangthaininh`, only
   `sprawl-releases`, *Contents: read and write*).
2. Copy it, then: `pbpaste | gh secret set SPRAWL_RELEASES_TOKEN --repo hoangthaininh/sprawling-agents`
3. Copy something else to clear the clipboard.
4. *Actions → maintenance → Run workflow* to confirm.

## Signing key

### Backups

Losing only the backup is harmless: CI signs with the secret, which stays on GitHub. A lost
**secret without a backup** is what hurts: a new key would not be trusted by installed copies, and
every user would have to reinstall once with `install.sh`.

Keep a **spare key** so that never happens. Its public key is trusted by every build from the
next release on; CI keeps signing with the current key.

```sh
scripts/release-key.sh hoangthaininh/sprawling-agents --spare --backup ~/sprawl-spare-signing-key
# move the file into your password manager, delete it, commit signers.pub, release
```

### Signing key lost

- **Spare key exists** (in `signers.pub` of the release users run):
  `gh secret set SPRAWL_SIGNING_KEY --repo hoangthaininh/sprawling-agents < spare-key-file`, then
  create a new spare.
- **No spare:** `scripts/release-key.sh hoangthaininh/sprawling-agents --backup FILE`, remove the
  old public key from `signers.pub`, release, and ask users to reinstall once with `install.sh`.

### Rotate (key suspected leaked)

1. Create the new key as a spare (above), release once — still signed with the old key.
2. Put the new key into `SPRAWL_SIGNING_KEY`, remove the old public key from `signers.pub`,
   release again.
