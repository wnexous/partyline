# Core Platform Specification

## Problem Statement

People run many Claude Code sessions (some in loops) and want them reachable from chat apps and from each other, with clear ownership. Today each project glues its own WhatsApp bridge, with no accounts, no sharing and no safety model. Partyline's core gives a self-hosted, multi-user server where rooms connect members and messages flow under explicit permissions.

## Goals

- [ ] A fresh instance goes from `docker run` to a logged-in admin in under 2 minutes.
- [ ] Every API call is authorized through one grants model; no endpoint leaks another user's private resource (covered by tests per endpoint).
- [ ] Messages posted in a room reach every eligible member exactly by the delivery rules, survive restarts and are visible live in the web app within 1 second on localhost.

## Out of Scope

| Feature | Reason |
| --- | --- |
| OAuth / OIDC login | Deferred; local accounts + invites chosen for v1 |
| Postgres / multi-instance | AD-001: single SQLite instance |
| Teams / groups of users | Grants to individual users or everyone cover v1 |
| Message edit / delete propagation | Inbound edits are recorded as notices only |
| End-to-end encryption | Server must read messages to route them |
| Email delivery (password reset mails) | Admin resets passwords via CLI or UI |

---

## Assumptions & Open Questions

| Assumption / decision | Chosen default | Rationale | Confirmed? |
| --- | --- | --- | --- |
| Routing model | Rooms with members (AD-002) | User picked "Salas" | y |
| Untrusted content | Delivered in full, flagged `trusted=false` | User picked "Tudo, marcado" | y |
| Login | Local accounts, first user via setup token, others via invites | User picked "Contas locais + convite" | y |
| Admin visibility | Admins manage accounts only, no implicit access to resources | "Privado total" must hold inside companies | y |
| Agent loop guard limit | 25 consecutive agent-authored messages pause agent delivery in that room | Stops runaway agent ping-pong while allowing real collaboration | n |
| Message retention | 90 days per room by default, 0 = keep forever | Bounded disk use; configurable per room | n |
| Text limit | 16,000 characters per message | Larger than any chat platform limit; bounds abuse | n |
| Attachment limit | 25 MiB per file (env `PARTYLINE_MAX_UPLOAD_MB`) | Matches common chat platform limits | n |
| Session lifetime | 30 days, sliding, revocable | Typical self-hosted dashboard | n |
| IDs | UUIDv7 strings | Sortable, no coordination | n |

**Open questions:** none - all resolved or logged above.

---

## User Stories

### P1: First-run setup and accounts ⭐ MVP

**User Story**: As the person deploying Partyline, I want to create the admin account safely and invite colleagues so that only people I choose can log in.

**Why P1**: Nothing else is reachable without accounts.

**Acceptance Criteria**:

1. WHEN the server starts with zero users THEN the system SHALL print a one-time setup token to the log and accept `POST /api/v1/setup` only with that token. (AUTH-01)
2. IF `POST /api/v1/setup` is called after a user exists THEN the system SHALL respond 409 and create nothing. (AUTH-02)
3. WHEN valid credentials are posted to `/api/v1/auth/login` THEN the system SHALL set an HttpOnly, SameSite=Lax session cookie valid for 30 days. (AUTH-03)
4. IF a login fails THEN the system SHALL respond 401 with the same body for unknown user and wrong password. (AUTH-04)
5. IF more than 5 failed logins happen for one IP within 1 minute THEN the system SHALL respond 429 to further attempts from that IP for the rest of that minute. (AUTH-05)
6. WHEN an admin creates an invite THEN the system SHALL return a single-use link that expires after 7 days. (AUTH-06)
7. WHEN an invite is accepted with a valid username and a password of at least 10 characters THEN the system SHALL create the account and mark the invite used. (AUTH-07)
8. The system SHALL store passwords only as argon2id hashes and API tokens only as SHA-256 hashes. (AUTH-08)
9. WHEN a user logs out or revokes a session THEN the system SHALL reject that session cookie with 401 on the next request. (AUTH-09)
10. WHEN an admin disables a user THEN the system SHALL reject that user's sessions and tokens with 401. (AUTH-10)

