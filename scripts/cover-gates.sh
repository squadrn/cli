#!/bin/sh
# Fails when a package's statement coverage is below its gate. Gates: "pkg:min ...", from the
# arguments or $COVER_GATES. Runs on Linux, macOS and (through Git Bash) Windows.
#   scripts/cover-gates.sh guard:95 proc:85
set -eu
gates=${*:-${COVER_GATES:?no gates given}}
status=0
for g in $gates; do
  pkg=${g%%:*}
  min=${g#*:}
  pct=$(go test -cover "./internal/$pkg/" | grep -o '[0-9.]*%' | tr -d % | tail -n 1)
  printf '%s coverage: %s%% (min %s%%)\n' "$pkg" "${pct:-0}" "$min"
  if ! awk -v p="${pct:-0}" -v m="$min" 'BEGIN { exit !(p >= m) }'; then
    status=1
  fi
done
exit "$status"
