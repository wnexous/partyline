#!/usr/bin/env bash
# Runs `make check` as parallel stages, each captured under its own log; every stage's output
# and wall time is printed afterwards, and the whole check fails when any stage failed.
#
# `make generate` runs once first so the parallel stages never read a half-written generated
# file; the drift check itself (`check-drift`) is one of the parallel stages. The build steps
# (`web-build`, `go build ./...`) run only after every stage passed.
#
# RACE=1 adds -race to the Go tests; SHARDS=N overrides the shard count (both are passed to
# the sub-makes, and `make check` itself accepts them as variables).
#
# CHECK_SKIP="stage ..." (space- or comma-separated) leaves those parallel stages out, e.g.
# CHECK_SKIP=test-go in CI, where the Go tests run split over a matrix of their own
# (GOTEST_SPLIT, see gotest.sh). A skipped stage is reported as skipped; an unknown name fails.
set -uo pipefail

cd "$(dirname "$0")/.." || exit 1

MAKE=${MAKE:-make}
RACE=${RACE:-0}
SHARDS=${SHARDS:-}
CHECK_SKIP=${CHECK_SKIP:-}
HAS_WEB=0
[ -f web/package.json ] && HAS_WEB=1

tmp=$(mktemp -d 2>/dev/null || mktemp -d -t partyline-check) || exit 1
trap 'rm -rf "$tmp"' EXIT INT TERM

now_ms() {
	date +%s%3N 2>/dev/null || echo $((SECONDS * 1000))
}

all_stages=(check-drift lint vet test-go integrations-test web-verify)
skip=" "
for name in ${CHECK_SKIP//,/ }; do
	case " ${all_stages[*]} " in
	*" $name "*) skip="$skip$name " ;;
	*) echo "check: CHECK_SKIP names an unknown stage: $name (stages: ${all_stages[*]})" >&2; exit 2 ;;
	esac
done

echo "=== generate ==="
if ! "$MAKE" --no-print-directory generate RACE="$RACE" SHARDS="$SHARDS"; then
	echo "check: generate failed" >&2
	exit 1
fi

stages=()
for stage in check-drift lint vet test-go integrations-test; do
	case "$skip" in *" $stage "*) ;; *) stages+=("$stage") ;; esac
done
if [ "$HAS_WEB" -eq 1 ]; then
	case "$skip" in *" web-verify "*) ;; *) stages+=(web-verify) ;; esac
fi

pids=()
for stage in "${stages[@]}"; do
	(
		start=$(now_ms)
		"$MAKE" --no-print-directory "$stage" RACE="$RACE" SHARDS="$SHARDS" >"$tmp/$stage.log" 2>&1
		status=$?
		end=$(now_ms)
		printf '%d\n' "$status" >"$tmp/$stage.status"
		printf '%d\n' "$((end - start))" >"$tmp/$stage.ms"
	) &
	pids+=("$!")
done
for pid in "${pids[@]}"; do
	wait "$pid" || true
done

failed=0
for stage in ${skip}; do
	printf '\n=== %s (skipped: CHECK_SKIP) ===\n' "$stage"
done
for stage in "${stages[@]}"; do
	status=$(cat "$tmp/$stage.status" 2>/dev/null || echo 1)
	ms=$(cat "$tmp/$stage.ms" 2>/dev/null || echo 0)
	printf '\n=== %s (%d.%03ds) ===\n' "$stage" $((ms / 1000)) $((ms % 1000))
	cat "$tmp/$stage.log"
	if [ "$status" -ne 0 ]; then
		echo "check: stage $stage failed" >&2
		failed=1
	fi
done

if [ "$failed" -ne 0 ]; then
	exit 1
fi

if [ "$HAS_WEB" -eq 1 ]; then
	echo
	echo "=== web-build ==="
	"$MAKE" --no-print-directory web-build || exit 1
fi
echo
echo "=== go build ./... ==="
go build ./... || exit 1