**Independent Test**: Start an empty instance, set up the admin with the logged token, invite a second user, log in as both.

---

### P1: Tokens for agents and scripts ⭐ MVP

**User Story**: As a user, I want personal tokens so that my Claude Code sessions and scripts can act as me without my password.

**Why P1**: Agents connect with tokens.

**Acceptance Criteria**:

1. WHEN a user creates a token THEN the system SHALL show the secret once, prefixed `pl_agent_` or `pl_user_`, and afterwards only its 12-character prefix. (TOK-01)
2. IF a request presents a revoked, expired or unknown token THEN the system SHALL respond 401. (TOK-02)
3. WHILE a request is authenticated by an agent token the system SHALL allow only agent endpoints and respond 403 to every other endpoint. (TOK-03)
4. WHEN a token authenticates a request THEN the system SHALL update its `last_used_at` at most once per minute. (TOK-04)

**Independent Test**: Create both token kinds, call an agent endpoint and a management endpoint with each, check status codes.

---

### P1: Rooms, members and sharing ⭐ MVP

**User Story**: As a user, I want to create rooms, add agents, chats and people, and choose who can see them so that my sessions talk only where I allow.

**Why P1**: Core routing primitive (AD-002).

**Acceptance Criteria**:

1. WHEN a user creates a room THEN the system SHALL make that user its owner with `manage` level. (ACL-01)
2. The system SHALL order room levels as `view` < `post` < `manage` and resolve a user's level as the highest of ownership, direct grants and the everyone (`*`) grant. (ACL-02)
3. IF a user without `view` requests a room, its messages or its members THEN the system SHALL respond 404. (ACL-03)
4. IF a user with `view` but not `post` posts a message THEN the system SHALL respond 403. (ACL-04)
5. WHEN a manager adds an agent member THEN the system SHALL require that the manager owns the agent or holds a `use` grant on it, else respond 403. (ACL-05)
6. WHEN a manager adds a chat member THEN the system SHALL require that the manager owns the chat's connector or holds a `use` grant on it, else respond 403. (ACL-06)
7. WHEN the owner sets visibility to private, shared or public THEN the system SHALL replace the room's grants with: none; the listed users at the chosen level; everyone at `view` or `post`. (ACL-07)
8. The system SHALL give instance admins no implicit access to rooms, agents or connectors they neither own nor hold grants on. (ACL-08)
9. WHEN a member is removed THEN the system SHALL stop all future deliveries to it within that room. (ROOM-01)
10. IF a room is deleted THEN the system SHALL delete its messages, attachments, cursors and outbox entries. (ROOM-02)

**Independent Test**: Two users; user A creates private room, B gets 404; A shares with B as `view`; B reads but gets 403 on post.

---

### P1: Messages and delivery rules ⭐ MVP

**User Story**: As a room member, I want every message stored in order and delivered to the right members so that agents and chats see a consistent conversation.

**Why P1**: The product's core value.

**Acceptance Criteria**:

1. WHEN a message is accepted THEN the system SHALL assign it the next per-room sequence number, starting at 1 with no gaps. (MSG-01)
2. IF message text exceeds 16,000 characters or is empty without attachments THEN the system SHALL respond 422. (MSG-02)
3. The system SHALL never deliver a message back to the member that authored it. (DLV-01)
4. WHEN a message is accepted THEN the system SHALL deliver it to every agent member other than the author. (DLV-02)
5. WHEN an agent or user posts without explicit recipients THEN the system SHALL queue it for every chat member whose `outbound` flag is on. (DLV-03)
6. WHEN an agent or user posts with explicit recipients THEN the system SHALL queue it only for the listed chat members. (DLV-04)
7. WHERE the room setting `bridge_chats` is on the system SHALL also queue chat-authored messages for the other chat members; otherwise it SHALL NOT. (DLV-05)
8. WHEN a message is delivered to an agent THEN the system SHALL set `trusted=true` only if the author resolves to the recipient agent's owner (as user, as one of the owner's agents, or as one of the owner's verified identities). (DLV-06)
9. IF a queued outbound send fails THEN the system SHALL retry with exponential backoff starting at 2 seconds for at most 5 attempts and then mark it `failed`. (DLV-07)
10. WHEN the room's consecutive agent-authored messages reach 25 THEN the system SHALL pause delivery to agents in that room and post a system message. (DLV-08)
11. WHEN a user with `manage` resumes a paused room THEN the system SHALL reset the counter and resume agent delivery. (DLV-09)
12. WHEN a user or chat message arrives THEN the system SHALL reset the room's consecutive agent counter to 0. (DLV-10)

