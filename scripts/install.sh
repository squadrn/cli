#!/bin/sh
# Installs sprawl from its releases repository (binaries only, no source). Requires gh, logged in
# with read access to that repository, and ssh-keygen to verify the release signature.
#
#   gh release download -R __SPRAWL_REPO__ -p install.sh -O - | sh
#   gh release download -R __SPRAWL_REPO__ -p install.sh -O - | sh -s -- --name ai --alias sx
#
# Options (or environment):
#   --name NAME       command name (SPRAWL_COMMAND; default: asked, else "sprawl")
#   --alias A,B       short aliases (SPRAWL_ALIASES; default: asked, else "sra"; '' for none)
#   --version vX.Y.Z  a specific release (SPRAWL_VERSION; default: latest)
#   --repo OWNER/NAME the repository to install from (default: the one this script came from)
#   --force           accept a name that another program on PATH already uses
set -eu

repo=__SPRAWL_REPO__
# Public keys allowed to sign releases (ssh-ed25519 lines); empty in unreleased builds.
signers=$(
  cat <<'SIGNERS'
__SPRAWL_SIGNERS__
SIGNERS
)
name=${SPRAWL_COMMAND:-}
aliases=${SPRAWL_ALIASES-}
alias_set=${SPRAWL_ALIASES+yes}
tag=${SPRAWL_VERSION:-}
force=""

die() { printf 'install.sh: %s\n' "$*" >&2; exit 1; }

while [ $# -gt 0 ]; do
  case "$1" in
    --name) [ $# -ge 2 ] || die "--name needs a value"; name=$2; shift 2 ;;
    --alias) [ $# -ge 2 ] || die "--alias needs a value"; aliases=$2; alias_set=yes; shift 2 ;;
    --version) [ $# -ge 2 ] || die "--version needs a value"; tag=$2; shift 2 ;;
    --repo) [ $# -ge 2 ] || die "--repo needs a value"; repo=$2; shift 2 ;;
    --force) force=--force; shift ;;
    *) die "unknown option $1" ;;
  esac
done

command -v gh >/dev/null || die "gh is required: https://cli.github.com (then: gh auth login)"
command -v git >/dev/null || die "git is required"
# sprawl needs Git 2.31 or newer (gitver.Min); sprawl doctor prints the upgrade command.
git_version=$(git version | sed -n 's/^git version \([0-9][0-9]*\)\.\([0-9][0-9]*\).*/\1 \2/p')
git_major=${git_version% *}
git_minor=${git_version#* }
if [ -z "$git_version" ] || [ "$git_major" -lt 2 ] || { [ "$git_major" -eq 2 ] && [ "$git_minor" -lt 31 ]; }; then
  die "$(git version) is too old; sprawl needs git 2.31 or newer (macOS: brew install git; Ubuntu: sudo add-apt-repository ppa:git-core/ppa && sudo apt install git; Debian: sudo apt install -t <codename>-backports git)"
fi
command -v tar >/dev/null || die "tar is required"

case $(uname -s) in
  Darwin) os=darwin ;;
  Linux) os=linux ;;
  *) die "unsupported OS $(uname -s); sprawl supports macOS and Linux" ;;
esac
case $(uname -m) in
  x86_64 | amd64) arch=amd64 ;;
  arm64 | aarch64) arch=arm64 ;;
  *) die "unsupported CPU $(uname -m)" ;;
esac

valid_name() { printf '%s' "$1" | grep -Eq '^[a-z][a-z0-9-]{0,15}$'; }

