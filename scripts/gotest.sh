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

case "$SHARDS" in '' | *[!0-9]* | 0) echo "gotest: SHARDS must be a positive integer" >&2; exit 2 ;; esac
case "$MIN_TESTS" in '' | *[!0-9]*) echo "gotest: SHARD_MIN_TESTS must be a non-negative integer" >&2; exit 2 ;; esac

flags=(-count=1 -timeout "$TIMEOUT")
if [ "$RACE" = 1 ]; then
	flags+=(-race)
fi

tmp=$(mktemp -d 2>/dev/null || mktemp -d -t partyline-gotest) || exit 1
ramtmp=
trap 'rm -rf "$tmp" ${ramtmp:+"$ramtmp"}' EXIT INT TERM

# Every test opens its own SQLite database, which syncs to disk on each commit. With many test
# processes at once a slow disk becomes the bottleneck (processes wait on the journal while the
# CPU idles), so on Linux the tests' temporary files and build output go to the RAM disk.
# GOTEST_TMPDIR=disk keeps the normal temporary directory.
if [ "${GOTEST_TMPDIR:-}" != disk ] && [ -d /dev/shm ] && [ -w /dev/shm ]; then
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

job_pkg=()
job_regex=()
job_pidx=()
sharded=()
shard_expected=()
shard_groups=()

list_idx=0
for pkg in "${packages[@]}"; do
	listfile="$tmp/list-$list_idx.txt"
	list_idx=$((list_idx + 1))
	if ! go test "${flags[@]}" -list '^Test' "$pkg" >"$listfile" 2>&1; then
		cat "$listfile" >&2
		echo "gotest: cannot list tests of $pkg" >&2
		exit 1
	fi
	mapfile -t tests < <(sed -n 's/^\(Test[A-Za-z0-9_]*\)$/\1/p' "$listfile" | sort -u)
	count=${#tests[@]}
	if [ "$count" -lt "$MIN_TESTS" ] || [ "$SHARDS" -le 1 ]; then
		job_pkg+=("$pkg")
		job_regex+=("")
		job_pidx+=(-1)
		continue
	fi

	groups=$SHARDS
	[ "$groups" -gt "$count" ] && groups=$count
	pidx=${#sharded[@]}
	sharded+=("$pkg")
	shard_groups+=("$groups")
	printf '%s\n' "${tests[@]}" >"$tmp/expected-$pidx.txt"
	shard_expected+=("$count")
	for ((g = 0; g < groups; g++)); do
		selected=()
		for ((i = g; i < count; i += groups)); do
			selected+=("${tests[i]}")
		done
		regex="^($(IFS='|'; echo "${selected[*]}"))$"
		job_pkg+=("$pkg")
		job_regex+=("$regex")
		job_pidx+=("$pidx")
	done
done

total=${#job_pkg[@]}
if [ "$total" -eq 0 ]; then
	echo "gotest: no test packages" >&2
	exit 1
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
	fi
	local start end status
	start=$(now_ms)
	go test "${args[@]}" "${job_pkg[i]}" >"$out" 2>&1
	status=$?
	end=$(now_ms)
	printf '%d\n' "$status" >"$tmp/job-$i.status"
	printf '%d\n' "$((end - start))" >"$tmp/job-$i.ms"
}

# Keep at most SHARDS test processes alive: one per shard group, and one per package that was
# not worth sharding.
launched=0
running=0
while [ "$launched" -lt "$total" ] || [ "$running" -gt 0 ]; do
	while [ "$launched" -lt "$total" ] && [ "$running" -lt "$SHARDS" ]; do
		run_job "$launched" &
		launched=$((launched + 1))
		running=$((running + 1))
	done
	wait -n || true
	running=$((running - 1))
done

failed=0
for ((i = 0; i < total; i++)); do
	status=$(cat "$tmp/job-$i.status" 2>/dev/null || echo 1)
	if [ "$status" -ne 0 ]; then
		failed=1
		ms=$(cat "$tmp/job-$i.ms" 2>/dev/null || echo 0)
		echo "gotest: FAIL ${job_pkg[i]} after ${ms}ms" >&2
		cat "$tmp/job-$i.log" >&2
	fi
done

for pidx in "${!sharded[@]}"; do
	actual="$tmp/actual-$pidx.txt"
	: >"$actual"
	for ((i = 0; i < total; i++)); do
		[ "${job_pidx[i]}" -eq "$pidx" ] || continue
		sed -n 's/^=== RUN   \(Test[A-Za-z0-9_]*\)$/\1/p' "$tmp/job-$i.log" >>"$actual"
	done
	sort -u -o "$actual" "$actual"
	if ! diff -u "$tmp/expected-$pidx.txt" "$actual" >"$tmp/diff-$pidx.txt"; then
		failed=1
		echo "gotest: ${sharded[pidx]}: the tests run by the shards differ from the listed tests" >&2
		cat "$tmp/diff-$pidx.txt" >&2
	fi
done

if [ "$failed" -ne 0 ]; then
	exit 1
fi

for pidx in "${!sharded[@]}"; do
	echo "ok  ${sharded[pidx]}  (${shard_expected[pidx]} tests in ${shard_groups[pidx]} shards)"
done
for ((i = 0; i < total; i++)); do
	if [ -z "${job_regex[i]}" ]; then
		sed -n -e 's/^\(ok[[:space:]].*\)$/\1/p' -e 's/^\(?[[:space:]].*\)$/\1/p' "$tmp/job-$i.log"
	fi
done
