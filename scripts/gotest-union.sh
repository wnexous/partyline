#!/usr/bin/env bash
# Checks that the parts of a split Go test run (GOTEST_SPLIT=k/N, see gotest.sh) together ran
# every listed test exactly once. Each argument is the GOTEST_REPORT directory of one part.
#
# It fails when a part is missing or repeated, when the parts disagree on the split parameters
# or on the test listing (a part ran other code, or the split was not deterministic), when a
# listed test ran in no part or a test ran in two parts, and when a part ran a test that is not
# listed. Each part already fails unless it ran exactly its own assignment; this closes the gap
# between the parts.
set -uo pipefail

if [ "$#" -eq 0 ]; then
	echo "usage: gotest-union.sh REPORT_DIR..." >&2
	exit 2
fi

tmp=$(mktemp -d 2>/dev/null || mktemp -d -t partyline-gotest-union) || exit 1
trap 'rm -rf "$tmp"' EXIT INT TERM

fail() {
	echo "gotest-union: $*" >&2
	if [ "${GITHUB_ACTIONS:-}" = true ]; then
		echo "::error title=go test split::$*"
	fi
	exit 1
}

value() { sed -n "s/^$2=//p" "$1/split.txt"; }

first=$1
for dir in "$@"; do
	for f in split.txt listed.txt ran.txt; do
		[ -f "$dir/$f" ] || fail "$dir has no $f"
	done
done
parts=$(value "$first" parts)
[ "$#" -eq "$parts" ] || fail "$# reports for a split into $parts parts"

seen=" "
for dir in "$@"; do
	for key in parts shards min_tests; do
		[ "$(value "$dir" "$key")" = "$(value "$first" "$key")" ] || fail "$dir: $key differs from $first"
	done
	part=$(value "$dir" part)
	case "$seen" in *" $part "*) fail "part $part appears twice" ;; esac
	seen="$seen$part "
	cmp -s "$first/listed.txt" "$dir/listed.txt" || fail "$dir: the test listing differs from $first"
done
for ((k = 1; k <= parts; k++)); do
	case "$seen" in *" $k "*) ;; *) fail "part $k/$parts is missing" ;; esac
done

LC_ALL=C sort "$first/listed.txt" >"$tmp/listed"
cat "${@/%//ran.txt}" | LC_ALL=C sort >"$tmp/ran"
dups=$(uniq -d "$tmp/ran")
[ -z "$dups" ] || fail "tests ran in more than one part: $(echo "$dups" | tr '\t\n' '. ')"
if ! diff -u "$tmp/listed" "$tmp/ran" >"$tmp/diff"; then
	cat "$tmp/diff" >&2
	fail "the tests the parts ran differ from the listed tests ('-': not run, '+': not listed)"
fi
echo "ok  $(wc -l <"$tmp/listed" | tr -d ' ') tests ran exactly once across $parts parts"
