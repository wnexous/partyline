#!/usr/bin/env bash
# Builds the agent image and checks what it promises without needing a Partyline instance:
# the partyline binary runs, every harness prints its pinned version, and the entrypoint refuses
# an unknown harness and a missing token instead of starting a session that cannot work.
#
# Not part of `make check` (it needs Docker). Run it from anywhere:
#   deploy/agent/smoke.sh
# Set DOCKER to use another client (for example DOCKER="sudo -n docker"), IMAGE to reuse a tag,
# and SKIP_BUILD=1 to check an image that is already built.
set -euo pipefail

root=$(cd "$(dirname "$0")/../.." && pwd)
image=${IMAGE:-partyline-agent:smoke}
skip_build=${SKIP_BUILD:-0}

read -r -a docker <<<"${DOCKER:-docker}"

run() {
	"${docker[@]}" "$@"
}

echo "==> image $image"
if [ "$skip_build" = "1" ]; then
	echo "skipping the build (SKIP_BUILD=1)"
else
	run build --file "$root/deploy/agent/Dockerfile" --tag "$image" "$root"
fi

echo "==> partyline version"
run run --rm --entrypoint partyline "$image" version

# The harnesses the image pins, and how each prints its version.
for harness in claude opencode pi omp; do
	echo "==> $harness --version"
	run run --rm --entrypoint "$harness" "$image" --version
done

echo "==> the entrypoint refuses an unknown harness"
if run run --rm \
	--env PARTYLINE_URL=https://partyline.example.com \
	--env PARTYLINE_TOKEN=pl_agent_smoke \
	--env PARTYLINE_HARNESS=codex \
	"$image" 2>/dev/null; then
	echo "FAIL: the entrypoint accepted PARTYLINE_HARNESS=codex" >&2
	exit 1
fi
echo "ok: an unknown harness exits non-zero"

echo "==> the entrypoint refuses a missing token"
if run run --rm \
	--env PARTYLINE_URL=https://partyline.example.com \
	--env PARTYLINE_HARNESS=claude \
	"$image" 2>/dev/null; then
	echo "FAIL: the entrypoint started without PARTYLINE_TOKEN" >&2
	exit 1
fi
echo "ok: a missing token exits non-zero"

echo "==> the entrypoint refuses a missing harness credential"
if run run --rm \
	--env PARTYLINE_URL=https://partyline.example.com \
	--env PARTYLINE_TOKEN=pl_agent_smoke \
	--env PARTYLINE_HARNESS=omp \
	"$image" 2>/dev/null; then
	echo "FAIL: the entrypoint started omp without a provider key" >&2
	exit 1
fi
echo "ok: a missing credential exits non-zero"

echo "==> image size"
run image inspect --format '{{.Size}}' "$image" |
	awk '{printf "%.0f MiB uncompressed\n", $1 / 1048576}'

echo "smoke: ok"
