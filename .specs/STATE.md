# STATE

## Decisions

### AD-001
- **Decision**: Partyline ships as one Go binary (modular monolith) that serves the REST API, the live WebSocket, the embedded web app and runs every connector in-process; SQLite (pure Go `modernc.org/sqlite`, WAL) is the only datastore.
- **Reason**: The target is "docker run and done" for a person or a small company. One process, one volume, no CGO, tiny distroless image.
- **Trade-off**: No horizontal scaling; a single instance owns all connector sessions. Postgres would need a second store implementation.
- **Scope**: all features, deployment, store layer.
- **Date**: 2026-10-19
- **Status**: active

### AD-002
- **Decision**: The room is the only routing primitive. Agents (Claude Code sessions), external chats (WhatsApp group, Telegram chat, Discord channel, webhook chat) and users are room members; a message posted by one member is delivered to the others by fixed delivery rules.
- **Reason**: Group→session routing, session↔session talk and cross-platform bridges become one mechanism with one permission model.
- **Trade-off**: Simple "chat X → session Y" needs a room. The UI hides this with a one-click "route chat to agent" action that creates the room.
- **Scope**: core-platform, agent-channel, connectors, web-app.
- **Date**: 2026-10-19
- **Status**: active

### AD-003
- **Decision**: Go DTO structs in `internal/api/dto` are the single source of the HTTP/WS contract. TypeScript types are generated from them with `tygo` into `web/src/lib/api/types.gen.ts`; nobody hand-writes API types in TS.
- **Reason**: Zero duplicated contract code; CI fails when generated files drift.
- **Trade-off**: No OpenAPI document; external API consumers read the DTO package and `docs/api.md`.
- **Scope**: core-platform, web-app.
- **Date**: 2026-10-19
- **Status**: active

### AD-004
- **Decision**: The Claude Code side is `partyline channel`, a hand-written MCP stdio server inside the same binary (no MCP SDK). It answers `server/discover` with JSON-RPC error -32601 and negotiates protocol `2025-11-25` (or the client's older version) so Claude Code registers it as a channel. It talks to the server over the agent WebSocket (push + ack) and the REST API (tools).
- **Reason**: Claude Code v2.1.303 probes `server/discover` with revision 2026-07-28 first; a server on that revision is not registered as a channel. A tiny hand-written server is fully testable and has no runtime dependency (no Bun/Node on the agent host).
- **Trade-off**: We maintain JSON-RPC framing ourselves and must track Claude Code channel contract changes (research preview).
- **Scope**: agent-channel.
- **Date**: 2026-10-19
- **Status**: active

### AD-005
- **Decision**: Sharing uses one `grants` table: (resource type, resource id, subject = user id or `*` for everyone, level). Visibility presets in the UI (private / shared / public) are just grant sets. Instance admins manage accounts but get no implicit access to other users' rooms, agents or connectors.
- **Reason**: One authorization code path for rooms, agents and connectors; "private" really means private, even inside a company.
- **Trade-off**: An admin cannot moderate a private room without being granted access.
- **Scope**: core-platform, agent-channel, connectors, web-app.
- **Date**: 2026-10-19
- **Status**: active

### AD-006
- **Decision**: Message content is data, never instructions. Every message delivered to an agent carries a server-computed, recipient-relative `trusted` flag (true only when the author resolves to the recipient agent's owner: the owner in the web app, the owner's agents, or the owner's verified chat identities). Partyline never declares the `claude/channel/permission` capability.
- **Reason**: Channels inject text into a live coding session; anyone in a bridged group could attempt prompt injection. Trust must be explicit and not forgeable by message text.
- **Trade-off**: Teammates' messages show as untrusted to your agents unless a later feature adds per-user trust.
- **Scope**: core-platform, agent-channel, connectors.
- **Date**: 2026-10-19
- **Status**: active

### AD-007
- **Decision**: Connectors implement one Go interface and describe their configuration with a declarative field schema served by the API; the web app renders connector forms from that schema. Connector secrets are encrypted at rest with AES-256-GCM using the instance secret key.
- **Reason**: Adding a platform touches one package; no per-connector UI code; a stolen database file does not leak bot tokens.
- **Trade-off**: Forms are generic (no per-platform custom widgets).
- **Scope**: connectors, web-app.
- **Date**: 2026-10-19
- **Status**: active

### AD-008
- **Decision**: A verified identity belongs to the connector instance it was linked through, not to the connector kind. Messages resolve to a user only when they arrive through that same connector. Kinds whose caller chooses sender ids (webhook) link only codes of the connector's owner.
- **Reason**: The connectors validation probe (S1) showed that a webhook, a Telegram connector with a custom `api_base` or a WhatsApp connector with its own `base_url` lets its owner claim any sender id, so kind-wide identities made trust forgeable (breaks AD-006).
- **Trade-off**: The same phone/account must be linked once per connector; identities linked before migration `0003` were dropped. Linking through someone else's connector still means trusting its owner.
- **Scope**: connectors, identities, web-app (identities page).
- **Date**: 2026-10-20
- **Status**: active
- **Amended** (2026-10-20, connectors fix iteration 2, S5): a pairing code is requested for one connector the user owns or may use and links only through it (migration `0005`); changing a connector's endpoint fields deletes the identities linked through it (audited). Linking through a shared connector is now always the user's explicit choice.

## Handoff

- **Feature**: core-platform
- **Phase / Task**: Execute, Verifier fix iteration 1 (T28-T44) complete
- **Completed**: T28-T44 on branch `fix/core-verify-1` (see tasks.md Phase 5)
- **In-progress** (file:line): none
- **Next step**: re-run the Verifier (iteration 2 of 3) over the fix range, then merge into main (agent-channel touches
  `internal/app/app.go` and `internal/api/router.go`; this branch changes both minimally)
- **Blockers**: none
- **Uncommitted files**: none
- **Branch**: fix/core-verify-1
