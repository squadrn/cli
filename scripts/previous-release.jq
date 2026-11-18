# The release an upgrade test starts from: the highest published release lower than $ENV.TAG,
# by semver precedence (rc.10 > rc.9), on the same channel (a stable tag looks at stable
# releases only). Input: gh release list --json tagName,isPrerelease,isDraft.
def key: ltrimstr("v") | split("+")[0] | (index("-")) as $i
  | (if $i then [.[:$i], .[$i+1:]] else [., null] end) as [$core, $pre]
  | ($core | split(".") | map(tonumber)) + [(if $pre then 0 else 1 end), (($pre // "") | split(".") | map(tonumber? // .))];
($ENV.TAG | key) as $new | ($ENV.TAG | contains("-")) as $beta
| [.[] | select(.isDraft | not) | select($beta or (.isPrerelease | not))
       | select(.tagName | test("^v[0-9]+\\.[0-9]+\\.[0-9]+")) | select((.tagName | key) < $new)]
| max_by(.tagName | key) | .tagName // empty
