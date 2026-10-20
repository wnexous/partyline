# Routing and Context Specification

## Problem Statement

First real use (2026-10-20) showed three gaps. Agents cannot tell which chat, platform or kind of conversation a message came from. Chats only reach an agent after someone adds each one to a room by hand, so a bot cannot answer whoever writes to it. Outbound messages always carry the agent's name as a header, with no way to turn it off. Group chats also show no title, and rooms routed from them get raw ids as names.

## Goals

- [ ] Every message an agent receives says which chat, platform and conversation kind it came from.
- [ ] A connector can route every new chat (all, only DMs, or only groups) to a room automatically, so an agent can answer anyone.
- [ ] Each agent membership controls how its messages look outbound (signature, display name) and which messages it receives (all, mentions, trusted).
- [ ] Chats always show a human title (group name, contact name) and routed rooms are named after them.

## Out of Scope

| Feature | Reason |
| --- | --- |
| Agents starting conversations with arbitrary phone numbers or users | Outreach without an inbound chat is a spam and abuse vector; catch-all routing covers replies |
| Typing indicators and read receipts | Platform-specific, little value for asynchronous agents |
| Per-chat quiet hours and schedules | Can be built on rooms later; not requested |
| Editing or deleting already sent outbound messages | Needs per-platform edit APIs; deferred |

---

## Assumptions & Open Questions

| Assumption / decision | Chosen default | Rationale | Confirmed? |
| --- | --- | --- | --- |
| Catch-all target | One room per connector (`auto_route_room_id`), chosen by the connector manager | Simple, visible, shareable; the room already defines agents and access | y |
| Catch-all scope options | `off`, `dms`, `groups`, `all`; default `off` | Opt-in keeps the current privacy default | y |
| Catch-all trust | Auto-routed senders stay `trusted=false` unless verified | AD-006 unchanged; anyone can now reach the agent | y |
| Signature default | On for existing and new agent memberships | Keeps current behavior; users opt out per room | y |
| Delivery modes | `all` (default), `mentions`, `trusted` | Covers busy groups and owner-only bots | y |
| What counts as a mention | `@<display name>` or `@<agent name>` (case-insensitive), a reply to one of the agent's messages, any configured keyword, or any message in a DM chat | Matches how people address bots in groups | y |
| Room instructions | Free text per room, up to 4,000 characters, given to agents through the `room_info` tool and the welcome notice | Lets each group carry its own context (client, tone, language) | y |
| Reactions | Agents can react with one emoji to a chat message; WhatsApp, Telegram and Discord adapters send it natively, webhook posts it as an event | Cheap acknowledgement without noise | y |

**Open questions:** none - all resolved or logged above.

---

## User Stories

### P1: Agents know where messages come from ⭐ MVP

**User Story**: As an agent session, I want each message to say its chat, platform and conversation kind so that I answer with the right context.

**Why P1**: Reported as missing in first use.

**Acceptance Criteria**:

1. WHEN a chat-authored message is delivered to an agent THEN the channel SHALL add meta keys `chat_id` (the chat member id usable as a recipient), `chat_title`, `chat_kind` (`group`, `dm` or `channel`), `platform` (`whatsapp`, `telegram`, `discord` or `webhook`) and `sender_id`. (CTX-01)
2. WHEN a message authored by an agent or user is delivered THEN the channel SHALL omit `chat_*` and `platform` keys. (CTX-02)
3. WHEN `history` returns chat-authored messages THEN each item SHALL carry the same chat fields as the push. (CTX-03)
4. The channel instructions SHALL explain the chat fields and that `send_message` accepts `chat_ids` to answer only one chat in a room with several chats. (CTX-04)

**Independent Test**: Room with one WhatsApp group and one Telegram DM; messages from each reach the agent with the matching chat meta.

---

### P1: Chat titles and room names ⭐ MVP

**User Story**: As a user, I want chats and routed rooms to show real names so that I recognise them.

**Why P1**: Rooms were named `lollerie|1203...@g.us` in first use.

**Acceptance Criteria**:

1. WHEN the WhatsApp connector sees a group without a known title THEN it SHALL fetch the group subject and store it as the chat title. (CTX-05)
2. WHEN a WhatsApp DM arrives THEN the chat title SHALL be the sender's push name, or the phone number when the push name is empty. (CTX-06)
3. WHEN a chat is routed to an agent THEN the new room SHALL be named after the chat title, falling back to `<platform> chat` only when no title is known. (CTX-07)
4. WHEN a chat title changes on the platform THEN the next refresh or inbound message SHALL update the stored title. (CTX-08)

**Independent Test**: Fake evolution-go with a group subject; the chat list and routed room show it.

---

### P1: Catch-all routing ⭐ MVP