**Independent Test**: Room with two agents, one chat with outbound on and one off; post from each author type and assert the delivery set.

---

### P1: Live updates ⭐ MVP

**User Story**: As a user, I want the web app to update live so that I watch conversations, presence and failures as they happen.

**Why P1**: Requested explicitly ("front com ws pra ver tudo ao vivo").

**Acceptance Criteria**:

1. WHEN a message is stored in a room THEN the system SHALL push a `room.message` event to every live connection of users with `view` on that room. (LIVE-01)
2. The system SHALL never push an event about a resource to a user without access to it. (LIVE-02)
3. IF the live WebSocket upgrade request has an Origin different from the public URL THEN the system SHALL reject it with 403. (LIVE-03)
4. WHEN agent presence, connector status or outbox status changes THEN the system SHALL push `agent.presence`, `connector.status` or `outbox.status` to users with access. (LIVE-04)

**Independent Test**: Two browsers as different users; post in a room visible to one only; only that one receives the event.

---

### P1: Secure, observable deployment ⭐ MVP

**User Story**: As an operator, I want a hardened single container so that I can expose it on the internet behind TLS.

**Why P1**: Requested ("sistema de segurança pra expor", "dockerzão").

**Acceptance Criteria**:

1. The system SHALL send `Content-Security-Policy`, `X-Content-Type-Options: nosniff`, `Referrer-Policy: same-origin` and `X-Frame-Options: DENY` on every HTML response. (SEC-01)
2. IF a cookie-authenticated state-changing request has an Origin or Sec-Fetch-Site other than the instance's own THEN the system SHALL respond 403. (SEC-02)
3. IF a request body exceeds 1 MiB (JSON) or the upload limit (multipart) THEN the system SHALL respond 413. (SEC-03)
4. The system SHALL encrypt connector secrets at rest with AES-256-GCM using `PARTYLINE_SECRET_KEY`, generating and persisting a key with mode 0600 when none is set. (SEC-04)
5. The system SHALL never write message text, passwords or token secrets to logs. (SEC-05)
6. WHEN `/healthz` is requested THEN the system SHALL respond 200 without authentication; `/readyz` SHALL respond 503 until migrations finished. (OPS-01)
7. WHEN the daily retention job runs THEN the system SHALL delete messages and attachments older than the room's retention days (default 90, 0 = forever). (OPS-02)
8. The container image SHALL run as a non-root user and store all state under `/data`. (OPS-03)

**Independent Test**: Run the image, check headers with curl, attempt a cross-origin POST, inspect logs for message text.

---

### P2: Audit trail

**User Story**: As an admin, I want an audit log of security events so that I can see who changed access.

**Why P2**: Valuable for companies, not needed to route messages.

**Acceptance Criteria**:

1. WHEN a login, logout, invite, token, grant, user-state or connector change happens THEN the system SHALL append an audit entry with actor, action, target and time, without secrets. (AUD-01)
2. WHEN an admin lists audit entries THEN the system SHALL return them newest first, 50 per page. (AUD-02)

**Independent Test**: Perform each action and list the log.

---

## Edge Cases

- IF two messages are posted to the same room concurrently THEN the system SHALL assign distinct consecutive sequence numbers (MSG-01).
- IF the server restarts with outbound sends queued THEN the system SHALL resume them (DLV-07).
- IF the last manager of a room is deleted THEN the system SHALL delete the room (ROOM-02).
- WHEN a grant targets a disabled user THEN the system SHALL keep it but deny access while the user is disabled (AUTH-10).

---

## Requirement Traceability

