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
- Auth: `partyline runner` uses the saved CLI login or the device-code flow (`internal/devices`)
  to get a token of a new kind `runner` (scoped: only the runner routes). `POST
  /api/v1/runner/register {name, machine}` → runner id (idempotent per token).
- `GET /api/v1/runner/ws` (WebSocket, the token): hello `{docker_version | docker_error,
  containers: [{name, state}]}`; server → runner commands `{id, op: run|start|stop|restart|remove|
  logs|state, ...}`; runner → server `{id, ok, error?, data?}` and unsolicited `state` events
  (from `docker events --filter label=partyline.agent` or a 15 s poll). Pings like the agent WS.
- Ops payloads carry only validated fields. `run` carries `{name, image, volume, cpus, memory_mb,
  labels, env: {VAR: value}}`; the runner writes env to `~/.local/state/partyline/agents/<name>.env`
  (0600) and runs `docker run -d --name … --restart unless-stopped --env-file … --cpus …
  --memory …m -v <volume>:/workspace --label partyline.agent=<agent-id> <image>` with
  `exec.Command("docker", args...)` (no shell). Each op validates again on the runner.
- Delivery: commands for an offline runner fail fast with `runner_offline` (no queue in v1); the
  web shows it.

## Packages
- `internal/runners`: registry (register, list, rename, delete+revoke token), hub of live runner
  connections (`Do(ctx, runnerID, op) (Result, error)` with timeout 60 s, logs 15 s), state
  updates into container_agents.
- `internal/ownersecrets`: CRUD; `Open(ctx, owner, ids)` for the container service only.
- `internal/containeragents`: create (identity with machine `runner:<runner-id>` and folder
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
`ghcr.io/wnexous/partyline-agent:<version>`: Debian slim + tini + git + ca-certificates + node/bun
+ pinned Claude Code, pi, OpenCode, omp + the partyline binary. Entrypoint: clone
`PARTYLINE_REPO`@`PARTYLINE_REPO_REF` into `/workspace/repo` when empty; configure the harness
credentials from env under the container user's home; run `partyline $PARTYLINE_HARNESS --always
--principal` inside `script -qfec` (TTY), restarting with backoff. Non-root, no Docker socket.
Release workflow builds and pushes it on each tag.

## Web
FSD: `entities/runner`, `entities/container-agent`, `features/{manage-runners, manage-secrets,
create-container-agent, operate-container}`; "Servidores" (runners list + "how to add": the two
commands to run, copyable) and "Segredos" in account/settings; "Nova IA em container" in the new-AI
flow; "Container" section in the AI profile (state, actions, logs). Reuse the canonical components.
