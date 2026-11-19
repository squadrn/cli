#!/bin/sh
# Lists the archives a release build produced and their contents, with the version replaced by
# VERSION, so CI can compare them with testdata/release-assets.golden (ADR 0008: macOS and Linux
# archives keep their names and contents).
#   scripts/release-assets.sh [DIST]   (default: dist)
set -eu
export LC_ALL=C
dist=${1:-dist}
for a in "$dist"/squadrn_*.tar.gz "$dist"/squadrn_*.zip; do
  [ -e "$a" ] || continue
  name=$(basename "$a" | sed -E 's/^squadrn_[^_]+_/squadrn_VERSION_/')
  echo "$name"
  case "$a" in
    *.tar.gz) tar -tzf "$a" ;;
    *.zip) unzip -Z1 "$a" ;;
  esac | sort | sed 's/^/  /'
done