| Requirement ID | Story | Phase | Status |
| --- | --- | --- | --- |
| AUTH-01 | P1: Setup and accounts | Tasks | Pending |
| AUTH-02 | P1: Setup and accounts | Tasks | Pending |
| AUTH-03 | P1: Setup and accounts | Tasks | Pending |
| AUTH-04 | P1: Setup and accounts | Tasks | Pending |
| AUTH-05 | P1: Setup and accounts | Tasks | Pending |
| AUTH-06 | P1: Setup and accounts | Tasks | Pending |
| AUTH-07 | P1: Setup and accounts | Tasks | Pending |
| AUTH-08 | P1: Setup and accounts | Tasks | Pending |
| AUTH-09 | P1: Setup and accounts | Tasks | Pending |
| AUTH-10 | P1: Setup and accounts | Tasks | Pending |
| TOK-01 | P1: Tokens | Tasks | Pending |
| TOK-02 | P1: Tokens | Tasks | Pending |
| TOK-03 | P1: Tokens | Tasks | Pending |
| TOK-04 | P1: Tokens | Tasks | Pending |
| ACL-01 | P1: Rooms and sharing | Tasks | Pending |
| ACL-02 | P1: Rooms and sharing | Tasks | Pending |
| ACL-03 | P1: Rooms and sharing | Tasks | Pending |
| ACL-04 | P1: Rooms and sharing | Tasks | Pending |
| ACL-05 | P1: Rooms and sharing | Tasks | Pending |
| ACL-06 | P1: Rooms and sharing | Tasks | Pending |
| ACL-07 | P1: Rooms and sharing | Tasks | Pending |
| ACL-08 | P1: Rooms and sharing | Tasks | Pending |
| ROOM-01 | P1: Rooms and sharing | Tasks | Pending |
| ROOM-02 | P1: Rooms and sharing | Tasks | Pending |
| MSG-01 | P1: Messages and delivery | Tasks | Pending |
| MSG-02 | P1: Messages and delivery | Tasks | Pending |
| DLV-01 | P1: Messages and delivery | Tasks | Pending |
| DLV-02 | P1: Messages and delivery | Tasks | Pending |
| DLV-03 | P1: Messages and delivery | Tasks | Pending |
| DLV-04 | P1: Messages and delivery | Tasks | Pending |
| DLV-05 | P1: Messages and delivery | Tasks | Pending |
| DLV-06 | P1: Messages and delivery | Tasks | Pending |
| DLV-07 | P1: Messages and delivery | Tasks | Pending |
| DLV-08 | P1: Messages and delivery | Tasks | Pending |
| DLV-09 | P1: Messages and delivery | Tasks | Pending |
| DLV-10 | P1: Messages and delivery | Tasks | Pending |
| LIVE-01 | P1: Live updates | Tasks | Pending |
| LIVE-02 | P1: Live updates | Tasks | Pending |
| LIVE-03 | P1: Live updates | Tasks | Pending |
| LIVE-04 | P1: Live updates | Tasks | Pending |
| SEC-01 | P1: Secure deployment | Tasks | Pending |
| SEC-02 | P1: Secure deployment | Tasks | Pending |
| SEC-03 | P1: Secure deployment | Tasks | Pending |
| SEC-04 | P1: Secure deployment | Tasks | Pending |
| SEC-05 | P1: Secure deployment | Tasks | Pending |
| OPS-01 | P1: Secure deployment | Tasks | Pending |
| OPS-02 | P1: Secure deployment | Tasks | Pending |
| OPS-03 | P1: Secure deployment | Tasks | Pending |
| AUD-01 | P2: Audit trail | Tasks | Pending |
| AUD-02 | P2: Audit trail | Tasks | Pending |

**Coverage:** 50 total, 0 mapped to tasks, 50 unmapped ⚠️ (mapped in tasks.md)

---

## Success Criteria

- [ ] `docker run` to logged-in admin in under 2 minutes following the README.
- [ ] Authorization test matrix: every endpoint tested with owner, grantee, stranger and admin-without-grant.
- [ ] A message posted in a room appears in a second browser within 1 second on localhost.
