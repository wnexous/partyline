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
| Release | Breaking server change, shipped as v0.5.0 together with a CLI release (the channel learns the new event and tool arguments) | Old CLIs keep receiving room messages but not platform events | n |

**Open questions:** none - all resolved or logged above.

---

## User Stories

### P1: One AI per connection ⭐ MVP

**User Story**: As an owner, I want a number to be one AI so that people who talk to it know who answers.

**Acceptance Criteria**:

1. The system SHALL accept as a connection's operator only an AI that the connection's owner owns. (CID-01)
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
3. IF the chat does not belong to the named connection THEN the system SHALL refuse with `unknown_chat` (HTTP 404) and send nothing. (CID-17)
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
| CID-01 | P1: One AI per connection | Execute | Implementing (T5) |
| CID-02 | P1: One AI per connection | Execute | Implementing (T5, T7) |
| CID-03 | P1: One AI per connection | Tasks | In Tasks |
| CID-04 | P1: One AI per connection | Execute | Implemented (T1, bebd8d1) |
| CID-05 | P1: One AI per connection | Tasks | In Tasks |
| CID-06 | P1: Direct delivery | Execute | Implementing (T7, T12, T13) |
| CID-07 | P1: Direct delivery | Execute | Implementing (T7) |
| CID-08 | P1: Direct delivery | Execute | Implementing (T2, T3, T12, T13) |
| CID-09 | P1: Direct delivery | Execute | Implementing (T3, T4, T12, T13) |
| CID-10 | P1: Direct delivery | Execute | Implementing (T4, T12) |
| CID-11 | P1: Direct delivery | Execute | Implementing (T3) |
| CID-12 | P1: Direct delivery | Execute | Implementing (T7-T11) |
| CID-13 | P1: Direct delivery | Execute | Implementing (T7) |
| CID-14 | P1: Direct delivery | Execute | Implementing (T7) |
| CID-15 | P1: Answer through the connection | Tasks | In Tasks |
| CID-16 | P1: Answer through the connection | Tasks | In Tasks |
| CID-17 | P1: Answer through the connection | Tasks | In Tasks |
| CID-18 | P1: Answer through the connection | Tasks | In Tasks |
| CID-19 | P1: Partyline chat is one more app | Tasks | In Tasks |
| CID-20 | P1: Partyline chat is one more app | Tasks | In Tasks |
| CID-21 | P1: Partyline chat is one more app | Tasks | In Tasks |
| CID-22 | P1: Partyline chat is one more app | Tasks | In Tasks |
| CID-23 | P1: Partyline chat is one more app | Execute | Implementing (T6) |
| CID-24 | Edge cases | Tasks | In Tasks |
| CID-25 | Edge cases | Execute | Implementing (T12) |
| CID-26 | Edge cases | Execute | Implementing (T2, T3, T7) |
| CID-27 | P1: Direct delivery | Execute | Implementing (T2, T6) |

**Coverage:** 27 total, 27 mapped to tasks (`tasks.md` T1-T38); CID-04 implemented (T1).

---

## Success Criteria

- [ ] André writes to his bot number in a WhatsApp group and only its operator AI receives it and answers, unsigned.
- [ ] No platform message text is stored outside the delivery queue.
- [ ] André's existing team room holds him and his AIs, and no chat.
