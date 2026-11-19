# Maintaining

Runbook for the owner: releases, the secrets they depend on, and what to do when one expires or
is lost. Design: [ADR 0007](adr/0007-public-distribution.md).

## What releases depend on

| Name | Where | What | Expires / lost |
|---|---|---|---|
| `SQUADRN_SIGNING_KEY` | Actions secret, `squadrn/squadrn` | private ed25519 key that signs `checksums.txt` | never; backups below — [if lost](#signing-key-lost) |
| signing key backup | your password manager (and `pass` if installed) | copy of the private key | keep it there; GitHub cannot show a secret again |
| `internal/release/signers.pub` | this repository | public keys every build and installer trusts | — |

Publishing uses the workflow's own `GITHUB_TOKEN`; there is no token to renew. The `maintenance`
workflow runs on the 1st of every month (and on demand: *Actions → maintenance → Run workflow*)
and fails — GitHub emails you — when the signing key no longer matches `signers.pub`.

## Release

1. Add a `## X.Y.Z` section at the top of `CHANGELOG.md`; it becomes the release notes.
2. Commit (Conventional Commits, see AGENTS.md) and push `main`.
3. `git tag -a vX.Y.Z -m vX.Y.Z && git push origin vX.Y.Z` (`vX.Y.Z-rc.N` reaches the beta
   channel only).
4. The `release` workflow runs the full CI matrix, then builds, signs and creates a **draft**
   release in this repository (GoReleaser v2.20.2, pinned, with `GITHUB_TOKEN`). The `upgrade` jobs install
   the previous release (the highest published one below the tag on the same channel, by semver:
   `scripts/previous-release.jq`) with the new installer, `self-update` to the draft and, on
   Linux, roll back and forward. Only then does `publish` make the release visible, and
   `discover` installs it with the one-line installer and checks that `self-update --check` on
   the release's channel finds it. Users get a notice within a day.

The installers (`install.sh`, `install.ps1`) are rendered with the release's own tag and install
that release by default; their checksums are in the signed `checksums.txt`. Check what users
will get:

```sh
curl -fsSL https://github.com/squadrn/squadrn/releases/download/vX.Y.Z/install.sh | sh        # macOS, Linux
```

```powershell
irm https://github.com/squadrn/squadrn/releases/download/vX.Y.Z/install.ps1 | iex
```

Pre-releases (`vX.Y.Z-rc.N`) need their own `## X.Y.Z-rc.N` CHANGELOG section; installing a
pre-release switches an install without a chosen channel to the beta channel (`self-install`).

If `upgrade` fails, the draft stays invisible: fix the problem, delete the draft
(`gh release delete vX.Y.Z -R squadrn/squadrn --yes`), then move the tag (below). The upgrade jobs
read the draft with the workflow token (`contents: write`): GitHub shows drafts only to tokens
with push access.

### Windows check (releases that touch Windows code)

CI installs, updates over a running command, rolls back and uninstalls on `windows-latest`. Before
announcing a release that changes Windows behaviour, also check by hand on Windows 10 and 11, as a
standard (non-admin) user with Defender on:

- `install.ps1` in Windows PowerShell 5.1 and PowerShell 7, run as documented (`irm … | iex` and
  the script block form); a tampered `checksums.txt` is refused; a failure leaves the PowerShell
  window open
- a new terminal finds `squadrn`; `squadrn doctor` is green
- `squadrn account add`/`login`, run as an account, ctrl-c in a session returns to the prompt
- `squadrn wt`, `squadrn gc`
- push to `main` from the Bash tool, the PowerShell tool and the plain terminal of a session:
  denied or asked each time
- `squadrn self-update` while a session runs; `--rollback`; `self-uninstall` leaves nothing behind
- colours and the account picker in Windows Terminal and the classic console
- `scripts/windows-spike.ps1` once per Claude Code major version (hook and tool behaviour)

If the release workflow fails before publishing, fix it (and delete a leftover draft), push
`main`, then move the tag:
`git push origin :refs/tags/vX.Y.Z && git tag -d vX.Y.Z`, tag again and push it.

## Repository settings

- *Settings → Actions → General → Workflow permissions*: **Read repository contents** (workflows
  ask for more per job), and leave "Allow GitHub Actions to create and approve pull requests" off.
- *Settings → Code security*: **Private vulnerability reporting** on (SECURITY.md points to it);
  Dependabot alerts on.
- *Settings → Branches* (or a ruleset) for `main`: require a pull request, the CI checks and the
  `commits` check; block force pushes and deletion. Docs-only pull requests skip the `ci`
  workflow, whose jobs then never report: require `commits` alone, or require the `ci` jobs too
  and merge docs-only pull requests as an admin.

## Signing key

### First-time setup (macOS, with pass)

`scripts/release-key.sh` refuses to create a key without a backup: either `pass` or
`--backup FILE` on an encrypted volume. With `pass` (run in a terminal; GPG asks for passphrases):

```sh
# 1. tools
brew install pass gnupg pinentry-mac

# 2. passphrase prompts as a macOS dialog (can be remembered in the Keychain)
mkdir -p ~/.gnupg && chmod 700 ~/.gnupg
echo "pinentry-program $(brew --prefix)/bin/pinentry-mac" >> ~/.gnupg/gpg-agent.conf
gpgconf --kill gpg-agent

# 3. a personal GPG key: signing/certification + an encryption subkey, valid 2 years
gpg --quick-gen-key "Your Name <you@example.com>" ed25519 cert,sign 2y
FPR=$(gpg --list-keys --with-colons you@example.com | awk -F: '/^fpr/{print $10; exit}')
gpg --quick-add-key "$FPR" cv25519 encr 2y
# extend later: gpg --quick-set-expire "$FPR" 2y

# 4. the password store (git history is optional)
pass init you@example.com
pass git init

# 5. back up the GPG key: without it every secret in pass is lost
gpg --export-secret-keys --armor you@example.com > /Volumes/<offline-drive>/gpg-secret.asc
gpg --gen-revoke you@example.com > /Volumes/<offline-drive>/gpg-revoke.asc
# also copy ~/.password-store (already encrypted) somewhere safe

# 6. the release signing key: secret SQUADRN_SIGNING_KEY, pass entry
#    squadrn/release-signing-key, public key appended to internal/release/signers.pub
scripts/release-key.sh squadrn/squadrn

# 7. check without printing the key
pass ls squadrn
gh secret list -R squadrn/squadrn
```

Then commit `internal/release/signers.pub` through a pull request. Without `pass`, mount an
encrypted volume (`hdiutil create -encryption AES-256 -size 20m -fs APFS -volname Keys keys.dmg`,
then `hdiutil attach keys.dmg`) and run
`scripts/release-key.sh squadrn/squadrn --backup /Volumes/Keys/release-signing-key`.

### Backups

Losing only the backup is harmless: CI signs with the secret, which stays on GitHub. A lost
**secret without a backup** is what hurts: a new key would not be trusted by installed copies, and
every user would have to reinstall once with `install.sh`.

Keep a **spare key** so that never happens. Its public key is trusted by every build from the
next release on; CI keeps signing with the current key.

```sh
scripts/release-key.sh squadrn/squadrn --spare --backup ~/squadrn-spare-signing-key
# move the file into your password manager, delete it, commit signers.pub, release
```

### Signing key lost

- **Spare key exists** (in `signers.pub` of the release users run):
  `gh secret set SQUADRN_SIGNING_KEY --repo squadrn/squadrn < spare-key-file`, then
  create a new spare.
- **No spare:** `scripts/release-key.sh squadrn/squadrn --backup FILE`, remove the
  old public key from `signers.pub`, release, and ask users to reinstall once with `install.sh`.

### Rotate (key suspected leaked)

1. Create the new key as a spare (above), release once — still signed with the old key.
2. Put the new key into `SQUADRN_SIGNING_KEY`, remove the old public key from `signers.pub`,
   release again.
