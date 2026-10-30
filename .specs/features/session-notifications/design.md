# Design: session notifications

## Identity of a session for persistence
`session_key` = the harness session id when the session sent one (`X-Partyline-Harness-Session`,
SESS-51..), else `label:<label>` (lives with the label). The principal uses its pinned id (or
`principal` when none). Helpers in `internal/agents` resolve a live connection → session_key.

## Data (migration 0031)
- `session_room_state(agent_id, session_key, room_id, read_seq INTEGER NOT NULL DEFAULT 0,
  notify TEXT NOT NULL CHECK (notify IN ('on','mentions')), updated_at, PRIMARY KEY (agent_id,
  session_key, room_id))`, FK agent/room ON DELETE CASCADE. Missing row ⇒ defaults: principal
  `on`, parallel `mentions`; read_seq for a missing row = the session's join point (first connect
  of that session_key in this room) — store it on first sight to avoid flooding a brand-new session
  with the whole history (a new session starts "caught up" at the moment it connects, and a
  mention then brings at most the 50 previous messages as context).
- The principal's existing `agent_cursors` stay the source of truth for the principal's backlog;
  `session_room_state.read_seq` for the principal mirrors it (write both) so unread counts are
  uniform.
- `agent_session_deliveries` keeps tracking live pushes/acks per message per session; the ack of
  a pushed message moves that session's read_seq.

## Routing (internal/agents gateway Push)
For a room message M and AI A (after delivery mode + loop guard decide A wants it at all):
1. targets = { principal if notify(principal, room) = on, or M mentions `@A` plain } ∪
   { each live parallel session S if notify(S, room) = on, or M addresses `@A:S.label` } ∪
   { principal if M addresses a parallel session and notify(principal, room) = on } (SN-04).
2. Self-authored M (author = A): apply SESS-38/49 exactly as today (only the explicitly mentioned
   sibling / principal), never the writer.
3. For each target T: frames = unread(T, room) since read_seq (cap 50, oldest first, excluding
   messages T itself wrote) + M; push as a batch (existing per-message frames, with a
   `context="true"` attribute on the catch-up ones so the model knows they are context, not new
   requests), one delivery row per message per session (reuse agent_session_deliveries keyed by
   session owner).
4. Non-targets: nothing pushed; unread grows implicitly (read_seq < room max seq).
5. Platform events: principal only (unchanged).
6. Offline principal: unchanged (queue/backlog via agent_cursors). Offline parallel session: no
   queue (it reads unread when it comes back or is mentioned).

## Tools (internal/channel)
- `list_rooms`/`room_info`: per conversation `unread` (for the calling session) and `notify`.
- `read_unread {room}`: returns unread frames, marks read.
- `set_notifications {room, notify: on|mentions}` for the calling session.

## API + web
- `GET /agents/{id}` sessions get `rooms_notify` for the room in context? Keep simple: `GET
  /agents/{id}/sessions/{label}/notifications` (list per room) and `PUT .../notifications/{room}`
  (manage level). Member card session line shows a muted icon for `mentions` sessions in that room;
  profile Sessions list has per-session "Notificações" sheet listing rooms with on/mentions
  toggles (ChoiceGroup / Switch). FSD: `features/session-notifications`.

## Edits/deletes
Follow every session that has a delivery row for the original message (SN-09), plus the addressed
session rule (SESS-39) as today.

## Migration/compat
Old clients (no harness id) keyed by label; principal default `on` keeps today's behaviour for
everyone who never touches settings, except SN-04 (principal now sees mentions of its siblings)
and SN-03 (a mentioned parallel session now gets context).
