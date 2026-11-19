#!/bin/sh
# Creates the release signing key (ed25519, SSH signature format). Run it once, yourself:
#
#   scripts/release-key.sh OWNER/cli [--backup FILE]
#   scripts/release-key.sh OWNER/cli --spare --backup FILE
#
# --spare creates a standby key: its public key is trusted by every build from the next release
# on, but CI keeps signing with the current key. If the current key is ever lost, put the spare
# into the secret (gh secret set SQUADRN_SIGNING_KEY < FILE) and nobody has to reinstall.
#
# The private key never reaches the terminal: it is stored as the GitHub Actions secret
# SQUADRN_SIGNING_KEY of the source repository, in pass (squadrn/release-signing-key) when pass is
# installed, and in FILE when --backup is given; then the temporary copy is deleted. The public
# key is appended to internal/release/signers.pub, which every build embeds.
#
# Keep a backup. Installed copies only trust the keys they were built with: losing the key means
# everyone must reinstall with install.sh once.
set -eu

die() { printf 'release-key.sh: %s\n' "$*" >&2; exit 1; }

repo=${1:-}
[ -n "$repo" ] || die "usage: scripts/release-key.sh OWNER/REPO [--backup FILE]"
shift
backup=""
spare=""
while [ $# -gt 0 ]; do
  case "$1" in
    --backup) [ $# -ge 2 ] || die "--backup needs a file"; backup=$2; shift 2 ;;
    --spare) spare=yes; shift ;;
    *) die "unknown option $1" ;;
  esac
done
[ -f internal/release/signers.pub ] || die "run from the repository root"
command -v ssh-keygen >/dev/null || die "ssh-keygen is required"
command -v gh >/dev/null || die "gh is required (logged in with admin access to $repo)"
if ! command -v pass >/dev/null && [ -z "$backup" ]; then
  die "no backup: install pass or give --backup FILE (an encrypted volume)"
fi
[ -z "$spare" ] || [ -n "$backup" ] || command -v pass >/dev/null || die "--spare needs a backup"

umask 077
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT INT TERM
kind=release
[ -z "$spare" ] || kind=spare
ssh-keygen -q -t ed25519 -N '' -C "squadrn-$kind-$(date +%Y%m%d)" -f "$tmp/key"

if [ -z "$spare" ]; then
  gh secret set SQUADRN_SIGNING_KEY --repo "$repo" <"$tmp/key"
fi
if command -v pass >/dev/null; then
  pass insert -m -f "squadrn/$kind-signing-key" <"$tmp/key" >/dev/null
  echo "private key stored in pass: squadrn/$kind-signing-key"
fi
if [ -n "$backup" ]; then
  [ ! -e "$backup" ] || die "$backup exists; refusing to overwrite it"
  cp "$tmp/key" "$backup"
  echo "private key backed up to $backup"
fi
cat "$tmp/key.pub" >>internal/release/signers.pub
if [ -z "$spare" ]; then
  echo "secret SQUADRN_SIGNING_KEY set on $repo"
else
  echo "spare key created; CI still signs with the current key. Commit signers.pub and release."
fi
echo "public key added to internal/release/signers.pub:"
cat "$tmp/key.pub"
