#!/bin/sh
# Installs squadrn from its GitHub releases. Needs curl or wget, tar, git 2.31 or newer, and
# ssh-keygen to verify the release signature. No GitHub account or gh needed.
#
#   curl -fsSL https://github.com/squadrn/squadrn-cli/releases/latest/download/install.sh | sh
#   curl -fsSL https://github.com/squadrn/squadrn-cli/releases/latest/download/install.sh | sh -s -- --command ai --alias sx
#
# It installs the release it was downloaded from (latest/download is the newest stable release;
# releases/download/vX.Y.Z/install.sh is that release).
#
# Options (or environment):
#   --command NAME    command name (SQUADRN_COMMAND; default: asked, else "srn")
#   --alias A,B       short aliases (SQUADRN_ALIASES; default: asked, else "squadrn"; '' for none)
#   --version vX.Y.Z  another release (SQUADRN_VERSION; default: this script's release)
#   --repo OWNER/NAME the repository to install from (default: the one this script came from)
#   --force           accept a name that another program on PATH already uses
# SQUADRN_INSTALL_BASE replaces https://github.com/OWNER/NAME/releases/download (a mirror, or a
# local directory holding <tag>/<files> for tests); signatures are verified all the same.
set -eu

repo=__SQUADRN_REPO__
case $repo in */*) ;; *) repo=squadrn/squadrn-cli ;; esac
# The release this script was published with; a placeholder in a source checkout.
release_tag=__SQUADRN_VERSION__
# Public keys allowed to sign releases (ssh-ed25519 lines); empty in unreleased builds.
signers=$(
  cat <<'SIGNERS'
__SQUADRN_SIGNERS__
SIGNERS
)
name=${SQUADRN_COMMAND:-}
aliases=${SQUADRN_ALIASES-}
alias_set=${SQUADRN_ALIASES+yes}
tag=${SQUADRN_VERSION:-}
force=""

die() { printf 'install.sh: %s\n' "$*" >&2; exit 1; }

while [ $# -gt 0 ]; do
  case "$1" in
    --command) [ $# -ge 2 ] || die "--command needs a value"; name=$2; shift 2 ;;
    --alias) [ $# -ge 2 ] || die "--alias needs a value"; aliases=$2; alias_set=yes; shift 2 ;;
    --version) [ $# -ge 2 ] || die "--version needs a value"; tag=$2; shift 2 ;;
    --repo) [ $# -ge 2 ] || die "--repo needs a value"; repo=$2; shift 2 ;;
    --force) force=--force; shift ;;
    *) die "unknown option $1" ;;
  esac
done

if command -v curl >/dev/null; then
  fetch() { curl -fsSL --retry 2 --connect-timeout 20 -o "$2" "$1"; }
elif command -v wget >/dev/null; then
  fetch() { wget -q -T 60 -O "$2" "$1"; }
else
  die "curl or wget is required"
fi
command -v git >/dev/null || die "git is required"
# squadrn needs Git 2.31 or newer (gitver.Min); squadrn doctor prints the upgrade command.
git_version=$(git version | sed -n 's/^git version \([0-9][0-9]*\)\.\([0-9][0-9]*\).*/\1 \2/p')
git_major=${git_version% *}
git_minor=${git_version#* }
if [ -z "$git_version" ] || [ "$git_major" -lt 2 ] || { [ "$git_major" -eq 2 ] && [ "$git_minor" -lt 31 ]; }; then
  die "$(git version) is too old; squadrn needs git 2.31 or newer (macOS: brew install git; Ubuntu: sudo add-apt-repository ppa:git-core/ppa && sudo apt install git; Debian: sudo apt install -t <codename>-backports git)"
fi
command -v tar >/dev/null || die "tar is required"

case $(uname -s) in
  Darwin) os=darwin ;;
  Linux) os=linux ;;
  MINGW* | MSYS* | CYGWIN*) die "this is Windows: in PowerShell run: irm https://github.com/$repo/releases/latest/download/install.ps1 | iex" ;;
  *) die "unsupported OS $(uname -s); squadrn supports macOS and Linux" ;;
