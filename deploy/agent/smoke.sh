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

# The session must work in exactly the AI's folder and report the AI's machine (CA-04): inside the
# container the CLI would otherwise report the container's hostname and its own working directory
# and join another AI. A stand-in partyline binary replaces the image's one, so nothing here needs
# a Partyline instance: `setup` succeeds and the harness session prints where it runs.
echo "==> the entrypoint works in PARTYLINE_FOLDER and reports PARTYLINE_MACHINE"
fake_dir=$(mktemp -d)
trap 'rm -rf "$fake_dir"' EXIT
cat >"$fake_dir/partyline" <<'EOF'
#!/bin/sh
case "$1" in
setup) exit 0 ;;
esac
printf 'smoke: cwd=%s folder=%s machine=%s\n' "$PWD" "${PARTYLINE_FOLDER:-}" "${PARTYLINE_MACHINE:-}"
exit 1
EOF
chmod 0755 "$fake_dir/partyline"
session_out=$(timeout -k 10 30 "${docker[@]}" run --rm \
	--volume "$fake_dir/partyline:/usr/local/bin/partyline:ro" \
	--env PARTYLINE_URL=https://partyline.example.com \
	--env PARTYLINE_TOKEN=pl_agent_smoke \
	--env PARTYLINE_HARNESS=opencode \
	--env OPENCODE_API_KEY=sk-smoke \
	--env PARTYLINE_FOLDER=/workspace/team/atlas \
	--env PARTYLINE_MACHINE=container:pl-atlas \
	"$image" 2>&1 || true)
expected='smoke: cwd=/workspace/team/atlas folder=/workspace/team/atlas machine=container:pl-atlas'
# The session runs under a pty (`script`), so its lines end with a carriage return.
if ! printf '%s\n' "$session_out" | tr -d '\r' | grep -Fqx "$expected"; then
	echo "FAIL: the session did not run in the AI's folder with its machine" >&2
	printf '%s\n' "$session_out" | tail -20 >&2
	exit 1
fi
echo "ok: the session's cwd, PARTYLINE_FOLDER and PARTYLINE_MACHINE are the AI's"

echo "==> image size"
run image inspect --format '{{.Size}}' "$image" |
	awk '{printf "%.0f MiB uncompressed\n", $1 / 1048576}'

echo "smoke: ok"