**User Story**: As a connector manager, I want new chats routed automatically to a room so that an agent can answer anyone who writes.

**Why P1**: Requested ("responder qualquer mensagem de qualquer pessoa").

**Acceptance Criteria**:

1. WHERE a connector has `auto_route_scope` other than `off` and an `auto_route_room_id` THEN the system SHALL add a chat that belongs to no room and matches the scope to that room as a member with `outbound` on, then store and deliver its message there. (ROUTE-01)
2. IF the chat already belongs to any room THEN the system SHALL NOT add it to the catch-all room. (ROUTE-02)
3. IF the catch-all room no longer exists or the connector owner lost `manage` on it THEN the system SHALL stop auto-routing and set the connector status detail to `auto-route room unavailable`. (ROUTE-03)
4. WHEN a user sets `auto_route_room_id` THEN the system SHALL require `manage` on that room, else respond 403; an unknown room id SHALL get the same answer (status, code and message) as a room the user cannot see. (ROUTE-04)
5. The system SHALL deliver auto-routed messages with `trusted=false` unless the sender is a verified identity of the agent's owner. (ROUTE-05)
6. WHILE a room's `broadcast` setting is off (default on) the system SHALL store a user or agent post without recipients, deliver it to the room's agents and queue it to no chat, and the post response (and channel tool result) SHALL say `not sent to any chat: this room only answers explicit chats (use reply or chat_ids)`; a reply to a message of a chat with outbound on SHALL go to that chat only. (ROUTE-06)
7. WHEN a room becomes a connector's `auto_route_room_id` THEN the system SHALL turn its `broadcast` and `bridge_chats` off, and WHILE it is any connector's catch-all room a PATCH turning either on SHALL get 422 on that field (`must stay off while the room is a catch-all room`) and apply nothing. (ROUTE-07)
8. IF a connector already auto-added 60 chats in the last hour, or its catch-all room already has 1,000 chats, THEN the system SHALL NOT add a new chat nor store its message and SHALL set the connector status detail to `auto-route limit reached`; the next successful route SHALL clear it. (ROUTE-08)
9. WHEN a chat is routed to the catch-all room again, or a manager saves the connector's catch-all settings, THEN the system SHALL clear an `auto-route room unavailable` or `auto-route limit reached` status detail and leave other details unchanged. (ROUTE-09)

**Independent Test**: Connector with scope `dms` and a room with an agent; a new DM reaches the agent, a new group does not.

---

### P1: Agent membership settings ⭐ MVP

**User Story**: As a room manager, I want per-room settings for each agent so that I control how it signs messages and what it receives.

**Why P1**: Requested ("desligar aquele nome que fica").

**Acceptance Criteria**:

1. WHEN an agent member has `sign_outbound` off THEN chat adapters SHALL send its messages without the author header. (MEM-01)
2. WHERE an agent member has a `display_name` THEN outbound headers and the room timeline SHALL show it instead of the agent name. (MEM-02)
3. WHILE an agent member's `delivery` is `mentions` the system SHALL deliver only messages that mention it as defined in Assumptions, and SHALL still store every message in the room. (MEM-03)
4. WHILE an agent member's `delivery` is `trusted` the system SHALL deliver only messages with `trusted=true` for that agent. (MEM-04)
5. IF `display_name` exceeds 64 characters or a keyword list exceeds 20 entries of 64 characters THEN the system SHALL respond 422. (MEM-05)

**Independent Test**: Agent with signature off and delivery `mentions` in a busy group: outbound has no header; only `@name` messages arrive.

---

### P2: Room instructions

**User Story**: As a room manager, I want to write instructions for agents in a room so that each group gets its own context.

**Why P2**: Makes one agent serve several groups well.

**Acceptance Criteria**:

1. WHEN a manager saves room instructions up to 4,000 characters THEN the system SHALL store them; longer text SHALL get 422. (INS-01)
2. WHEN an agent calls `room_info` THEN the tool SHALL return the room name, instructions, members (kind, name, chat platform and kind) and the agent's own membership settings. (INS-02)
3. WHEN an agent connects and the room has instructions THEN the welcome backlog SHALL include one notice per room, before that room's messages and also when the room is paused, with the text `This room has instructions for its agents; call the room_info tool to read them.` (INS-03)

**Independent Test**: Set instructions, call `room_info` through the channel, see them.

---

### P2: Reactions

**User Story**: As an agent, I want to react to a message with an emoji so that I can acknowledge without writing.

**Why P2**: Low-noise acknowledgement.

**Acceptance Criteria**:

