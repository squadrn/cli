#!/bin/sh
# Signs a release file in CI (goreleaser `signs`): scripts/sign.sh FILE SIGNATURE.
# The key comes from SQUADRN_SIGNING_KEY and only ever exists in a private temporary file.
# The signature is then verified against internal/release/signers.pub, so a key that installed
# copies would refuse fails the release while it is still a draft.
# SQUADRN_ALLOW_UNSIGNED=1 skips signing for local snapshot builds.
set -eu
file=$1
signature=$2
signers=$(dirname "$0")/../internal/release/signers.pub
if [ -z "${SQUADRN_SIGNING_KEY:-}" ]; then
  if [ "${SQUADRN_ALLOW_UNSIGNED:-}" = 1 ]; then
    echo "sign.sh: SQUADRN_SIGNING_KEY not set; $file left unsigned" >&2
    : >"$signature"
    exit 0
  fi
  echo "sign.sh: SQUADRN_SIGNING_KEY is not set; releases must be signed" >&2
  exit 1
fi
umask 077
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT INT TERM
printf '%s\n' "$SQUADRN_SIGNING_KEY" >"$tmp/key"
cp "$file" "$tmp/msg"
ssh-keygen -Y sign -q -f "$tmp/key" -n squadrn-release "$tmp/msg"
grep '^ssh-ed25519 ' "$signers" | sed 's/^/squadrn /' >"$tmp/allowed_signers"
if ! ssh-keygen -Y verify -f "$tmp/allowed_signers" -I squadrn -n squadrn-release -s "$tmp/msg.sig" <"$tmp/msg" >/dev/null 2>&1; then
  echo "sign.sh: SQUADRN_SIGNING_KEY is not in internal/release/signers.pub; installed copies would refuse this release (docs/maintaining.md)" >&2
  exit 1
fi
mv "$tmp/msg.sig" "$signature"
