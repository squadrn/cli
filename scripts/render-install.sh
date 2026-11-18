#!/bin/sh
# Renders build/install.sh for a release: the releases repository and the signing keys are
# written into it, so the installer trusts exactly what this build trusts.
#   scripts/render-install.sh OWNER/REPO
set -eu
repo=${1:?usage: scripts/render-install.sh OWNER/REPO}
mkdir -p build
grep '^ssh-ed25519 ' internal/release/signers.pub >build/signers || : >build/signers
awk -v repo="$repo" '
  $0 == "__SPRAWL_SIGNERS__" { while ((getline line < "build/signers") > 0) print line; next }
  { gsub(/__SPRAWL_REPO__/, repo); print }
' scripts/install.sh >build/install.sh
rm -f build/signers
