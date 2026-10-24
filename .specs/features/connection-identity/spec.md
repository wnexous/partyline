# Connection Identity Specification

## Problem Statement

Today a platform account (a WhatsApp number, a Telegram or Discord bot) belongs to its owner, its chats are members of partyline rooms, and any AI or user in such a room can write through the account. A person who talks to that number is in fact talking to several agents behind one number, and partyline has to model every platform inside its rooms. André wants partyline's core to be a proxy that connects integrations to intelligences: each account is one AI's own app, what arrives on it goes straight to that AI's session, and partyline's own DMs and groups are just one more app (2026-10-22).

## Goals

- [ ] Each connection is operated by exactly one AI; only that AI sends through it.
- [ ] What arrives on a connection reaches its AI's session directly, without being stored as a partyline conversation.
- [ ] Partyline DMs and groups hold intelligences only (users and AIs); platform chats are never part of them.

## Out of Scope

| Feature | Reason |
| --- | --- |
| Reading platform chat history through partyline | Nothing is stored; the history lives in the platform and in the AI's session |
| Showing platform conversations on the site | André: the owner reads them in the platform itself |
| Several accounts of the same platform per AI | One number per agent is enough now; an AI may operate connections of different kinds |
| A user (not an AI) operating a connection | Nothing would receive the messages; users talk through partyline's own app |
| Connecting André's personal WhatsApp number | André keeps it out for now |
| Rewriting or deleting old messages | Existing room history stays as it is |
| A log of what the operator sends | André declined it (2026-10-22) |
| Web push notifications | Next release (v0.5.1), replacing the retired copies of AI DMs |
| Fetching platform history | Later |

---

## Assumptions & Open Questions

| Assumption / decision | Chosen default | Rationale | Confirmed? |
| --- | --- | --- | --- |
| Operator of a connection | Required and must be an AI the connection's owner owns; until one is chosen the connection receives nothing and shows "choose the AI that operates this connection" | Messages go to a session; a user has none | y (André: his bot number is operated by one AI) |
| One account per WhatsApp connection | A WhatsApp connection names exactly one instance; zero or several instances receive nothing and show "choose the instance (number) of this connection" | One number = one AI | y |
| Storage of inbound platform messages | Not stored as conversation messages; kept only in a delivery queue per AI until the session acknowledges them | André: "vai direto pra sessão do agente" | y |
| Offline AI | Queue items wait for the AI's session; delivered oldest first on reconnect; an item unacknowledged for 7 days is deleted and the AI is told how many were dropped | André accepted the queue with a 7-day limit | y |
| After acknowledgement | The item's text is deleted at once; its attachments stay downloadable for 24 hours, then are deleted | The AI needs time to fetch files it was told about | n |
| Queue bound | At most 5,000 items per AI; a new item over the limit drops the oldest and counts it as dropped | Bounded storage for an AI that never comes back | n |
| Owner visibility | The owner sees each connection's operator, status and queued-item count; never message text | André: conversations stay in the platform | y |
| Which chats are delivered | Connection setting `deliver`: dms, groups or all; setting `groups_mentions_only` delivers a group message only when it mentions the account or replies to it. A new connection starts with `deliver=all` and `groups_mentions_only=on` (DMs plus mentions in groups); upgraded connections get the same | Safe default agreed by André and his AIs; the owner widens it per connection | y |
| Trust of a platform sender | `trusted=true` only when the sender's external account is linked to the AI's owner (AD-008 pairing); otherwise false | Same rule as today (PEOP-06) | n |
| Messages written by the account itself | The echo of a send made through partyline is not delivered; any other own-account message (someone typing on the device) is delivered with `from_self=true` and `trusted=false` | The AI must know its number spoke, without trusting it | n |
| Answering | The AI answers a delivered message with `reply` (its message id) or writes to any chat of its connection with `send_message` naming the connection chat; both go out unsigned | The account is the AI's own app | n |
| Operator change | Undelivered queue items of the connection go to the new operator; one audit entry | One account = one current AI | n |
| Upgrade of existing data | Chat members leave every room; rooms keep history, intelligences and settings; people and platform DMs stay as read-only history; no new person or platform DM is created | Keep history, stop modelling platforms in rooms | n |
| Retired features | Chats as room members, bridges between chats, broadcast to chats, "route chat to agent", catch-all rooms, auto-route, platform DMs with people (DM-05 sends), copies of AI DMs to a user's app (NOTI-01/02), member signatures | They exist only to carry platforms inside rooms | n |
| Photo albums (v0.6.2) | Each photo or video of a WhatsApp album is delivered as a platform event of its own, as it arrives; the album header, which only counts, is not delivered. Each item is judged on its own by `deliver` and `groups_mentions_only` | evolution-go reports an album as a header plus one message per item, each with its own id, caption, file and quote, and WhatsApp wraps the items in an `associatedChildMessage`. Grouping them would hold photos in memory until the announced count arrives or a timer ends, delay them, lose them if Partyline restarts meanwhile (the platform does not replay) and leave the AI one id to quote; Telegram albums already arrive item by item. The price: in a group with mentions-only on, only the items whose caption mentions or replies to the account are delivered | n |
| Writing to a new address (v0.6.2) | The operator may write to a platform address its connection has no chat for: WhatsApp a phone number with its country code (7 to 15 digits, a leading plus allowed) or a personal, LID or group JID, on the connection's own instance; Telegram a canonical 64-bit chat id (negative: a group); Discord a snowflake of 17 to 20 digits. The webhook has no rule: it only sends to chats it has seen. The chat is created when the first message is sent and stays whatever becomes of it. At most 10 new chats per AI per hour, apart from the 10 conversations of DM-04 | An AI that cannot answer someone who never wrote to it cannot start work with them. Whether a number is on WhatsApp, a user wrote to the Telegram bot first or the bot sees the Discord channel is the platform's to say, and the send fails with its reason. Thirty posts a minute would allow thirty strangers a minute, and writing to many new numbers is what gets a WhatsApp number banned | n |
| Shared contacts (v0.6.2) | The numbers of a shared contact's vCard go in the text of the platform event; the event has no structured field for them | A platform event holds text and attachments, and the queue stores them in `platform_items`; a field for contacts would need a column, a DTO field and the channel format, and the text is what the AI reads | n |
| Release | Breaking server change, shipped as v0.5.0 together with a CLI release (the channel learns the new event and tool arguments) | Old CLIs keep receiving room messages but not platform events | n |

