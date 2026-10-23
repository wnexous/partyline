# Connection Identity Specification

## Problem Statement

Today a platform account (a WhatsApp number, a Telegram or Discord bot) belongs to its owner, and any AI or user in a room can write through it, signed. A person who talks to that number in a WhatsApp group is in fact talking to several agents behind one number, and partyline groups mix chats with intelligences. André wants each account to be one intelligence: the number IS an agent (2026-10-22).

## Goals

- [ ] Each connection belongs to exactly one intelligence (its operator); only that intelligence sends through it.
- [ ] Partyline groups hold intelligences only (users and AIs); platform chats are never group members.
- [ ] Every platform chat a connection receives is a conversation of its operator with the people in that chat.

## Out of Scope

| Feature | Reason |
| --- | --- |
| Several accounts per intelligence of the same platform | One number per agent is enough now; an intelligence may operate several connections of different kinds (already allowed) |
| Relaying a platform chat into a partyline group automatically | The operator decides what to bring to a group (André's rule) |
| Connecting André's personal WhatsApp number | André keeps it out for now |
| External people as members of partyline groups | They are reached only through their platform conversation (DM-05) |
| Rewriting old messages | History stays in the room where it was written |

---

## Assumptions & Open Questions

| Assumption / decision | Chosen default | Rationale | Confirmed? |
| --- | --- | --- | --- |
| Operator of a connection | Required: the owner (default) or one AI the owner owns, as today (ACC-01) | Keeps the existing setting; only its meaning widens | y (André: his bot number is operated by one AI) |
| One account per WhatsApp connection | A WhatsApp connection names exactly one instance; a connection with zero or several instances receives nothing and shows "choose the instance (number) of this connection" until the owner picks one | One number = one intelligence; the old empty list meant "every instance" | n |
| Platform group chat | Becomes a conversation (group kind) whose members are the operator plus the people seen in the chat; it keeps the chat as its transport; nobody else can join it | André: "grupo com identidades, não com chats" | n |
| Platform DM chat | Unchanged: the DM between the person and the operator (ACC-02) | Already the target model | y |
| Who sends into a platform conversation | Only the operator; its posts go out unsigned; a human operator's own posts go out as typed (no signature) | The account is the operator's app | n |
| Who sees a platform conversation | The operator, the connection's owner, and users with `use` on the operator AI (AD-013); viewers other than the operator read only | Owner oversight without writing through someone else's account | n |
| Volume | Auto-route scope (off, dms, groups, all) chooses which chats **without a conversation** become conversations; a chat that has one keeps receiving whatever the scope; deleting a platform conversation detaches its chat and the scope decides again; the operator's delivery mode per conversation (all, mentions, trusted) limits what reaches its session; default `all` | André: "recebe tudo"; nothing that flows today is silenced (orchestrator, 2026-10-22) | y |
| Offline operator | Missed messages arrive on reconnect, newest 50 per conversation, then a skipped notice (AGT-08/09, already built) | Verified in code (internal/agents/gateway.go) | y |
| Existing rooms with chat members (upgrade) | A room whose only members are one group chat and that chat's operator becomes that chat's conversation (history kept in place); every other chat member leaves its room, which keeps its history, intelligences and settings; every chat that was in any room before the upgrade gets its operator conversation on its next message whatever the scope; catch-all rooms lose their chats | No silent fan-out through someone else's account after upgrade, and nothing that flows today is silenced | y (orchestrator, 2026-10-22) |
| Copies of AI DMs to a user's app (NOTI-02) | Sent only through a connection the notified user operates (their own account writes to them); other choices are cleared at upgrade | Keeps "no message leaves a connection with an author other than its operator" | y (orchestrator, 2026-10-22) |
| Operator change | Every platform conversation of the connection moves to the new operator (member swap, audit entry), like the DM move in ACC-02 | One account = one current identity | n |
| Room-to-chat features | `bridge_chats`, broadcast to chats, member signatures for chats, "route chat to agent" and catch-all rooms are retired | They exist only to put chats in rooms | n |
| Release | Breaking server change, shipped as v0.5.0; CLIs need no change | The channel protocol does not change | n |

**Open questions:** none - all resolved or logged above.

---

## User Stories

### P1: One identity per connection ⭐ MVP

**User Story**: As an owner, I want a number to be one agent so that people who talk to it know who answers.

**Acceptance Criteria**:

1. The system SHALL send a message through a connection only when its author is the connection's operator. (CID-01)
2. IF any other intelligence tries to post in a platform conversation THEN the system SHALL refuse with 403 `not_operator` and send nothing. (CID-02)
3. WHEN the operator posts in a platform conversation THEN the message SHALL go out through the connection unsigned. (CID-03)
4. IF a WhatsApp connection names zero or several instances THEN the system SHALL receive no message through it and SHALL show the status detail "choose the instance (number) of this connection". (CID-04)
5. WHEN the owner changes the operator THEN every platform conversation of the connection SHALL have the new operator as its member instead of the old one, and the system SHALL record one audit entry. (CID-05)

**Independent Test**: Operator of a Telegram connection = Atlas; Atlas's reply in the bot's group goes out unsigned; another AI posting there gets 403 `not_operator`.

---

### P1: Platform chats are the operator's conversations ⭐ MVP

**User Story**: As an operator AI, I want each chat of my number to be my own conversation so that I receive everything said to my number.

**Acceptance Criteria**:

1. WHERE the auto-route scope includes groups, WHEN a group chat not seen before sends a message THEN the system SHALL create a group conversation whose members are the operator and the people seen in that chat, and deliver the message there. (CID-06)
2. WHEN a person not seen before writes in that chat THEN the system SHALL add the person to the conversation (PEOP-01). (CID-07)
3. The system SHALL keep each platform chat in exactly one conversation. (CID-08)
4. WHILE the operator's session is offline the system SHALL keep the conversation's messages and deliver them on reconnect per AGT-08/09. (CID-09)
5. The system SHALL show a platform conversation to the operator, the connection's owner and users with `use` on the operator AI, and to no one else. (CID-10)
6. IF a viewer other than the operator posts in a platform conversation THEN the system SHALL refuse with 403 `not_operator` (CID-02). (CID-11)

**Independent Test**: A new WhatsApp group with Atlas's number appears as a conversation of Atlas with the people in it; the owner can read it; another AI of the owner cannot see it.

---

### P1: Groups hold intelligences only ⭐ MVP

**User Story**: As a user, I want partyline groups to contain intelligences so that a group is a talk between identities, not a switchboard of chats.

**Acceptance Criteria**:

1. IF a request adds a chat as a member of any conversation THEN the system SHALL refuse with 422 `chats_not_members`. (CID-12)
2. The add-member picker SHALL offer users and AIs only. (CID-13)
3. WHEN the server upgrades THEN a room whose only members are one group chat and that chat's operator SHALL become that chat's conversation, every other chat member SHALL leave its room, the room SHALL keep its history, intelligences and settings, and the next message of a chat that left SHALL create its operator conversation (CID-06 or ACC-02) whatever the auto-route scope. (CID-14)
4. The system SHALL no longer offer bridges between chats, broadcast to chats, "route chat to agent" or catch-all rooms, in the API or the web app. (CID-15)

**Independent Test**: After the upgrade, a room that held a WhatsApp group chat, the owner and two AIs lists the owner and the two AIs and no chat; the WhatsApp group is a conversation of the connection's operator.

---

## Edge Cases

- IF the operator AI is deleted THEN its platform conversations SHALL move to the connection owner (ACC-02 rule, extended to groups). (CID-16)
- WHEN the owner's own account writes in a chat (a human operator typing on the phone) THEN the message SHALL be authored by the operator, with trust per AD-014 (ACC-05 unchanged). (CID-17)
- IF the auto-route scope excludes a chat's kind and the chat has no conversation THEN the system SHALL keep no message from that chat (CON-07 unchanged); a chat that has a conversation SHALL keep receiving whatever the scope, and a chat whose platform conversation was deleted SHALL have none until the scope routes it again. (CID-18)

---

## Implicit-requirement sweep

| Dimension | Covered by |
| --- | --- |
| Input validation & bounds | CID-04 (one instance), CID-12 (no chat members) |
| Failure / partial-failure states | CID-04 (connection idle with a status detail); send failures keep the existing outbox retries |
| Idempotency / duplicates | CID-08 (one conversation per chat), PEOP-01 (one person per account) |
| Auth boundaries & rate limits | CID-01, CID-02, CID-10, CID-11; existing auto-route caps (60 new chats per hour) apply |
| Concurrency / ordering | CID-05 (operator change swaps members in one transaction with the audit entry) |
| Data lifecycle | CID-14 (upgrade keeps history), CID-16 (operator deleted) |
| Observability | CID-04 status detail; CID-05 audit entry |
| External-dependency failure | N/A because platform outages keep the existing connector status and retries |
| State-transition integrity | CID-05, CID-14, CID-16 |

---

## Requirement Traceability

| Requirement ID | Story | Phase | Status |
| --- | --- | --- | --- |
| CID-01 | P1: One identity per connection | Tasks | In Tasks |
| CID-02 | P1: One identity per connection | Tasks | In Tasks |
| CID-03 | P1: One identity per connection | Tasks | In Tasks |
| CID-04 | P1: One identity per connection | Tasks | Implementing |
| CID-05 | P1: One identity per connection | Tasks | In Tasks |
| CID-06 | P1: Platform chats are conversations | Tasks | In Tasks |
| CID-07 | P1: Platform chats are conversations | Tasks | In Tasks |
| CID-08 | P1: Platform chats are conversations | Tasks | In Tasks |
| CID-09 | P1: Platform chats are conversations | Tasks | In Tasks |
| CID-10 | P1: Platform chats are conversations | Tasks | In Tasks |
| CID-11 | P1: Platform chats are conversations | Tasks | In Tasks |
| CID-12 | P1: Groups hold intelligences only | Tasks | In Tasks |
| CID-13 | P1: Groups hold intelligences only | Tasks | In Tasks |
| CID-14 | P1: Groups hold intelligences only | Tasks | In Tasks |
| CID-15 | P1: Groups hold intelligences only | Tasks | In Tasks |
| CID-16 | Edge cases | Tasks | In Tasks |
| CID-17 | Edge cases | Tasks | In Tasks |
| CID-18 | Edge cases | Tasks | In Tasks |

**Coverage:** 18 total, 18 mapped to tasks (`tasks.md` T1-T36).

---

## Success Criteria

- [ ] André talks to his bot number in a WhatsApp group and only its operator AI answers, unsigned.
- [ ] André's existing team room holds him and his AIs, and no chat.
- [ ] No message leaves a connection with an author other than its operator.
