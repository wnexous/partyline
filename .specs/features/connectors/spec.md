# Connectors Specification

## Problem Statement

Rooms need external chats as members: WhatsApp groups (through an evolution-go server), Telegram chats, Discord channels and anything else that can speak HTTP. Each platform has its own transport, ids and media handling. Connectors normalize all of them into chats and messages, and send room messages back out, without leaking content between users.

## Goals

- [ ] A user adds a Telegram bot, sees its chats and routes one to an agent in under 3 minutes.
- [ ] Adding a new platform touches only one package plus its registration line (AD-007).
- [ ] No inbound message from a chat outside every room is stored (only the chat's id, title and last-seen time).

## Out of Scope

| Feature | Reason |
| --- | --- |
| Slack, Matrix, Signal, e-mail native connectors | Webhook connector covers custom bridges; add natively later |
| Direct WhatsApp (whatsmeow) without evolution-go | User already runs evolution-go; one WhatsApp path in v1 |
| Voice transcription | Agents can download audio and transcribe themselves |
| Outbound reactions, edits, deletes, polls | Text and files only in v1 |

---

## Assumptions & Open Questions

| Assumption / decision | Chosen default | Rationale | Confirmed? |
| --- | --- | --- | --- |
| evolution-go transport | Broadcast WebSocket `/ws?token=<global key>` (no `instanceId`); REST with instance tokens fetched from `/instance/all` | Several broadcast clients coexist; proven in zap-router | y |
| evolution-go instances | Config lists allowed instance names; empty list = every instance with websocket enabled | One evolution server can host several numbers | n |
| Telegram transport | Bot API long polling (`getUpdates`, 30 s timeout) | Works behind NAT, no public URL needed | n |
| Discord transport | Gateway via `github.com/bwmarrin/discordgo` with Message Content intent | Standard; raw gateway is large | n |
| Webhook auth | HMAC-SHA256 of the raw body with the connector secret, header `X-Partyline-Signature: sha256=<hex>`, timestamp header `X-Partyline-Timestamp` within 5 minutes | Standard webhook signing, replay protection | n |
| Identity verification | Pairing code: user gets a 6-character code in the web app, sends `!link CODE` or `/link CODE` from the account; valid 10 minutes | Proves control of the account; no manual claims | n |
| Media size | Inbound media larger than the upload limit is stored as an attachment record with status `too_large` and no file | Bounded disk use | n |
| Connector reconnect | Backoff 2 s doubling to 120 s; status `connecting`, `connected`, `error` with last error text | Same as zap-router | n |

**Open questions:** none - all resolved or logged above.

---

## User Stories

### P1: Manage connectors ⭐ MVP

**User Story**: As a user, I want to add, configure, enable and remove connectors so that my bots and numbers feed my rooms.

**Why P1**: Entry point for every platform.

**Acceptance Criteria**:

1. WHEN `GET /api/v1/connector-kinds` is called THEN the system SHALL return each kind with its config field schema (name, label, type, required, secret, help). (CON-01)
2. IF a connector config misses a required field or a field fails its type THEN the system SHALL respond 422 naming the field. (CON-02)
3. The system SHALL never return a secret field's value through the API; reads SHALL return `"********"` when set. (CON-03)
4. WHEN a connector is created or enabled THEN the system SHALL start it within 2 seconds and report status changes as `connector.status` events. (CON-04)
5. WHEN a connector is disabled or deleted THEN the system SHALL stop it and stop all its outbound sends. (CON-05)
6. IF a connector's transport fails THEN the system SHALL set status `error` with the error text (secrets redacted) and retry with backoff. (CON-06)

**Independent Test**: Create a webhook connector, read it back with the secret masked, disable it, observe status events.

---

### P1: Chats and inbound routing ⭐ MVP

**User Story**: As a user, I want my connectors' chats listed so that I can add them to rooms; messages from chats in rooms must arrive there.

**Why P1**: Chats are room members.

**Acceptance Criteria**:

1. WHEN a connector receives a message from an unknown chat THEN the system SHALL create the chat record (connector, external id, title, kind) and SHALL NOT store the message. (CON-07)
2. WHEN a connector receives a message from a chat that belongs to rooms THEN the system SHALL store it once per room with author kind `chat`, sender id, sender name and external message id. (CON-08)
3. WHERE the connector kind supports listing chats the system SHALL refresh the chat list on demand via `POST /api/v1/connectors/{id}/chats/refresh`. (CON-09)
4. IF the same external message id arrives twice for a chat THEN the system SHALL store it only once. (CON-10)
5. WHEN inbound media fits the upload limit THEN the system SHALL store it as an attachment of the message. (CON-11)
6. The system SHALL drop messages sent by the connector's own account. (CON-12)

**Independent Test**: Fake platform server emits messages for a routed and an unrouted chat; only the routed one is stored.

---

### P1: WhatsApp via evolution-go ⭐ MVP

**User Story**: As a user with an evolution-go server, I want WhatsApp groups and DMs as chats so that clients' groups reach my agents.

**Why P1**: The original use case.

**Acceptance Criteria**:

1. WHEN connected THEN the connector SHALL subscribe to the broadcast WebSocket and handle `Message` events of allowed instances. (WA-01)
2. The connector SHALL map a group chat to external id = group JID and a DM to the peer's JID with the `:device` suffix removed. (WA-02)
3. WHEN sending text THEN the connector SHALL call `POST /send/text` with the instance token, and include `quoted` when replying to a message from that chat. (WA-03)
4. WHEN sending a file THEN the connector SHALL call `POST /send/media` with base64 content and the right type (image, video, audio, document). (WA-04)
5. WHEN chats are refreshed THEN the connector SHALL list groups via `GET /group/list` for each allowed instance. (WA-05)

**Independent Test**: Fake evolution-go (WebSocket + REST) in tests.

---

### P1: Telegram ⭐ MVP

**User Story**: As a user, I want a Telegram bot as a connector so that chats with the bot become room members.

**Why P1**: Explicitly requested.

**Acceptance Criteria**:

1. WHEN connected THEN the connector SHALL long-poll `getUpdates` with the last offset + 1 and handle `message` and `channel_post`. (TG-01)
2. WHEN sending text THEN the connector SHALL call `sendMessage`, splitting text above 4,096 characters into ordered parts, with `reply_parameters` when replying. (TG-02)
3. WHEN sending a file THEN the connector SHALL use `sendPhoto` for images and `sendDocument` otherwise. (TG-03)
4. WHEN inbound media is present THEN the connector SHALL fetch it via `getFile`. (TG-04)

**Independent Test**: httptest fake Bot API.

---

### P1: Discord ⭐ MVP

**User Story**: As a user, I want a Discord bot as a connector so that Discord channels become room members.

**Why P1**: Explicitly requested.

**Acceptance Criteria**:

1. WHEN a guild or DM message is created by a non-bot user THEN the connector SHALL emit it with external id = channel id. (DC-01)
2. WHEN sending text THEN the connector SHALL split above 2,000 characters and set the message reference when replying. (DC-02)
3. WHEN chats are refreshed THEN the connector SHALL list text channels of every guild the bot is in. (DC-03)

**Independent Test**: Adapter tested through an interface fake of the discordgo session.

---

### P1: Generic webhook ⭐ MVP

**User Story**: As a developer, I want to push and receive messages over signed HTTP so that any system (CI, Slack bridge, alerts) joins rooms.

**Why P1**: "endpoint api" for everything else.

**Acceptance Criteria**:

1. WHEN `POST /api/v1/hooks/{connector_id}` has a valid signature and timestamp THEN the system SHALL accept `{chat_id, chat_title?, sender_id, sender_name?, text, message_id?}` and respond 202. (HOOK-01)
2. IF the signature is invalid or the timestamp is older than 5 minutes THEN the system SHALL respond 401 and store nothing. (HOOK-02)
3. WHEN a room message is queued for a webhook chat THEN the connector SHALL POST it as JSON to the configured outbound URL with the same signature headers, and treat any non-2xx as a failure. (HOOK-03)

**Independent Test**: Sign a payload in the test, post it, receive the outbound call on an httptest server.

---

### P1: Verified identities ⭐ MVP

**User Story**: As a user, I want to prove which WhatsApp/Telegram/Discord accounts are mine so that my own messages reach my agents as trusted.

**Why P1**: Required by the trust model (AD-006).

**Acceptance Criteria**:

1. WHEN a user requests a pairing code THEN the system SHALL return a 6-character code (A-Z, 2-9) valid for 10 minutes. (IDN-01)
2. WHEN a connector receives `!link CODE` or `/link CODE` with a valid code THEN the system SHALL link (connector kind, sender id and alternate sender id) to that user, consume the code and not route the message. (IDN-02)
3. IF the code is unknown or expired THEN the system SHALL ignore the command and not route the message. (IDN-03)
4. WHEN a user deletes an identity THEN the system SHALL resolve messages received afterwards from that sender to no user, so they are delivered with `trusted=false`. (IDN-04)

**Independent Test**: Request code, inject a `!link` message through the fake connector, list identities.

---

## Edge Cases

- IF evolution-go returns 401 for the global key THEN the connector SHALL set status `error: unauthorized` and keep retrying with backoff (CON-06).
- IF a Telegram chat migrates to a supergroup (`migrate_to_chat_id`) THEN the connector SHALL update the chat's external id (TG-01).
- IF an outbound target chat no longer exists on the platform THEN the send SHALL fail and follow the retry rules (CON-06).

---

## Requirement Traceability

| Requirement ID | Story | Phase | Status |
| --- | --- | --- | --- |
| CON-01 | P1: Manage connectors | Tasks | Pending |
| CON-02 | P1: Manage connectors | Tasks | Pending |
| CON-03 | P1: Manage connectors | Tasks | Pending |
| CON-04 | P1: Manage connectors | Tasks | Pending |
| CON-05 | P1: Manage connectors | Tasks | Pending |
| CON-06 | P1: Manage connectors | Tasks | Pending |
| CON-07 | P1: Chats and inbound | Tasks | Pending |
| CON-08 | P1: Chats and inbound | Tasks | Pending |
| CON-09 | P1: Chats and inbound | Tasks | Pending |
| CON-10 | P1: Chats and inbound | Tasks | Pending |
| CON-11 | P1: Chats and inbound | Tasks | Pending |
| CON-12 | P1: Chats and inbound | Tasks | Pending |
| WA-01 | P1: WhatsApp | Tasks | Pending |
| WA-02 | P1: WhatsApp | Tasks | Pending |
| WA-03 | P1: WhatsApp | Tasks | Pending |
| WA-04 | P1: WhatsApp | Tasks | Pending |
| WA-05 | P1: WhatsApp | Tasks | Pending |
| TG-01 | P1: Telegram | Tasks | Pending |
| TG-02 | P1: Telegram | Tasks | Pending |
| TG-03 | P1: Telegram | Tasks | Pending |
| TG-04 | P1: Telegram | Tasks | Pending |
| DC-01 | P1: Discord | Tasks | Pending |
| DC-02 | P1: Discord | Tasks | Pending |
| DC-03 | P1: Discord | Tasks | Pending |
| HOOK-01 | P1: Webhook | Tasks | Pending |
| HOOK-02 | P1: Webhook | Tasks | Pending |
| HOOK-03 | P1: Webhook | Tasks | Pending |
| IDN-01 | P1: Identities | Tasks | Pending |
| IDN-02 | P1: Identities | Tasks | Pending |
| IDN-03 | P1: Identities | Tasks | Pending |
| IDN-04 | P1: Identities | Tasks | Pending |

**Coverage:** 31 total, mapped in tasks.md

---

## Success Criteria

- [ ] Each connector kind passes its fake-platform test suite.
- [ ] Manual UAT on the real evolution-go `lollerie` instance: a routed group message reaches an agent (no reply sent without André's request).
