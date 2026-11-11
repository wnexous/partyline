#!/usr/bin/env bash
# Runs the Go tests, splitting every slow package into SHARDS concurrent `go test -run` jobs.
#
# A package with at least SHARD_MIN_TESTS top-level tests is split round-robin into SHARDS
# groups and each group runs in its own process; the other packages run unfiltered. Every test
# builds its own isolated app (temp SQLite, httptest server), so process-level sharding needs
# no t.Parallel(). Each shard prints the top-level tests it ran; the union must equal
# `go test -list '^Test'` for the package, otherwise a test could be silently skipped by the
# split and the run fails.
#
# RACE=1 adds -race (the CI gate); RACE defaults to 0 locally. SHARDS defaults to the CPU
# count and may be overridden with SHARDS=N. TIMEOUT bounds each process (default 20m).
#
# GOTEST_SPLIT=k/N runs only part k of N, so N machines (the CI matrix) share the tests. Every
# split package is then split into N*SHARDS groups and part k runs the groups whose index mod N
# is k-1, so each part gets a slice of every big package; part k also runs the unsplit packages
# whose index mod N is k-1. The assignment depends only on the test listing (sorted in the C
# locale), SHARDS and SHARD_MIN_TESTS, so every part must use the same SHARDS. In this mode the
# unsplit packages run with -v too, and each part fails unless it ran exactly the tests assigned
# to it; the parts together cover every listed test exactly once. GOTEST_REPORT=dir makes the
# part write split.txt (the split and its parameters), listed.txt (every listed test of every
# package, "package<TAB>test") and ran.txt (the tests the part ran), and
# scripts/gotest-union.sh checks the reports of all the parts. Unset, nothing changes.
set -uo pipefail

cd "$(dirname "$0")/.." || exit 1

cpus() {
	if command -v nproc >/dev/null 2>&1; then
		nproc
	elif command -v getconf >/dev/null 2>&1; then
		getconf _NPROCESSORS_ONLN
	else
		echo 1
	fi
}

SHARDS=${SHARDS:-$(cpus)}
RACE=${RACE:-0}
TIMEOUT=${TIMEOUT:-20m}
MIN_TESTS=${SHARD_MIN_TESTS:-8}
SPLIT=${GOTEST_SPLIT:-}
REPORT=${GOTEST_REPORT:-}

case "$SHARDS" in '' | *[!0-9]* | 0) echo "gotest: SHARDS must be a positive integer" >&2; exit 2 ;; esac
case "$MIN_TESTS" in '' | *[!0-9]*) echo "gotest: SHARD_MIN_TESTS must be a non-negative integer" >&2; exit 2 ;; esac

