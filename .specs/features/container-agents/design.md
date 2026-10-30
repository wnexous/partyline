# Design: container agents (runner model)

A server joins by running `partyline runner`, which logs in to Partyline and keeps an outbound
WebSocket, like a self-hosted CI runner. Partyline never connects to the server: no SSH, no keys,
no inbound port, works behind NAT.

## Data (migration 0029)
- `runners(id, owner_id → users ON DELETE CASCADE, name, token_id → tokens, machine TEXT,
  docker_version TEXT NULL, docker_error TEXT NULL, last_seen_at INT NULL, created_at,
  updated_at)`, unique (owner_id, name).
- `owner_secrets(id, owner_id → users ON DELETE CASCADE, name, value_sealed BLOB, created_at,
  updated_at)`, unique (owner_id, name). Name pattern `^[A-Za-z][A-Za-z0-9_-]{0,63}$`.
- `container_agents(agent_id PK → agents ON DELETE CASCADE, runner_id → runners (RESTRICT),
  harness, image, repo_url NULL, repo_ref NULL, volume, container_name, env JSON
  /*[{var, secret_id}]*/, cpus REAL, memory_mb INT, desired TEXT CHECK in (running, stopped),
  state TEXT /*last reported: running|stopped|error|unknown*/, last_error TEXT NULL,
  token_id → tokens, created_at, updated_at)`.
- Sealing with `internal/secrets` Box (AES-256-GCM, instance key).

## Runner protocol
- Token path (decided in T2): `partyline runner` obtains a token of the new kind `runner` from the
  device-code flow (`internal/devices`), which takes `kind: "runner"` on `POST /api/v1/devices`
  (default `agent`, so setup is unchanged). The approving user sees the machine and approves; the
  poll delivers a `pl_runner_…` secret once, named `<machine> (runner)`. Nothing else creates the
  kind: `POST /api/v1/tokens` offers agent and user only, and a runner token reaches only
  `/api/v1/runner/*` (the middleware restricts each token kind to its routes). The token is scoped
  to the approving owner; the AI inside a container never holds one. Then `POST
  /api/v1/runner/register {name, machine}` → runner (201 first time, 200 after; idempotent per
  token, enforced by a unique index on `runners.token_id`). The name defaults to the machine and
  is made unique per owner with `-2`, `-3`….
- `GET /api/v1/runner/ws` (WebSocket, the token): hello `{docker_version | docker_error,
  containers: [{name, state, error?}]}` updates `docker_version`/`docker_error`/`last_seen_at` and
  the container states; server → runner commands `{type: "command", id, op:
  run|start|stop|restart|remove|logs|state, run?, name?, tail?, remove_volume?}`; runner → server
  results `{type: "result", id, ok, error?, logs?, containers?}` and unsolicited `state` events
  `{type: "state", containers}` (from `docker events --filter label=partyline.agent` or a 15 s
  poll). Pings like the agent WS. A new connection replaces the old one at once.
- Ops payloads carry only validated fields (`internal/runners/spec` validates on the server and
  again on the runner; the image repository is the default agent one or an entry of
  `PARTYLINE_RUNNER_IMAGES`). `run` carries `{name, image, volume, cpus, memory_mb, labels, env:
  {VAR: value}}`; the runner writes env to `~/.local/state/partyline/agents/<name>.env` (0600) and
  runs `docker run -d --name … --restart unless-stopped --env-file … --cpus … --memory …m -v
  <volume>:/workspace --label partyline.agent=<agent-id> <image>` with `exec.Command("docker",
  args...)` (no shell). Each op validates again on the runner.
- Delivery: commands for an offline runner fail fast with `runner_offline` (409, no queue in v1);
  an operation has its own timeout (`Do`: 60 s, logs and state 15 s) and a runner that does not
  answer is `ErrTimeout`; the web shows both. Delete revokes the token, closes the connection and
  is refused with `runner_in_use` (409) while one of its containers exists.

## Packages
- `internal/runners/spec`: the frame types (hello, command, result, state) and the validators both
  sides apply (container/volume/env names, image + allowlist, cpus/memory, repo URL and ref). Pure
  Go, imported by the CLI and the server (CA-09).
- `internal/runners`: registry (register, list, rename, delete+revoke token; 404 for another
  owner's runner) and hub of live runner connections (`Do(ctx, runnerID, cmd) (Result, error)` with
  per-op timeouts), state updates into container_agents, audit of register/rename/delete.
- `internal/ownersecrets`: CRUD; `Open(ctx, owner, ids)` for the container service only.
- `internal/containeragents`: create (identity with machine `container:<container-name>` and folder
  `/workspace`, agent token, env = PARTYLINE_URL, PARTYLINE_TOKEN, PARTYLINE_HARNESS,
  PARTYLINE_REPO, PARTYLINE_REPO_REF + mapped secrets), lifecycle, logs, delete (+ volume) and
  token revoke, audit, last_error.
