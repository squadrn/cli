#!/bin/sh
# Renders build/install.sh and build/install.ps1 for a release: the releases repository, the
# release tag (the installers' default version) and the signing keys are written into them, so
# the installers install their own release and trust exactly what this build trusts.
#   scripts/render-install.sh OWNER/REPO TAG [SIGNERS_FILE]   (default: internal/release/signers.pub)
set -eu
repo=${1:?usage: scripts/render-install.sh OWNER/REPO TAG [SIGNERS_FILE]}
tag=${2:?usage: scripts/render-install.sh OWNER/REPO TAG [SIGNERS_FILE]}
keys=${3:-internal/release/signers.pub}
case $tag in v[0-9]*) ;; *) echo "render-install.sh: $tag is not a release tag (vX.Y.Z)" >&2; exit 2 ;; esac
mkdir -p build
grep '^ssh-ed25519 ' "$keys" >build/signers || : >build/signers
for f in install.sh install.ps1; do
  awk -v repo="$repo" -v tag="$tag" '
    $0 == "__SPRAWL_SIGNERS__" { while ((getline line < "build/signers") > 0) print line; close("build/signers"); next }
    { gsub(/__SPRAWL_REPO__/, repo); gsub(/__SPRAWL_VERSION__/, tag); print }
  ' "scripts/$f" >"build/$f"
done
rm -f build/signers
