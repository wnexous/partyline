#!/bin/sh
# Partyline agent container entrypoint (CA-04, CA-05, CA-08). See deploy/agent/README.md.
#
# One container runs one harness as an AI of the Partyline instance it is handed: it clones the
# optional workspace repository, logs the partyline CLI in non-interactively, registers the
# harness with Partyline, and then keeps `partyline <harness> --principal` alive inside a TTY,
# restarting it with an exponential backoff (1s doubling to 60s, reset after 10 minutes up).
#
# Every credential comes from the environment; this script never prints, copies or inspects one.
set -eu
set +x # commands are not traced: the environment holds secrets (CA-08)

log() { printf 'partyline-agent: %s\n' "$*" >&2; }
die() {
	code=$1
	shift
	log "error: $*"
	exit "$code"
}

# Config files, the cloned repository and everything a harness writes are private by default.
umask 077

workspace=${PARTYLINE_WORKSPACE:-/workspace}
repo_dir="$workspace/repo"

# --- environment contract (CA-04) ------------------------------------------------------------

[ -n "${PARTYLINE_URL:-}" ] || die 2 "PARTYLINE_URL is required, e.g. https://partyline.example.com"
case "$PARTYLINE_URL" in
http://* | https://*) ;;
*) die 2 "PARTYLINE_URL must start with http:// or https://" ;;
esac
# The URL is written into a JSON config file, so it may not need escaping.
case "$PARTYLINE_URL" in
*'"'* | *"'"* | *'\'* | *[[:space:]]*) die 2 "PARTYLINE_URL must not contain quotes, backslashes or whitespace" ;;
esac

[ -n "${PARTYLINE_TOKEN:-}" ] || die 2 "PARTYLINE_TOKEN is required: an agent token (pl_agent_...)"
case "$PARTYLINE_TOKEN" in
pl_*) ;;
*) die 2 "PARTYLINE_TOKEN must be a Partyline token (pl_...)" ;;
esac
case "$PARTYLINE_TOKEN" in
*[!A-Za-z0-9_-]*) die 2 "PARTYLINE_TOKEN has unexpected characters: expected letters, digits, _ and -" ;;
esac

harness=${PARTYLINE_HARNESS:-}
case "$harness" in
claude | opencode | pi | omp) ;;
*) die 2 "PARTYLINE_HARNESS must be one of claude, opencode, pi, omp (got '${harness}')" ;;
esac

# any_set reports whether at least one of the named variables is non-empty, without printing any
# value. The names are literals of this script, never input.
any_set() {
	for name in "$@"; do
		eval "value=\${$name:-}"
		[ -n "$value" ] && return 0
	done
	return 1
}

# Every harness reads its provider credentials from the environment; the table is also in
# deploy/agent/README.md and the design ("Agent image"). A container without one of them would
# restart forever inside the harness, so it stops here with the variable names instead.
case "$harness" in
claude)
	any_set CLAUDE_CODE_OAUTH_TOKEN ANTHROPIC_API_KEY ANTHROPIC_AUTH_TOKEN ||
		die 2 "claude: set CLAUDE_CODE_OAUTH_TOKEN (from \`claude setup-token\`), ANTHROPIC_API_KEY or ANTHROPIC_AUTH_TOKEN"
	;;
opencode)
	any_set OPENCODE_API_KEY ANTHROPIC_API_KEY OPENAI_API_KEY GEMINI_API_KEY OPENROUTER_API_KEY ||
		die 2 "opencode: set OPENCODE_API_KEY (OpenCode Go key) or a provider key such as ANTHROPIC_API_KEY or OPENAI_API_KEY"
	;;
pi)
	any_set OPENCODE_API_KEY ANTHROPIC_API_KEY OPENAI_API_KEY GEMINI_API_KEY OPENROUTER_API_KEY ||
		die 2 "pi: set OPENCODE_API_KEY (OpenCode Go key) or a provider key such as ANTHROPIC_API_KEY or OPENAI_API_KEY"
	;;
omp)
	any_set OPENCODE_API_KEY ANTHROPIC_API_KEY OPENAI_API_KEY GEMINI_API_KEY OPENROUTER_API_KEY ||
		die 2 "omp: set OPENCODE_API_KEY (OpenCode Go key) or a provider key such as ANTHROPIC_API_KEY or OPENAI_API_KEY"
	;;
esac

# --- workspace (CA-05) -----------------------------------------------------------------------

if [ -n "${PARTYLINE_REPO:-}" ]; then
	case "$PARTYLINE_REPO" in
	*[!A-Za-z0-9._:/@+~-]*) die 2 "PARTYLINE_REPO must be a repository URL: letters, digits and . _ : / @ + ~ - only" ;;
	esac
	ref=${PARTYLINE_REPO_REF:-main}
	case "$ref" in
	*[!A-Za-z0-9._/-]*) die 2 "PARTYLINE_REPO_REF has unexpected characters: expected letters, digits and . _ / -" ;;
	esac
	mkdir -p "$workspace"
	if [ ! -e "$repo_dir/.git" ] && [ -z "$(ls -A "$repo_dir" 2>/dev/null)" ]; then
		log "cloning $PARTYLINE_REPO ($ref) into $repo_dir"
		# --depth 50 keeps the clone small and still lets the AI see recent history.
		git clone --depth 50 --branch "$ref" "$PARTYLINE_REPO" "$repo_dir"
	else
		log "$repo_dir already holds a workspace: not cloning"
	fi
fi

if [ -d "$repo_dir" ]; then
	cd "$repo_dir"
else
	mkdir -p "$workspace"
	cd "$workspace"
fi

# --- Partyline login and harness registration ------------------------------------------------