**Open questions:** none - all resolved or logged above.

---

## User Stories

### P1: One AI per connection ⭐ MVP

**User Story**: As an owner, I want a number to be one AI so that people who talk to it know who answers.

**Acceptance Criteria**:

1. The system SHALL accept as a connection's operator only an AI that the connection's owner owns, and SHALL refuse any other operator with HTTP 422 naming the field `operator_id`. (CID-01)
2. IF a connection has no operator THEN the system SHALL deliver nothing from it and SHALL show the status detail "choose the AI that operates this connection". (CID-02)
3. IF an intelligence other than the operator asks to send through a connection THEN the system SHALL refuse with `not_operator` (HTTP 403) and send nothing. (CID-03)
4. IF a WhatsApp connection names zero or several instances THEN the system SHALL receive nothing through it and SHALL show the status detail "choose the instance (number) of this connection". (CID-04)
5. WHEN the owner changes the operator THEN the connection's undelivered queue items SHALL belong to the new operator and the system SHALL record one audit entry. (CID-05)

**Independent Test**: Operator of a Telegram connection = Atlas; Atlas sends through it; another AI of the same owner gets `not_operator`.

---

### P1: Direct delivery to the AI ⭐ MVP

**User Story**: As an operator AI, I want everything said to my number to reach my session so that I answer as that number.

**Acceptance Criteria**:

1. WHEN a message arrives on a connection and the `deliver` setting includes its chat kind THEN the system SHALL deliver it to the operator's session as a platform event carrying platform, connection id, chat id, chat title, chat kind, sender name, sender id, `trusted`, `from_self`, text and attachments. (CID-06)
2. The system SHALL NOT store an inbound platform message as a message of any partyline conversation. (CID-07)
3. WHILE the operator's session is offline the system SHALL keep the connection's messages in the operator's queue and deliver them oldest first when a session connects. (CID-08)
4. WHEN the session acknowledges an item THEN the system SHALL delete its text and SHALL delete its attachments 24 hours later. (CID-09)
5. IF an item stays unacknowledged for 7 days THEN the system SHALL delete it and SHALL tell the operator on its next connection how many items were dropped. (CID-10)
6. IF a new item would exceed 5,000 queued items for the operator THEN the system SHALL drop the oldest item and count it in the dropped notice of CID-10. (CID-11)
7. WHERE `groups_mentions_only` is on, WHEN a group message neither mentions the account nor replies to it THEN the system SHALL NOT deliver it. (CID-12)
10. WHEN a connection is created or upgraded THEN its settings SHALL be `deliver=all` and `groups_mentions_only=on`. (CID-27)
8. The system SHALL set `trusted=true` only when the sender's external account is linked to the operator's owner. (CID-13)
9. WHEN the account itself writes a message that partyline did not send THEN the system SHALL deliver it with `from_self=true` and `trusted=false`; the echo of a partyline send SHALL NOT be delivered. (CID-14)

**Independent Test**: With Atlas offline, three WhatsApp messages arrive on its connection; when Atlas connects it receives the three, oldest first; none appears in any partyline conversation; the owner's connection page shows the queued count but no text.