esac
case $(uname -m) in
  x86_64 | amd64) arch=amd64 ;;
  arm64 | aarch64) arch=arm64 ;;
  *) die "unsupported CPU $(uname -m)" ;;
esac

case $release_tag in v[0-9]*) [ -n "$tag" ] || tag=$release_tag ;; esac
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
trap 'rm -rf "$tmp"; exit 130' INT TERM
if [ -z "$tag" ]; then
  fetch "https://api.github.com/repos/$repo/releases/latest" "$tmp/latest.json" ||
    die "cannot look up the latest release of $repo (network, or GitHub API rate limit)"
  tag=$(sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' "$tmp/latest.json" | head -n 1)
  [ -n "$tag" ] || die "cannot read the latest release of $repo"
fi
asset="squadrn_${tag#v}_${os}_${arch}.tar.gz"
base=${SQUADRN_INSTALL_BASE:-https://github.com/$repo/releases/download}
# get FILE: one file of the release into $tmp; a local base directory is copied from.
get() {
  case $base in
    /*) cp "$base/$tag/$1" "$tmp/$1" 2>/dev/null ;;
    *) fetch "$base/$tag/$1" "$tmp/$1" ;;
  esac
}
signed=""
if printf '%s\n' "$signers" | grep -q '^ssh-ed25519 '; then signed=yes; fi
if [ -n "$signed" ]; then
  command -v ssh-keygen >/dev/null || die "ssh-keygen is required to verify the release signature"
  # The signature first and on its own, so a release without one is refused with a clear reason.
  get checksums.txt.sig ||
    die "cannot download the signature of $tag (checksums.txt.sig): no such release, no signature, or no network; refusing to install"
  get checksums.txt || die "cannot download checksums.txt of $tag"
  printf '%s\n' "$signers" | grep '^ssh-ed25519 ' | sed 's/^/squadrn /' >"$tmp/allowed_signers"
  ssh-keygen -Y verify -f "$tmp/allowed_signers" -I squadrn -n squadrn-release -s "$tmp/checksums.txt.sig" <"$tmp/checksums.txt" >/dev/null 2>&1 ||
    die "the release signature is not valid; refusing to install"
  echo "verified release signature"
else
  get checksums.txt || die "cannot download checksums.txt of $tag (no such release, or no network)"
  echo "note: this installer has no signing key; verifying by checksum only" >&2
fi

get "$asset" || die "cannot download $asset (no build for $os/$arch in $tag?)"
expected=$(awk -v f="$asset" '$2 == f { print $1 }' "$tmp/checksums.txt")
[ -n "$expected" ] || die "checksums.txt has no entry for $asset"
if command -v sha256sum >/dev/null; then
  actual=$(sha256sum "$tmp/$asset" | awk '{print $1}')
else
  actual=$(shasum -a 256 "$tmp/$asset" | awk '{print $1}')
fi
[ "$actual" = "$expected" ] || die "checksum mismatch for $asset"

mkdir "$tmp/release"
tar -xzf "$tmp/$asset" -C "$tmp/release"

set -- "$tmp/release/squadrn" self-install "$tmp/release"
[ -z "$name" ] || set -- "$@" --command "$name"
[ -z "$alias_set" ] || set -- "$@" --alias "$aliases"
[ -z "$force" ] || set -- "$@" "$force"
# squadrn asks for the names itself (on the terminal) unless they were given. /dev/tty can exist
# yet fail to open (no controlling terminal: CI, ssh -T), so try opening it.
if [ -z "$name" ] && [ -z "$alias_set" ] && (: </dev/tty >/dev/tty) 2>/dev/null; then
  set -- "$@" --interactive
fi
"$@"
