#!/bin/sh
# Renders build/install.sh and build/install.ps1 for a release: the releases repository and the
# signing keys are written into them, so the installers trust exactly what this build trusts.
#   scripts/render-install.sh OWNER/REPO
set -eu
repo=${1:?usage: scripts/render-install.sh OWNER/REPO}
mkdir -p build
grep '^ssh-ed25519 ' internal/release/signers.pub >build/signers || : >build/signers
for f in install.sh install.ps1; do
  awk -v repo="$repo" '
    $0 == "__SPRAWL_SIGNERS__" { while ((getline line < "build/signers") > 0) print line; close("build/signers"); next }
    { gsub(/__SPRAWL_REPO__/, repo); print }
  ' "scripts/$f" >"build/$f"
done
rm -f build/signers