# `partyline setup` with --url and --token-stdin is the non-interactive login: it saves
# ~/.config/partyline/config.json (mode 0600) with the instance URL and the agent token, then
# registers this one harness. --always records the choice in the harness config, so the
# extension, plugin and MCP server of the harness connect in every session of the container, not
# only in the one the launcher starts. Nothing asks for credentials: setup never does.
log "registering $harness with $PARTYLINE_URL"
if ! printf '%s\n' "$PARTYLINE_TOKEN" |
	partyline setup --url "$PARTYLINE_URL" --token-stdin --harness "$harness" --always --yes; then
	die 1 "partyline setup failed: $harness is not registered (the lines above say why)"
fi

# Claude Code answers a few questions in its TUI before it accepts a message: the first-run
# theme, whether it trusts the files of the folder, and whether it may use the API key found in
# the environment (it stores the last 20 characters of an approved key in ~/.claude.json, which
# is what the prompt does when a person answers it). Nobody can press a key inside a container,
# so the session would sit on those prompts for ever. Seed exactly those answers in
# ~/.claude.json, which setup has just written. Nothing here touches permissions: a tool call
# still asks for approval.
if [ "$harness" = claude ] && [ -f "$HOME/.claude.json" ]; then
	if command -v node >/dev/null 2>&1; then
		if ! node -e '
const fs = require("fs");
const [file, folder] = process.argv.slice(1);
let config = {};
try { config = JSON.parse(fs.readFileSync(file, "utf8")) || {}; } catch { config = {}; }
config.hasCompletedOnboarding = true;
if (!config.theme) config.theme = "dark";
config.projects = config.projects || {};
config.projects[folder] = Object.assign(config.projects[folder] || {}, { hasTrustDialogAccepted: true });
const apiKey = process.env.ANTHROPIC_API_KEY || "";
if (apiKey) {
  const tail = apiKey.slice(-20);
  const responses = config.customApiKeyResponses || {};
  const approved = (responses.approved || []).filter((k) => k !== tail);
  approved.push(tail);
  responses.approved = approved;
  responses.rejected = (responses.rejected || []).filter((k) => k !== tail);
  config.customApiKeyResponses = responses;
}
fs.writeFileSync(file, JSON.stringify(config, null, 2) + "\n", { mode: 0o600 });
fs.chmodSync(file, 0o600);
' "$HOME/.claude.json" "$PWD"; then
			log "warning: could not seed the Claude Code first-run answers; the session may wait on them"
		fi
	else
		log "warning: node is missing: the Claude Code first-run answers are not seeded"
	fi
fi

# --- unattended Claude Code ------------------------------------------------------------------

# Nothing in this container can answer a prompt, and the launcher must start every harness
# without one. Claude Code 2.1.x asks at every launch to acknowledge
# --dangerously-load-development-channels, the flag `partyline claude` needs because Partyline is a
# raw MCP server channel and not a plugin on the approved allowlist. The answer is not stored, and
# there is no supported non-interactive way to pre-acknowledge it: `--channels` takes only
# allowlisted plugin entries, `-p` ignores the development flag, and no settings key or environment
# variable exists (deploy/agent/README.md, design.md). The session already runs under `script`,
# which owns a pty, so for Claude Code the entrypoint answers that one prompt with Enter once the
# harness has started. Nothing else is typed and no other prompt is answered.
export PARTYLINE_UNATTENDED=1

# start_ack feeds the pty of `script` a few Enters while Claude Code starts. fd 9 stays open for
# the whole session: a closed stdin could make the TUI read an end of file.
ack_fifo=
ack_dir=
ack_writer=
start_ack() {
	ack_dir=$(mktemp -d "${TMPDIR:-/tmp}/partyline-ack.XXXXXX")
	ack_fifo="$ack_dir/ack"
	mkfifo "$ack_fifo"
	# O_RDWR, so opening the FIFO neither blocks nor gives the reader an end of file.
	exec 9<>"$ack_fifo"
	(
		for _ in 1 2 3 4 5 6; do
			sleep 2
			printf '\r' >&9
		done
	) &
	ack_writer=$!
}

# stop_ack stops the writer and removes the FIFO once the session ended.
stop_ack() {
	if [ -n "$ack_writer" ]; then
		kill "$ack_writer" 2>/dev/null || true
		wait "$ack_writer" 2>/dev/null || true
		ack_writer=
	fi
	if [ -n "$ack_fifo" ]; then
		exec 9>&-
		exec 9<&-
		rm -rf "$ack_dir"
		ack_fifo=
		ack_dir=
	fi
}

# --- the session -----------------------------------------------------------------------------

# script -qfec runs the launcher under a pty, so the harness sees a terminal it can draw on; -e
# makes the exit status the harness's own, which the backoff below uses.
log "starting a principal $harness session in $PWD"

child=
stop() {
	log "stopping"
	[ -n "$child" ] && kill -TERM "$child" 2>/dev/null
	exit 0
}
trap stop TERM INT

backoff=1
while :; do
	started=$(date +%s)
	set +e
	if [ "$harness" = claude ]; then
		start_ack
		script -qfec "partyline $harness --principal" /dev/null <"$ack_fifo" &
	else
		script -qfec "partyline $harness --principal" /dev/null &
	fi
	child=$!
	wait "$child"
	status=$?
	set -e
	child=
	stop_ack
	ran=$(($(date +%s) - started))

	# A session that stayed up long enough counts as healthy: the next crash starts at 1s again.
	if [ "$ran" -ge 600 ]; then
		backoff=1
	fi
	log "$harness exited with status $status after ${ran}s; restarting in ${backoff}s"
	sleep "$backoff"
	backoff=$((backoff * 2))
	if [ "$backoff" -gt 60 ]; then
		backoff=60
	fi
done