# First install with a terminal: ask for the command name. Upgrades keep the saved one.
config="${XDG_CONFIG_HOME:-$HOME/.config}/sprawl/config.toml"
# /dev/tty can exist yet fail to open (no controlling terminal: CI, ssh -T), so try opening it.
if [ -z "$name" ] && [ ! -f "$config" ] && (: </dev/tty >/dev/tty) 2>/dev/null; then
  while :; do
    printf 'Command name for sprawl [sprawl]: ' >/dev/tty
    IFS= read -r answer </dev/tty || answer=""
    name=${answer:-sprawl}
    if ! valid_name "$name"; then
      printf '  use lowercase letters, digits and -, starting with a letter (max 16)\n' >/dev/tty
      continue
    fi
    existing=$(command -v "$name" 2>/dev/null || true)
    if [ -n "$existing" ] && [ "$(dirname "$existing")" != "$HOME/.local/bin" ] && [ -z "$force" ]; then
      printf '  %s is already a command (%s). Use it anyway? [y/N] ' "$name" "$existing" >/dev/tty
      IFS= read -r yn </dev/tty || yn=""
      case "$yn" in y | Y) force=--force ;; *) continue ;; esac
    fi
    break
  done
  if [ -z "$alias_set" ]; then
    while :; do
      printf "Short alias [sra] ('-' for none): " >/dev/tty
      IFS= read -r answer </dev/tty || answer=""
      case "${answer:-sra}" in
        -) aliases=""; alias_set=yes; break ;;
        *) if valid_name "${answer:-sra}"; then aliases=${answer:-sra}; alias_set=yes; break; fi
           printf '  use lowercase letters, digits and -, starting with a letter (max 16)\n' >/dev/tty ;;
      esac
    done
  fi
fi
[ -z "$name" ] || valid_name "$name" || die "invalid command name: $name"

if [ -z "$tag" ]; then
  tag=$(gh release view --repo "$repo" --json tagName --jq .tagName) ||
    die "cannot read releases of $repo; check gh auth status and your access to the repo"
fi
asset="sprawl_${tag#v}_${os}_${arch}.tar.gz"

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT INT TERM
signed=""
if printf '%s\n' "$signers" | grep -q '^ssh-ed25519 '; then signed=yes; fi
if [ -n "$signed" ]; then
  command -v ssh-keygen >/dev/null || die "ssh-keygen is required to verify the release signature"
  # The signature first and on its own, so a release without one is refused with a clear reason.
  gh release download "$tag" --repo "$repo" --pattern checksums.txt.sig --dir "$tmp" 2>/dev/null ||
    die "release $tag has no signature (checksums.txt.sig); refusing to install"
  gh release download "$tag" --repo "$repo" --pattern "$asset" --pattern checksums.txt --dir "$tmp"
  printf '%s\n' "$signers" | grep '^ssh-ed25519 ' | sed 's/^/sprawl /' >"$tmp/allowed_signers"
  ssh-keygen -Y verify -f "$tmp/allowed_signers" -I sprawl -n sprawl-release -s "$tmp/checksums.txt.sig" <"$tmp/checksums.txt" >/dev/null 2>&1 ||
    die "the release signature is not valid; refusing to install"
  echo "verified release signature"
else
  gh release download "$tag" --repo "$repo" --pattern "$asset" --pattern checksums.txt --dir "$tmp"
  echo "note: this installer has no signing key; verifying by checksum only" >&2
fi

expected=$(awk -v f="$asset" '$2 == f { print $1 }' "$tmp/checksums.txt")
[ -n "$expected" ] || die "checksums.txt has no entry for $asset"
if command -v sha256sum >/dev/null; then
  actual=$(sha256sum "$tmp/$asset" | awk '{print $1}')
else
  actual=$(shasum -a 256 "$tmp/$asset" | awk '{print $1}')
fi
[ "$actual" = "$expected" ] || die "checksum mismatch for $asset"

# Build provenance, where GitHub provides it (public repositories, Enterprise Cloud). Signed
# releases do not depend on it.
if out=$(gh attestation verify "$tmp/$asset" --owner "${repo%%/*}" 2>&1); then
  printf 'verified build provenance of %s\n' "$asset"
else
  case "$out" in
    *"no attestations found"* | *"unknown command"* | *"HTTP 404"* | *"HTTP 403"*) [ -n "$signed" ] || printf 'note: %s has no build attestation\n' "$asset" >&2 ;;
    *) die "build provenance check failed for $asset: $out" ;;
  esac
fi

mkdir "$tmp/release"
tar -xzf "$tmp/$asset" -C "$tmp/release"

set -- "$tmp/release/sprawl" self-install "$tmp/release"
[ -z "$name" ] || set -- "$@" --command "$name"
[ -z "$alias_set" ] || set -- "$@" --alias "$aliases"
[ -z "$force" ] || set -- "$@" "$force"
"$@"