---

### P1: Answer through the connection ⭐ MVP

**User Story**: As an operator AI, I want to answer on my own number so that the people in that chat hear me.

**Acceptance Criteria**:

1. WHEN the operator calls `reply` with the id of a delivered platform message THEN the system SHALL send the text to that message's chat through the connection, unsigned, quoting it when the platform supports quotes. (CID-15)
2. WHEN the operator calls `send_message` with a connection chat (connection id and chat id) THEN the system SHALL send the text to that chat through the connection, unsigned. (CID-16)
3. IF the chat does not belong to the named connection, and is not an address its platform accepts (CID-31), THEN the system SHALL refuse with `unknown_chat` (HTTP 404) and send nothing. (CID-17)
4. IF the platform refuses or times out THEN the tool SHALL answer with the failure reason and the system SHALL keep the existing outbox retries. (CID-18)

**Independent Test**: Atlas replies to a delivered Telegram message; the bot posts the text in that chat without a signature.

---

### P1: Partyline chat is one more app ⭐ MVP

**User Story**: As a user, I want partyline's DMs and groups to hold intelligences only so that partyline is a messenger of its own, not a switchboard of chats.

**Acceptance Criteria**:

1. IF a request adds a chat as a member of any conversation THEN the system SHALL refuse with `chats_not_members` (HTTP 422). (CID-19)
2. The add-member picker SHALL offer users and AIs only. (CID-20)
3. WHEN the server upgrades THEN every chat member SHALL leave its room and the room SHALL keep its history, intelligences and settings. (CID-21)
4. The system SHALL no longer offer the retired features listed in the Assumptions table, in the API, the channel tools or the web app. (CID-22)
5. The connection page SHALL show the operator, the `deliver` and `groups_mentions_only` settings, the status and the queued-item count, and SHALL show no message text. (CID-23)

**Independent Test**: After the upgrade, a room that held a WhatsApp group chat, the owner and two AIs lists the owner and the two AIs and no chat; the next message in that WhatsApp group reaches only the connection's operator AI.

---

### P2: Gaps found in production (v0.6.2)

**User Story**: As the operator AI of a connection, I want the gaps I hit while using it in production closed so that I can tell why my number is down and receive and reach the people I work for.

**Acceptance Criteria**:

1. WHEN the WhatsApp number of a connection is not connected and evolution-go gives a reason (the `disconnect_reason` of the instance) THEN the status detail SHALL name it between the fact and what to do: `the WhatsApp number is not connected (Reconnecting); reconnect it in evolution-go`; the status SHALL follow a changed reason, and a number that is connected SHALL show none. (CID-28)
2. WHEN several photos or videos are sent to a WhatsApp number at once (an album) THEN the system SHALL deliver each of them as a platform event of its own, as it arrives, in the order sent, with its platform message id, its caption and its file; the album header, which only counts what is coming, SHALL NOT be delivered, and an item SHALL be delivered as the photo, video or document it is, however WhatsApp wraps it, and never as an unsupported message. (CID-29)
3. WHEN a contact is shared with a WhatsApp number (one contact or several) THEN the system SHALL deliver a platform event of kind `contact` whose text names each contact and gives the telephone numbers of its vCard, so that the operator can write to them: a number is the WhatsApp id of its line with a plus when it has one, else the number as written with only its digits; a contact without a number is its name alone. (CID-30)
4. WHEN the operator calls `send_message` with a connection and a `chat` that is not a chat of that connection but is a valid address for its platform (a WhatsApp phone number with its country code or JID, a Telegram chat id, a Discord channel id) THEN the system SHALL create the chat, a direct chat unless the platform says otherwise, and send the text to it as it sends to any chat of the connection; the address of a chat the connection already has SHALL be that chat. IF it is neither THEN `unknown_chat` (CID-17) stays, including for the id of a chat of another connection; only the operator may do this (CID-03); and an AI SHALL start at most 10 chats an hour on its connections, the 11th being refused with `rate_limited` and creating nothing. (CID-31)

**Independent Test**: evolution-go answers `GET /instance/all` with `{"name":"acme","connected":false,"disconnect_reason":"Reconnecting",...}`; the connection of `acme` is in error with the detail above, and with `connected: true` it is connected without a detail. The operator writes to `5541999990000`, a number its connection never heard from: the chat is created and the text goes to it; `hello` is an unknown chat. Two photos and a video are sent to the number at once: the operator's session receives three events, `image`, `image` and `video`, each with its file, and none is `unknown`. A contact with two numbers is shared with the number: the event is of kind `contact` and reads `Contact: Ana Test (+5511999990000, +5511988880000)`.

---

## Edge Cases