1. WHEN an agent calls `react` with a message id and one emoji THEN the system SHALL send the reaction to the message's origin chat through its connector. (REACT-01)
2. IF the target message did not come from a chat THEN `react` SHALL return `isError: true` with `can only react to chat messages`. (REACT-02)
3. IF the target message's chat is listen-only in the room (`outbound` off) THEN `react` SHALL respond 422 with field `recipients` = `listen-only`, as for messages, and store nothing. (REACT-03)
4. WHEN the reacting agent member has `sign_outbound` off THEN the webhook reaction payload SHALL leave out `message.author_name`. (REACT-04)

**Independent Test**: Fake connectors receive the reaction with the original external id.

---

### P1: Delete a room from the web app ⭐ MVP

**User Story**: As a room manager, I want a delete action in the room settings so that I can remove rooms I no longer need.

**Why P1**: Reported missing in first use; the API already supports it.

**Acceptance Criteria**:

1. WHERE the user has `manage` on the room the app SHALL show a "Delete room" action in the room settings, confirmed in an alert dialog that states messages, attachments and pending deliveries are removed. (ROOMDEL-01)
2. WHEN the deletion succeeds THEN the app SHALL navigate to `/rooms`, show a confirmation toast and drop the room from the list without reload. (ROOMDEL-02)

**Independent Test**: Manager deletes a room; it disappears from the list; a viewer sees no delete action.

---

## Edge Cases

- IF two first messages from the same new chat arrive at once THEN the system SHALL add the chat to the catch-all room only once (ROUTE-01).
- WHEN a catch-all chat is later removed from the room THEN it SHALL NOT be re-added automatically for 24 hours unless a manager adds it (ROUTE-02).
- WHEN a room with `bridge_chats` on is chosen as a catch-all room THEN bridging SHALL be turned off with broadcast (ROUTE-07).
- WHEN no connector targets a room any more THEN its `broadcast` SHALL stay off until a manager turns it on (ROUTE-07).
- WHEN a mention keyword matches inside a longer word THEN it SHALL still count only on word boundaries (MEM-03).

---

## Requirement Traceability

| Requirement ID | Story | Phase | Status |
| --- | --- | --- | --- |
| CTX-01 | P1: Agents know where | Tasks | Implementing |
| CTX-02 | P1: Agents know where | Tasks | Implementing |
| CTX-03 | P1: Agents know where | Tasks | Implementing |
| CTX-04 | P1: Agents know where | Tasks | Implementing |
| CTX-05 | P1: Chat titles | Tasks | Implementing |
| CTX-06 | P1: Chat titles | Tasks | Implementing |
| CTX-07 | P1: Chat titles | Tasks | Implementing |
| CTX-08 | P1: Chat titles | Tasks | Implementing |
| ROUTE-01 | P1: Catch-all | Tasks | Implementing |
| ROUTE-02 | P1: Catch-all | Tasks | Implementing |
| ROUTE-03 | P1: Catch-all | Tasks | Implementing |
| ROUTE-04 | P1: Catch-all | Tasks | Amended (T27), re-verify |
| ROUTE-05 | P1: Catch-all | Tasks | Implementing |
| ROUTE-06 | P1: Catch-all | Fix iteration 1 | Implemented (T23, T24, T31) |
| ROUTE-07 | P1: Catch-all | Fix iteration 1 | Implemented (T23, T31) |
| ROUTE-08 | P1: Catch-all | Fix iteration 1 | Implemented (T25) |
| ROUTE-09 | P1: Catch-all | Fix iteration 1 | Implemented (T26) |
| MEM-01 | P1: Membership settings | Tasks | Implementing |
| MEM-02 | P1: Membership settings | Tasks | Implementing |
| MEM-03 | P1: Membership settings | Tasks | Fixed (T18), re-verify |
| MEM-04 | P1: Membership settings | Tasks | Implementing |
| MEM-05 | P1: Membership settings | Tasks | Implementing |
| INS-01 | P2: Room instructions | Tasks | Implementing |
| INS-02 | P2: Room instructions | Tasks | Implementing |
| INS-03 | P2: Room instructions | Tasks | Fixed (T19), re-verify |
| REACT-01 | P2: Reactions | Tasks | Implementing |
| REACT-02 | P2: Reactions | Tasks | Implementing |
| REACT-03 | P2: Reactions | Fix iteration 1 | Implemented (T20) |
| REACT-04 | P2: Reactions | Fix iteration 1 | Implemented (T21) |
| ROOMDEL-01 | P1: Delete a room | Tasks | Implementing |
| ROOMDEL-02 | P1: Delete a room | Tasks | Implementing |

**Coverage:** 31 total, mapped in tasks.md

---

## Success Criteria

- [ ] In the lollerie group, the agent's message arrives without a header and the agent's context names the group.
- [ ] A DM from an unknown number reaches the catch-all agent within 2 seconds of arrival.