# split=1 when GOTEST_SPLIT is set; part (1-based) of parts.
split=0
part=1
parts=1
if [ -n "$SPLIT" ]; then
	split=1
	part=${SPLIT%%/*}
	parts=${SPLIT#*/}
	case "$SPLIT" in */*) ;; *) part= ;; esac
	case "$part" in '' | *[!0-9]* | 0*) part= ;; esac
	case "$parts" in '' | *[!0-9]* | 0*) part= ;; esac
	if [ -z "$part" ] || [ "$part" -gt "$parts" ]; then
		echo "gotest: GOTEST_SPLIT must be k/N with 1 <= k <= N (got $SPLIT)" >&2
		exit 2
	fi
else
	REPORT= # the report describes a part; without a split there is none
fi

# The split must not depend on the locale, or two machines could order the tests differently.
sort_tests() {
	if [ "$split" -eq 1 ]; then
		LC_ALL=C sort -u "$@"
	else
		sort -u "$@"
	fi
}

flags=(-count=1 -timeout "$TIMEOUT")
if [ "$RACE" = 1 ]; then
	flags+=(-race)
fi

tmp=$(mktemp -d 2>/dev/null || mktemp -d -t partyline-gotest) || exit 1
ramtmp=
trap 'rm -rf "$tmp" ${ramtmp:+"$ramtmp"}' EXIT INT TERM

# GOTEST_TMPDIR=ram puts the tests' temporary files and build output on the RAM disk
# (/dev/shm). It is opt-in: systemd-logind removes a user's /dev/shm files when that user's last
# login session ends (RemoveIPC), which can break a run in the middle. Test databases already skip
# fsync (store.testPragmas), so the disk is not the bottleneck by default.
if [ "${GOTEST_TMPDIR:-}" = ram ] && [ -d /dev/shm ] && [ -w /dev/shm ]; then
	if ramtmp=$(mktemp -d /dev/shm/partyline-test.XXXXXX 2>/dev/null); then
		export TMPDIR="$ramtmp"
	else
		ramtmp=
	fi
fi

if [ "$#" -gt 0 ]; then
	if ! packages_out=$(go list "$@"); then
		exit 1
	fi
else
	if ! packages_out=$(go list ./...); then
		exit 1
	fi
fi
if [ -z "$packages_out" ]; then
	echo "gotest: no packages" >&2
	exit 1
fi
mapfile -t packages <<<"$packages_out"
if [ "${#packages[@]}" -eq 0 ]; then
	echo "gotest: no packages" >&2
	exit 1
fi

# One job is one `go test` process. job_pidx names the checked package the job belongs to (-1:
# not checked, an unsplit package outside split mode). job_verbose runs an unsplit package with -v
# so its tests can be checked; job_size (tests the job runs) orders the jobs in split mode.
job_pkg=()
job_regex=()
job_pidx=()
job_verbose=()
job_size=()
# A checked package: the tests its jobs must run (expected-$pidx.txt) and the summary line parts.
checked=()
checked_expected=()
checked_total=()
checked_groups=()
checked_all_groups=()

if [ -n "$REPORT" ]; then
	mkdir -p "$REPORT" || exit 1
	: >"$REPORT/listed.txt"
fi

all_groups=$((SHARDS * parts))
list_start=$SECONDS

# finished prefix n counts the background jobs prefix-0 .. prefix-(n-1) that wrote their
# prefix-i.status file. Running jobs are the launched ones that have not finished; they are not
# counted from the returns of `wait -n`, because bash's `wait -n` also returns for process
# substitutions (`< <(...)`), and a counter decremented on every return lets the last jobs be
# read while they still run. `wait -n` only sleeps until some child ends; 127 means none is left.
finished() {
	local n=0 i
	for ((i = 0; i < $2; i++)); do
		[ -e "$tmp/$1-$i.status" ] && n=$((n + 1))
	done
	echo "$n"
}

# list_pkg idx pkg writes the package's test listing to list-idx.txt and its exit status to
# list-idx.status. Listing builds the package's test binary, so a split part (which must build
# them all anyway) lists SHARDS packages at a time; otherwise they are listed one by one, while
# the loop below reads them.
list_pkg() {
	go test "${flags[@]}" -list '^Test' "$2" >"$tmp/list-$1.txt" 2>&1
	printf '%d\n' "$?" >"$tmp/list-$1.status"
}
if [ "$split" -eq 1 ]; then
	for idx in "${!packages[@]}"; do
		while [ $((idx - $(finished list "$idx"))) -ge "$SHARDS" ]; do
			wait -n
			[ "$?" -ne 127 ] || break
		done
		list_pkg "$idx" "${packages[idx]}" &
	done
	wait
fi

list_idx=0
unsplit_idx=0
for pkg in "${packages[@]}"; do
	listfile="$tmp/list-$list_idx.txt"
	[ "$split" -eq 1 ] || list_pkg "$list_idx" "$pkg"
	list_status=$(cat "$tmp/list-$list_idx.status" 2>/dev/null || echo 1)
	list_idx=$((list_idx + 1))
	if [ "$list_status" -ne 0 ]; then
		cat "$listfile" >&2
		echo "gotest: cannot list tests of $pkg" >&2
		exit 1
	fi
	mapfile -t tests < <(sed -n 's/^\(Test[A-Za-z0-9_]*\)$/\1/p' "$listfile" | sort_tests)
	count=${#tests[@]}
	if [ -n "$REPORT" ] && [ "$count" -gt 0 ]; then
		printf '%s\n' "${tests[@]}" | awk -v p="$pkg" '{ print p "\t" $0 }' >>"$REPORT/listed.txt"
	fi
	if [ "$count" -lt "$MIN_TESTS" ] || [ "$all_groups" -le 1 ]; then
		u=$unsplit_idx
		unsplit_idx=$((unsplit_idx + 1))
		if [ "$split" -eq 0 ]; then
			job_pkg+=("$pkg")
			job_regex+=("")
			job_pidx+=(-1)
			job_verbose+=(0)
			job_size+=("$count")
			continue
		fi
		[ $((u % parts)) -eq $((part - 1)) ] || continue
		pidx=${#checked[@]}
		checked+=("$pkg")
		if [ "$count" -gt 0 ]; then
			printf '%s\n' "${tests[@]}" >"$tmp/expected-$pidx.txt"
		else
			: >"$tmp/expected-$pidx.txt"
		fi
		checked_expected+=("$count")
		checked_total+=("$count")
		checked_groups+=(1)
		checked_all_groups+=(0)
		job_pkg+=("$pkg")
		job_regex+=("")
		job_pidx+=("$pidx")
		job_verbose+=(1)
		job_size+=("$count")
		continue
	fi

	groups=$all_groups
	[ "$groups" -gt "$count" ] && groups=$count
	pidx=${#checked[@]}
	: >"$tmp/expected-$pidx.txt"
	mine=0
	assigned=0
	for ((g = part - 1; g < groups; g += parts)); do
		selected=()
		for ((i = g; i < count; i += groups)); do
			selected+=("${tests[i]}")
		done
		printf '%s\n' "${selected[@]}" >>"$tmp/expected-$pidx.txt"
		regex="^($(IFS='|'; echo "${selected[*]}"))$"
		job_pkg+=("$pkg")
		job_regex+=("$regex")
		job_pidx+=("$pidx")
		job_verbose+=(1)
		job_size+=("${#selected[@]}")
		mine=$((mine + 1))
		assigned=$((assigned + ${#selected[@]}))
	done
	[ "$mine" -gt 0 ] || continue
	sort_tests -o "$tmp/expected-$pidx.txt" "$tmp/expected-$pidx.txt"
	checked+=("$pkg")
	checked_expected+=("$assigned")
	checked_total+=("$count")
	checked_groups+=("$mine")
	checked_all_groups+=("$groups")
done

total=${#job_pkg[@]}
if [ "$split" -eq 1 ]; then
	echo "gotest: part $part/$parts: listed ${#packages[@]} packages in $((SECONDS - list_start))s; running $total jobs"
	if [ -n "$REPORT" ]; then
		printf 'part=%s\nparts=%s\nshards=%s\nmin_tests=%s\n' "$part" "$parts" "$SHARDS" "$MIN_TESTS" >"$REPORT/split.txt"
		: >"$REPORT/ran.txt"
	fi
	# A part may get no job at all when there are more parts than work units.
	if [ "$total" -eq 0 ]; then
		echo "gotest: part $part/$parts has no tests to run"
		exit 0
	fi
elif [ "$total" -eq 0 ]; then
	echo "gotest: no test packages" >&2
	exit 1
fi

# Launch order: the package order, or in split mode the biggest jobs first, so the slowest
# shards do not start last and stretch the part's wall time.
order=()
if [ "$split" -eq 1 ]; then
	mapfile -t order < <(for ((i = 0; i < total; i++)); do echo "${job_size[i]} $i"; done | sort -k1,1nr -k2,2n | cut -d' ' -f2)
else
	for ((i = 0; i < total; i++)); do order+=("$i"); done
fi

now_ms() {
	date +%s%3N 2>/dev/null || echo $((SECONDS * 1000))
}

run_job() {
	local i=$1
	local out="$tmp/job-$i.log"
	local args=("${flags[@]}")
	if [ -n "${job_regex[i]}" ]; then
		args+=(-v -run "${job_regex[i]}")
	elif [ "${job_verbose[i]}" -eq 1 ]; then
		args+=(-v)
	fi
	local start end status
	start=$(now_ms)
	go test "${args[@]}" "${job_pkg[i]}" >"$out" 2>&1
	status=$?
	end=$(now_ms)
	printf '%d\n' "$status" >"$tmp/job-$i.status"
	printf '%d\n' "$((end - start))" >"$tmp/job-$i.ms"
}

# annotate prints a GitHub Actions error annotation (only under GitHub Actions).
annotate() {
	if [ "${GITHUB_ACTIONS:-}" = true ]; then
		echo "::error title=go test::$1"
	fi
}

# Keep at most SHARDS test processes alive: one per shard group, and one per package that was
# not worth sharding.
launched=0
while [ "$launched" -lt "$total" ]; do
	while [ "$launched" -lt "$total" ] && [ $((launched - $(finished job "$total"))) -lt "$SHARDS" ]; do
		run_job "${order[launched]}" &
		launched=$((launched + 1))
	done
	[ "$launched" -lt "$total" ] || break
	wait -n
	[ "$?" -ne 127 ] || break
done
wait

failed=0
for ((i = 0; i < total; i++)); do
	status=$(cat "$tmp/job-$i.status" 2>/dev/null || echo 1)
	if [ "$status" -ne 0 ]; then
		failed=1
		ms=$(cat "$tmp/job-$i.ms" 2>/dev/null || echo 0)
		echo "gotest: FAIL ${job_pkg[i]} after ${ms}ms" >&2
		cat "$tmp/job-$i.log" >&2
		if [ "${GITHUB_ACTIONS:-}" = true ]; then
			fails=$(sed -n 's/^[[:space:]]*--- FAIL: \([^ ]*\).*$/\1/p' "$tmp/job-$i.log" | sort -u | tr '\n' ' ' | sed 's/ $//')
			annotate "FAIL ${job_pkg[i]} ${fails:-(no failing test reported: build error, panic or timeout; see the log)}"
		fi
	fi
done

for pidx in "${!checked[@]}"; do
	actual="$tmp/actual-$pidx.txt"
	: >"$actual"
	for ((i = 0; i < total; i++)); do
		[ "${job_pidx[i]}" -eq "$pidx" ] || continue
		sed -n 's/^=== RUN   \(Test[A-Za-z0-9_]*\)$/\1/p' "$tmp/job-$i.log" >>"$actual"
	done
	sort_tests -o "$actual" "$actual"
	if [ -n "$REPORT" ]; then
		awk -v p="${checked[pidx]}" '{ print p "\t" $0 }' "$actual" >>"$REPORT/ran.txt"
	fi
	if ! diff -u "$tmp/expected-$pidx.txt" "$actual" >"$tmp/diff-$pidx.txt"; then
		failed=1
		if [ "$split" -eq 1 ]; then
			echo "gotest: ${checked[pidx]}: the tests run by part $part/$parts differ from the tests assigned to it" >&2
		else
			echo "gotest: ${checked[pidx]}: the tests run by the shards differ from the listed tests" >&2
		fi
		cat "$tmp/diff-$pidx.txt" >&2
		annotate "${checked[pidx]}: the tests run differ from the tests assigned (a test was skipped or added)"
	fi
done

if [ "$failed" -ne 0 ]; then
	exit 1
fi

for pidx in "${!checked[@]}"; do
	if [ "$split" -eq 0 ]; then
		echo "ok  ${checked[pidx]}  (${checked_expected[pidx]} tests in ${checked_groups[pidx]} shards)"
	elif [ "${checked_all_groups[pidx]}" -eq 0 ]; then
		echo "ok  ${checked[pidx]}  (${checked_expected[pidx]} tests, whole package)"
	else
		echo "ok  ${checked[pidx]}  (${checked_expected[pidx]} of ${checked_total[pidx]} tests in ${checked_groups[pidx]} of ${checked_all_groups[pidx]} shards)"
	fi
done
for ((i = 0; i < total; i++)); do
	if [ -z "${job_regex[i]}" ] && [ "${job_verbose[i]}" -eq 0 ]; then
		sed -n -e 's/^\(ok[[:space:]].*\)$/\1/p' -e 's/^\(?[[:space:]].*\)$/\1/p' "$tmp/job-$i.log"
	fi
done