- CLI `internal/cli/runner.go`: `partyline runner [--name N] [--check]`, `partyline runner install`
  (systemd user unit `partyline-runner.service` with `Restart=always`; prints the
  `loginctl enable-linger` hint), `partyline runner uninstall`. Executor `internal/runnerexec` with
  validators shared with the server (`internal/runners/spec` package used by both).
- API: owner routes `/api/v1/runners` (GET), `/runners/{id}` (PATCH name, DELETE),
  `/api/v1/secrets` (GET names, PUT `/secrets/{name}`, DELETE), `/api/v1/container-agents` (POST),
  `/agents/{id}/container` (GET, POST `{action}`, GET `logs?tail=200`, DELETE `?volume=`).
  Runner routes `/api/v1/runner/register`, `/runner/ws`. DTOs + tygo.

## Agent image (`deploy/agent/`)
`ghcr.io/wnexous/partyline-agent:<version>`: Debian slim + tini + git + ca-certificates + curl +
util-linux `script` + node LTS + bun + pinned Claude Code, pi, OpenCode, omp + the partyline
binary (built in a Go stage with the server Dockerfile's flags). Entrypoint (`deploy/agent/entrypoint.sh`,
POSIX sh, `set -eu`, tracing off): validate `PARTYLINE_URL`, `PARTYLINE_TOKEN` and
`PARTYLINE_HARNESS`; clone `PARTYLINE_REPO`@`PARTYLINE_REPO_REF` into `/workspace/repo` when
empty; log the CLI in and register the harness in one non-interactive step
(`partyline setup --url … --token-stdin --harness … --always --yes`, which writes
`~/.config/partyline/config.json` mode 0600); run `partyline $PARTYLINE_HARNESS --principal`
inside `script -qfec` (TTY), restarting with backoff 1s→60s, reset after 10 min up. `--always` is
a `partyline setup` flag, not a launcher flag: setup records in the harness config that the
extension, plugin and MCP server act in every session of the container, while `--principal`
makes the session the AI's principal one (SESS-57). Non-root user `agent` (uid/gid 1000, home
`/home/agent`), no Docker socket, no secret in the image. Release workflow builds and pushes it
on each tag (`image-agent` job, amd64 + arm64, QEMU for the runtime stage).

Harness credentials come only from the environment — each harness reads its provider keys
itself, so the entrypoint writes no credential file and requires at least one variable per row
(it names the missing ones instead of starting a session that could not authenticate):

| Harness | `PARTYLINE_HARNESS` | Credential env (any one) |
| --- | --- | --- |
| Claude Code | `claude` | `CLAUDE_CODE_OAUTH_TOKEN` (from `claude setup-token`), `ANTHROPIC_API_KEY`, `ANTHROPIC_AUTH_TOKEN` |
| OpenCode | `opencode` | `OPENCODE_API_KEY` (OpenCode Go key), `ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, `GEMINI_API_KEY`, `OPENROUTER_API_KEY` |
| pi | `pi` | `OPENCODE_API_KEY`, `ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, `GEMINI_API_KEY`, `OPENROUTER_API_KEY` |
| omp | `omp` | `OPENCODE_API_KEY`, `ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, `GEMINI_API_KEY`, `OPENROUTER_API_KEY` |

`PARTYLINE_WORKSPACE` (default `/workspace`) is optional; `/workspace` is the one volume the
container AI keeps (CA-05).

## Unattended Claude Code (decision, T6)
Claude Code 2.1.x shows a confirmation dialog on every launch of
`--dangerously-load-development-channels` and does not remember the answer, so a container session
would sit on it. There is no supported non-interactive way to pre-acknowledge it (checked against
2.1.304 and its documentation): `--channels` accepts only plugin entries from the approved (or
managed `allowedChannelPlugins`) allowlist, a raw `server:` entry needs the development flag, the
flag is ignored with `-p`/the Agent SDK, and `channelsEnabled` gates availability without removing
the dialog. No settings key or environment variable records the acknowledgement.

Decision: the launcher keeps `--dangerously-load-development-channels server:partyline` (it is
the only way a raw MCP server registers as a channel), the agent image exports
`PARTYLINE_UNATTENDED=1`, and for `claude` the entrypoint feeds a few Enters into the pty it
already creates with `script` while the harness starts: the prompt's default choice is the
acknowledgement, so the session proceeds without a person. This is a last resort, isolated to the
entrypoint's `claude` branch and removed when Anthropic offers a supported acknowledgement; see
`deploy/agent/README.md`, "Unattended Claude Code".

## Web
FSD: `entities/runner`, `entities/container-agent`, `features/{manage-runners, manage-secrets,
create-container-agent, operate-container}`; "Servidores" (runners list + "how to add": the two
commands to run, copyable) and "Segredos" in account/settings; "Nova IA em container" in the new-AI
flow; "Container" section in the AI profile (state, actions, logs). Reuse the canonical components.
