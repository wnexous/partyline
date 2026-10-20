# Contract Notes (from agent-channel Phase 1: server side)

Binding conventions Phase 2 (channel side, T5-T9) and the web app follow. Types live in `internal/api/dto` (AD-003).

## Agent identity
- Every agent request sends `Authorization: Bearer pl_agent_…` and `X-Partyline-Agent: <name>`. The server slugifies the name
  with `agents.Slug` (lowercase; runs of other characters → one `-`; leading/trailing `-._` dropped; cut to 48; empty →
  `agent`); the result matches `dto.AgentNamePattern` (`^[a-z0-9][a-z0-9._-]{0,47}$`). The channel should send the slug it
  computed (same rule) so `welcome.agent.name` equals what it expects.
- The agent is always resolved server-side from (token owner, slug); the principal's `AgentID` is never taken from client input.
- Registration happens only on the WebSocket (`Registry.Upsert`, idempotent per owner+name). REST calls for a name that never
  connected answer 403 `forbidden` "agent not registered: connect the channel first" (`agents.ErrNotRegistered`).
- User sessions and user tokens on any `/api/v1/agent/...` route (WS included) → 403 "agent token required"
  (`agents.ErrAgentTokenRequired`); anonymous → 401. Agent tokens elsewhere → 403 (core middleware).
- Deleting an agent (`DELETE /agents/{id}`, owner only) removes memberships, grants, cursors and closes its socket with
  code 4001 reason `deleted`; a later connection with the same name registers a new agent (new id).

## Agent WebSocket `GET /api/v1/agent/ws`
- Frames are JSON text messages, see `dto.Agent*Frame`. Order: `welcome` → backlog → live `message` frames.
- `welcome.agent = {id, name, owner}` (owner = owner's username).
- Backlog: per member room (paused rooms skipped), messages after the cursor excluding the agent's own, oldest first, at most
  `agents.BacklogLimit` (50). More pending → first a `notice` `{room, text: "<n> older messages skipped; use the history tool"}`
  and the cursor jumps to just before the first delivered message. Live messages already sent by the backlog are not repeated,
  but delivery is at least once: the channel must dedupe by message id (AGT-10).
- `message` frame = `{"type":"message","room":{"id","name"},"trusted":bool,"message":dto.Message}`; `trusted` is computed for
  the receiving agent (`delivery.Trusted(message.author_user_id, agent owner)`).
- Ack: channel sends `{"type":"ack","message_id":"…"}` after writing the notification; the cursor moves to that message's seq,
  never backwards. Acks for unknown messages or rooms without the agent are ignored. No reply frame.
- Close codes: 4000 `replaced` (a newer connection for the same agent; do not fight it - but the channel's reconnect loop will
  replace back, so Phase 2 should stop reconnecting or back off after `replaced`), 4001 `deleted`, 1013 `lagged` (push queue of
  256 overflowed; reconnect, the backlog recovers), 1001 on server shutdown. Server pings every 25 s.
- Presence: online on connect (live `agent.presence` to users with access, `GET /agents` `online`), offline when the current
  socket closes; `last_seen_at` stamped on both.

## Agent REST (all `agent token + X-Partyline-Agent`)
| Method | Path | Body / query | Answer |
| --- | --- | --- | --- |
| GET | `/api/v1/agent/rooms` | - | `List[AgentRoom]` (members carry `online` for agents) |
| GET | `/api/v1/agent/rooms/{room}/messages` | `?limit=` (default 20, clamped to 100), `?before=<message id>` | `Page[AgentMessage]`, newest first; next page: `before=<last item id>` |
| POST | `/api/v1/agent/rooms/{room}/messages` | JSON `PostMessageRequest` or multipart (`message` part + `file` parts) | 201 `Message` |
| POST | `/api/v1/agent/messages/{id}/reply` | `ReplyRequest{text}` | 201 `Message` with `reply_to`; recipients = `[chat id]` when the original author is a chat |
| GET | `/api/v1/agent/attachments/{id}` | - | file bytes (same disposition rules as the user route) |

- `{room}`: room id, else a room name matched case-insensitively (or by its slug) among the agent's rooms.
- Error texts the channel maps to tool errors:
  - 403 `forbidden` "not a member of room <room>" (`<room>` echoes the reference as given) - unknown room or no membership.
  - 409 `conflict` "ambiguous room name".
  - 429 `rate_limited` "rate limited: at most 30 messages per minute" - posts and replies share the per-agent budget
    (`agents.PostLimit` 30 per sliding `PostWindow` 1 min; counted when the room resolves, before validation).
  - 404 `not_found` "message not found" - reply to an unknown message or one in a room without the agent.
  - 422 `invalid` field `before` - history cursor not a message of the room; other 422s come from `rooms.Post` (`text`,
    `recipients`, `reply_to`).
  - 403 "agent not registered: connect the channel first".

## Server wiring
- `internal/agents`: `Presence` (one session per agent), `Registry` (`Upsert`, `Lookup`, `Rooms`, `Online`, `AllowPost`, user
  management methods), `Gateway` (`ServeHTTP`, `Push`, `Online`, `Message`, `Close`). `App.Presence`, `App.Agents`,
  `App.Gateway`; `api.Deps.Agents` and `api.Deps.AgentWS`.
- `delivery.AgentDispatcher` (`NewAgentDispatcher` subscribes at construction; `Run`) pushes `MessageCreated` to online plan
  targets through `delivery.AgentPusher` (the Gateway) with `delivery.MessageLoader` (`Gateway.Message`); paused rooms and the
  author agent get nothing.
- `rooms.Service.Details(views)` and `MessageRef(id)` are trusted, unauthorized helpers for server components.
- User routes: `GET /agents`, `PATCH /agents/{id}` (owner), `DELETE /agents/{id}` (owner), `GET /agents/{id}/sharing`
  (owner or use grantee), `PUT /agents/{id}/sharing` (owner). Room member DTOs now fill `online` for agent members.
- testkit: `Client.DialAgent(name) (*AgentConn, *Response)` with `Welcome`, `Message`, `Notice`, `Next`, `ExpectNone`, `Ack`,
  `Closed`, `Close`; live and agent sockets share one background reader.
