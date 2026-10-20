# Routing and Context Design

**Spec**: `.specs/features/routing-context/spec.md`
**Status**: Approved (user asked for this flexibility; decisions logged in the spec)
**Architecture**: `.specs/features/core-platform/design.md`; conventions in every feature's `contract-notes.md`.

---

## Data model (migration `0006_routing_context.sql`)

```sql
ALTER TABLE room_members ADD COLUMN display_name TEXT NOT NULL DEFAULT '';
ALTER TABLE room_members ADD COLUMN sign_outbound BOOLEAN NOT NULL DEFAULT 1;
ALTER TABLE room_members ADD COLUMN delivery TEXT NOT NULL DEFAULT 'all' CHECK (delivery IN ('all','mentions','trusted'));
ALTER TABLE room_members ADD COLUMN keywords_json TEXT NOT NULL DEFAULT '[]';
ALTER TABLE rooms ADD COLUMN instructions TEXT NOT NULL DEFAULT '';
ALTER TABLE connectors ADD COLUMN auto_route_scope TEXT NOT NULL DEFAULT 'off' CHECK (auto_route_scope IN ('off','dms','groups','all'));
ALTER TABLE connectors ADD COLUMN auto_route_room_id TEXT NULL REFERENCES rooms(id) ON DELETE SET NULL;
CREATE TABLE auto_route_skips (connector_id TEXT NOT NULL REFERENCES connectors(id) ON DELETE CASCADE,
  chat_id TEXT NOT NULL REFERENCES chats(id) ON DELETE CASCADE, until INTEGER NOT NULL,
  PRIMARY KEY (connector_id, chat_id));
```
Member settings apply to agent members; for other kinds they are ignored (and the API refuses them with 422).

## DTO changes (`internal/api/dto`, regenerate tygo)

- `RoomMember` += `display_name`, `sign_outbound`, `delivery` (`all|mentions|trusted`), `keywords []string`.
- `UpdateMemberRequest` += the same optional fields (pointers).
- `Room` += `instructions`; `UpdateRoomRequest` += `instructions`.
- `Connector` += `auto_route_scope`, `auto_route_room_id`; `UpdateConnectorRequest` += both.
- `ChatRef{id, title, kind, platform}`; `Message.chat *ChatRef` (set when `author_kind == chat`), `AgentMessage` likewise. Web and channel both use it.
- `RoomInfo{room, instructions, members[], me}` for `GET /api/v1/agent/rooms/{room}/info`.
- `ReactRequest{emoji}` for `POST /api/v1/agent/messages/{id}/react`.
- Message kind `reaction` already exists in the kind union; agent reactions are stored as messages of kind `reaction`, `reply_to` = target, `text` = emoji.

## Components

### delivery.Wants (pure)
`Wants(m MemberPrefs, d Delivered) bool` where `MemberPrefs{Delivery, Names []string /* display name + agent name */, Keywords []string}` and `Delivered{Text, Trusted bool, ChatKind string, ReplyToAuthorIsMe bool}`. `all` → true; `trusted` → Trusted; `mentions` → DM chat, reply to me, `@name` (case-insensitive, word boundary), or a keyword on word boundaries. Unit-tested exhaustively.

### Dispatcher and gateway backlog
Both call `Wants` per agent member. Skipped messages still advance the cursor (backlog) and are never pushed live. `history` returns everything (filter applies to push only).

### Outbound signing
`connectors.Sender` builds `Outbound.AuthorName`: empty when the authoring agent member has `sign_outbound=false`; `display_name` when set; else the agent name. Users keep their display name. Adapters already skip the header when `AuthorName` is empty.

### Chat titles
- WhatsApp: group without title → `POST /group/info {"groupJid": jid}` (instance token) → `data.Name`; cached per instance for 10 minutes; `/group/list` refresh also sets titles. DM → `PushName`, else the phone number part of the JID.
  Real response (evolution-go 0.7.2): `{"data":{"JID":"…","Name":"…","Topic":"…","Participants":[…],"ParticipantCount":3,…},"message":"success"}`.
- Telegram: `chat.title`, else first + last name, else `@username`.
- Inbound pipeline: `chats.Upsert` updates the title when a non-empty new title differs (CTX-08).
- `roomName(chat)`: title, else `<Platform> chat`.

### Catch-all routing (inbound pipeline)
After chat upsert, when the chat is in no room: load connector `auto_route_*`; if scope matches chat kind (`dm` ↔ `dms`; `group`/`channel` ↔ `groups`; `all`), no unexpired skip row, room exists and connector owner has `manage` → `rooms.AddMember(chat, outbound on)` with the owner's principal (idempotent on the unique key; concurrent first messages add once) → continue normal posting. Otherwise, if the room is unavailable → connector status detail `auto-route room unavailable` (ROUTE-03). `RemoveMember` of a chat in a connector's catch-all room writes a skip row for 24 h.

### Agent tools (channel)
- Push meta adds `chat_id`, `chat_title`, `chat_kind`, `platform`, `sender_id` when `message.chat` is set.
- `send_message` gains `chat_ids` (alias of the existing recipients param; the old name keeps working).
- New `room_info(room)` → `GET /agent/rooms/{room}/info`.
- New `react(message_id, emoji)` → `POST /agent/messages/{id}/react`; 422 `can only react to chat messages` when the target is not chat-authored.
- Instructions text documents the new meta keys and tools. Welcome backlog adds one notice per room with instructions (INS-03).

### Reactions outbound
Optional adapter interface `connectors.Reactor { React(ctx, Reaction{ChatID, TargetID, TargetSenderID, Emoji}) error }`. Sender: message kind `reaction` → `Reactor.React`; adapters without it fail the outbox row with `reactions not supported`.
- WhatsApp: `POST /message/react {"number": jid, "id": targetID, "reaction": emoji, "fromMe": false, "participant": senderJID(groups only)}`.
- Telegram: `setMessageReaction {chat_id, message_id, reaction:[{type:"emoji", emoji}]}`.
- Discord: `MessageReactionAdd(channel, message, emoji)`.
- Webhook: outbound POST `{event:"reaction", chat_id, target_message_id, emoji, …}` signed as usual.

### Web
- Members panel: agent member row gets a settings popover: display name, "Sign messages" switch, delivery select (All / Only mentions / Only trusted), keywords tag input.
- Room settings sheet: instructions textarea (4,000 chars, counter).
- Connector page: "Catch-all" card: scope select (Off / DMs / Groups / All) + room picker (rooms the user manages) + warning that anyone who writes will reach the room's agents.
- Timeline: chat messages show the chat title and platform badge; member list shows chat titles.

## Risks & Concerns

| Concern | Location | Impact | Mitigation |
| --- | --- | --- | --- |
| Catch-all exposes agents to anyone | inbound pipeline | Prompt injection volume | Off by default; UI warning; trust flag unchanged; `delivery=trusted` option |
| Platform field types differ from docs | adapters | Silent decode failures (seen with websocketEnable) | Fixtures built from captured real responses (lesson L-030) |
| Mention filter misses messages | delivery.Wants | Agent silent in busy groups | Exhaustive unit table; DMs always delivered; history unfiltered |

## Tech Decisions

| Decision | Choice | Rationale |
| --- | --- | --- |
| Where filters run | Server-side before push (not in the channel) | Saves agent context; one rule for live and backlog |
| Reaction storage | Message of kind `reaction` | Reuses outbox, audit and live events |
