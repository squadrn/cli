#!/bin/sh
# Checks commit messages against Conventional Commits, as @commitlint/config-conventional does,
# plus this project's rule that commits carry no AI co-author or attribution lines.
#
#   scripts/commitlint.sh FILE            one message (git commit-msg hook)
#   scripts/commitlint.sh --range A..B    every commit in a range (CI)
set -eu

types="build chore ci docs feat fix perf refactor revert style test"
max_header=100
max_line=100

# lint MESSAGE-FILE LABEL: prints problems, returns 1 when there are any.
lint() {
  file=$1
  label=$2
  msg=$(grep -v '^#' "$file" | sed -e :a -e '/^\n*$/{$d;N;ba' -e '}')
  header=$(printf '%s\n' "$msg" | head -n 1)
  bad=""
  add() { bad="$bad
  - $1"; }

  # Merges, reverts and autosquash commits are git's own messages.
  case "$header" in
    "Merge "* | "Revert \""* | "fixup! "* | "squash! "* | "amend! "*) return 0 ;;
  esac

  type=$(printf '%s' "$header" | sed -n 's/^\([a-zA-Z]*\)\(([^)]*)\)\{0,1\}!\{0,1\}: .*/\1/p')
  if [ -z "$type" ]; then
    add "header must be 'type(scope): subject' or 'type: subject' (got: $header)"
  else
    case " $types " in
      *" $type "*) ;;
      *) add "type '$type' must be one of: $types" ;;
    esac
    scope=$(printf '%s' "$header" | sed -n 's/^[a-zA-Z]*(\([^)]*\)).*/\1/p')
    if [ -n "$scope" ] && ! printf '%s' "$scope" | grep -Eq '^[a-z0-9-]+(,[a-z0-9-]+)*$'; then
      add "scope '$scope' must be lower-case words (a-z, 0-9, -), comma separated"
    fi
    subject=$(printf '%s' "$header" | sed 's/^[^:]*: //')
    if [ -z "$subject" ]; then
      add "subject must not be empty"
    elif printf '%s' "$subject" | LC_ALL=C grep -q '^[A-Z]'; then
      add "subject must start in lower case (got: $subject)"
    fi
    case "$subject" in *.) add "subject must not end with a full stop" ;; esac
  fi
  if [ "${#header}" -gt "$max_header" ]; then
    add "header is ${#header} characters; at most $max_header"
  fi
  second=$(printf '%s\n' "$msg" | sed -n 2p)
  if [ -n "$second" ]; then
    add "a blank line must separate the header from the body"
  fi
  long=$(printf '%s\n' "$msg" | sed 1d | grep -v '://' | awk -v m="$max_line" 'length($0) > m' | head -n 1)
  if [ -n "$long" ]; then
    add "body lines must be at most $max_line characters: $long"
  fi
  if printf '%s\n' "$msg" | grep -Eiq '^co-authored-by:.*(claude|anthropic|noreply@anthropic\.com)|generated with \[?claude'; then
    add "no AI co-author or attribution lines (Co-Authored-By: Claude, Generated with Claude Code)"
  fi

  if [ -n "$bad" ]; then
    printf 'commitlint: %s%s\n' "$label" "$bad" >&2
    return 1
  fi
  return 0
}

case "${1:-}" in
  --range)
    range=${2:?usage: scripts/commitlint.sh --range A..B}
    tmp=$(mktemp)
    trap 'rm -f "$tmp"' EXIT INT TERM
    status=0
    for sha in $(git rev-list --no-merges "$range"); do
      git log -1 --format=%B "$sha" >"$tmp"
      lint "$tmp" "$(git log -1 --format='%h %s' "$sha")" || status=1
    done
    exit "$status"
    ;;
  "" | -h | --help)
    sed -n '2,7p' "$0" | sed 's/^# \{0,1\}//'
    exit 2
    ;;
  *)
    lint "$1" "commit message" || {
      echo "see AGENTS.md, section Commits" >&2
      exit 1
    }
    ;;
esac