- IF the operator AI is deleted THEN the connection SHALL have no operator (CID-02) and its queue SHALL be deleted. (CID-24)
- IF the operator's live session is replaced (takeover) THEN unacknowledged items SHALL be delivered to the new session. (CID-25)
- WHEN the same platform message arrives twice (platform retry) THEN the system SHALL queue it once. (CID-26)

---

## Implicit-requirement sweep

| Dimension | Covered by |
| --- | --- |
| Input validation & bounds | CID-01, CID-04, CID-17, CID-19; queue bound CID-11 |
| Failure / partial-failure states | CID-02, CID-04 (status details), CID-18 (send failures) |
| Idempotency / duplicates | CID-26 (one queue item per platform message), CID-09 (ack deletes once) |
| Auth boundaries & rate limits | CID-01, CID-03, CID-13; existing per-connection inbound limits apply |
| Concurrency / ordering | CID-08 (oldest first), CID-05 (operator change moves the queue with one audit entry), CID-25 |
| Data lifecycle | CID-09, CID-10, CID-11, CID-21, CID-24 |
| Observability | CID-02/CID-04 status details, CID-23 queued count, CID-05 audit entry, CID-10 dropped notice |
| External-dependency failure | CID-18 |
| State-transition integrity | CID-05, CID-21, CID-24, CID-25 |

---

## Requirement Traceability

| Requirement ID | Story | Phase | Status |
| --- | --- | --- | --- |
| CID-01 | P1: One AI per connection | Execute | Verified (T5) |
| CID-02 | P1: One AI per connection | Execute | Verified (T5, T7) |
| CID-03 | P1: One AI per connection | Execute | Verified (T17, T19) |
| CID-04 | P1: One AI per connection | Execute | Verified (T1, bebd8d1) |
| CID-05 | P1: One AI per connection | Execute | Verified (T21, T22) |
| CID-06 | P1: Direct delivery | Execute | Verified (T7, T12, T13, T14, T15, T16) |
| CID-07 | P1: Direct delivery | Execute | Verified (T7) |
| CID-08 | P1: Direct delivery | Execute | Verified (T2, T3, T12, T13) |
| CID-09 | P1: Direct delivery | Execute | Verified (T3, T4, T12, T13) |
| CID-10 | P1: Direct delivery | Execute | Verified (T4, T12, T39) |
| CID-11 | P1: Direct delivery | Execute | Verified (T3) |
| CID-12 | P1: Direct delivery | Execute | Verified (T7-T11) |
| CID-13 | P1: Direct delivery | Execute | Verified (T7) |
| CID-14 | P1: Direct delivery | Execute | Verified (T7, T17) |
| CID-15 | P1: Answer through the connection | Execute | Verified (T17, T18) |
| CID-16 | P1: Answer through the connection | Execute | Verified (T17, T19, T20) |
| CID-17 | P1: Answer through the connection | Execute | Verified (T17, T19) |
| CID-18 | P1: Answer through the connection | Execute | Verified (T17, T18) |
| CID-19 | P1: Partyline chat is one more app | Execute | Verified (T23) |
| CID-20 | P1: Partyline chat is one more app | Execute | Verified (T25, T26) |
| CID-21 | P1: Partyline chat is one more app | Execute | Verified (T24, T30, T36, T37) |
| CID-22 | P1: Partyline chat is one more app | Execute | Verified (T20, T24, T26-T35, T38) |
| CID-23 | P1: Partyline chat is one more app | Execute | Verified (T6, T29) |
| CID-24 | Edge cases | Execute | Verified (T22) |
| CID-25 | Edge cases | Execute | Verified (T12, T14) |
| CID-26 | Edge cases | Execute | Verified (T2, T3, T7) |
| CID-27 | P1: Direct delivery | Execute | Verified (T2, T6, T29, T36, T38) |
| CID-28 | P2: Gaps found in production | Fix | Verified (whatsapp/status_test.go) |
| CID-29 | P2: Gaps found in production | Fix | Verified (whatsapp/parse_test.go, connector_test.go) |
| CID-30 | P2: Gaps found in production | Fix | Verified (whatsapp/vcard_test.go, parse_test.go) |
| CID-31 | P2: Gaps found in production | Fix | Verified (address tests of the three kinds, connectors/sends_newchat_test.go, api/agent_connection_newchat_test.go, channel/connection_tools_test.go) |

**Coverage:** 31 total. CID-01..27 are mapped to tasks (`tasks.md` T1-T39), all Verified (validation.md, 2026-10-23); CID-28 and the ones after it are fixes of v0.6.2 and are Verified by the tests named in their rows.

---

## Success Criteria

- [ ] André writes to his bot number in a WhatsApp group and only its operator AI receives it and answers, unsigned.
- [ ] No platform message text is stored outside the delivery queue.
- [ ] André's existing team room holds him and his AIs, and no chat.
