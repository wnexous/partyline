# Design: container agents

## Data (migration 0029)
- `servers(id, owner_id → users, name, ssh_host, ssh_port, ssh_user, private_key_sealed BLOB,
  public_key TEXT, host_key TEXT NULL /*pinned authorized_keys line*/, created_at, updated_at)`,
  unique (owner_id, name).
- `owner_secrets(id, owner_id → users, name, value_sealed BLOB, created_at, updated_at)`,
  unique (owner_id, name). Name pattern `^[A-Za-z][A-Za-z0-9_-]{0,63}$`.
- `container_agents(agent_id PK → agents ON DELETE CASCADE, server_id → servers, harness,
  image, repo_url NULL, repo_ref NULL, volume, container_name, env JSON /*[{var, secret_id}]*/,
  cpus REAL, memory_mb INT, desired TEXT /*running|stopped*/, last_error TEXT NULL,
  token_id → tokens, created_at, updated_at)`.
- Sealing: `internal/secrets` Box (AES-256-GCM, instance key) as connectors already do.

## Packages
- `internal/servers`: service for servers + key generation (ed25519 via `golang.org/x/crypto/ssh`),
  `Check(ctx, server)` (dial, TOFU pin through `ssh.HostKeyCallback`, run `docker version
  --format '{{.Server.Version}}'`). Timeouts: dial 10 s, command 30 s.
- `internal/ownersecrets`: CRUD; `Open(ctx, owner, ids)` returns plaintext only to the container
  service.
- `internal/containers`: `Runner` interface `{Run(ctx, Spec) error; Start; Stop; Restart; Remove(ctx,
  name string, volume bool); Logs(ctx, name, tail int) ([]string, error); State(ctx, name)
  (State, error)}` with `sshRunner` executing the `docker` CLI over one SSH session per command.
  Every arg is validated and shell-quoted (`shellQuote`); secrets never go on the command line:
  `Run` streams an env file through the session's stdin into `~/.partyline/agents/<name>.env`
  (umask 077, `cat >`), then `docker run --env-file … --name … --restart unless-stopped
  --cpus … --memory … -v <volume>:/workspace --label partyline.agent=<id> <image>`. Tests use an
  in-process SSH server (x/crypto/ssh) that records commands and stdin.
- `internal/containeragents`: orchestrates create (agents.Register-like identity with machine
  `container:<server-id>` and folder `/workspace`, agent token via `users.CreateToken` kind agent,
  env = PARTYLINE_URL (instance public URL), PARTYLINE_TOKEN, PARTYLINE_HARNESS, PARTYLINE_REPO,
  PARTYLINE_REPO_REF + mapped secrets), lifecycle ops, audit, `last_error` updates. Delete revokes
  the token.
- API (`internal/api`): `/api/v1/servers` (GET, POST), `/servers/{id}` (PATCH, DELETE),
  `/servers/{id}/check` (POST); `/api/v1/secrets` (GET names only, PUT `/secrets/{name}`, DELETE);
  `/api/v1/container-agents` (POST create), `/agents/{id}/container` (GET state, POST
  `start|stop|restart`, GET `logs?tail=200`, DELETE `?volume=true|false`). Authorisation: servers
  and secrets by owner; container ops via the AI's manage level (`managedAgent`).
  DTOs in `internal/api/dto`, regenerate tygo.

## Agent image (`deploy/agent/`)
`ghcr.io/wnexous/partyline-agent:<version>`: Debian slim + tini + git + ca-certificates + bun/node
+ pinned Claude Code, pi, OpenCode, omp + the partyline binary of the same version. Entrypoint
`partyline-agent-entrypoint`:
1. If `/workspace/repo` is empty and `PARTYLINE_REPO` is set: `git clone --branch "$REF"`.
2. Configure the harness from env (e.g. pi/OpenCode provider key for OpenCode Go; Claude Code
   `CLAUDE_CODE_OAUTH_TOKEN`), writing config under the container user's home only.
3. `cd /workspace/repo` (or `/workspace`) and run `partyline $PARTYLINE_HARNESS --always` inside
   `script -qfec` (the TUIs need a TTY), restarting it after an exit with backoff.
Non-root user; no Docker socket mounted. The release workflow builds and pushes it on each tag.

## Web
FSD slices: `entities/server`, `entities/container-agent`, `features/{add-server, check-server,
manage-secrets, create-container-agent, operate-container}`, pages: account → "Servidores" and
"Segredos" sections (or `pages/servers`), "Nova IA em container" entry in the new-AI flow, and a
"Container" section in the AI profile (widgets/profile-panel + contacts page) with state, actions
and logs (monospace, scrollable, last 200 lines). Reuse FormDialog, ConfirmDialog, CopyField,
SelectField, EmptyState, RelativeTime.

## Open points
- Network: the Partyline server must reach the server's SSH port (prod VM → dev box on the LAN).
  `Check` surfaces "cannot connect" clearly.
- Image size; pinning harness versions; how each harness reads its credentials from env
  (verify per harness in T5).
