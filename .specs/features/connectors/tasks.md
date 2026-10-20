# Connectors Tasks

## Execution Protocol (MANDATORY -- do not skip)

Implement these tasks with the `tlc-spec-driven` skill: **activate it by name and follow its Execute flow and Critical Rules.** Do not search for skill files by filesystem path. The skill is the source of truth for the full flow (per-task cycle, sub-agent delegation, adequacy review, Verifier, discrimination sensor).

**If the skill cannot be activated, STOP and tell the user - do not proceed without it.**

---

**Design**: `.specs/features/connectors/design.md`
**Status**: Approved
**Depends on feature**: core-platform (all), agent-channel T1 (agents table usage in route-to-agent)

---

## Test Coverage Matrix

> Guidelines found: `AGENTS.md` (repo), core-platform matrix.

| Code Layer | Required Test Type | Coverage Expectation | Location Pattern | Run Command |
| --- | --- | --- | --- | --- |
| Schema/validation, WhatsApp parsing (pure) | unit | All branches; 1:1 ACs; fixtures for every message kind | `internal/connectors/**/*_test.go` | `go test -count=1 ./internal/connectors/...` |
| Manager, inbound pipeline, identities (services) | integration | Every AC + error paths with real SQLite and fake connector | `internal/connectors/*_test.go`, `internal/identities/*_test.go` | `go test -race -count=1 ./internal/connectors/... ./internal/identities/...` |
| Platform adapters | integration against fakes (httptest evolution-go + WS, httptest Bot API, discordgo session fake) | Each AC: inbound, send text, send file, reply, list chats, failure | `internal/connectors/<kind>/*_test.go` | `go test -race -count=1 ./internal/connectors/...` |
| HTTP endpoints, webhook | e2e (testkit) | Every route: happy + edge + error; auth matrix | `internal/api/*_test.go` | `go test -race -count=1 ./internal/api/...` |

## Gate Check Commands

| Gate Level | When to Use | Command |
| --- | --- | --- |
| Quick | Unit-only tasks | `go test -count=1 ./internal/connectors/...` |
| Full | Integration/e2e tasks | `make test` (Phase 3: `go test -race -count=1 -timeout 20m ./... && make web-test`, since the Makefile sets no timeout) |
| Build | Last task of a phase | `make check` |

---

## Execution Plan

### Phase 1: Framework

```
T1 → T2 → T3 → T4 → T5 → T6
```

### Phase 2: Platforms

```
T7 → T8 → T9 → T10 → T11
```

### Phase 3: Verifier fix iteration 1

Gaps from `validation.md` (2026-10-20): S1 (blocker), S2-S4, M31, M04 and the minor notes.

```
T12 → T13 → T14 → T15 → T16 → T17 → T18 → T19 → T20 → T21
```

---

## Task Breakdown

### Phase 1: Framework

#### T1: Kind registry, schema and config handling

**What**: `Kind`, `Field`, `Register/Kinds/Lookup`, `Validate(kind, cfg)`, secret split (`secrets_enc`) and masking (`"********"`, PATCH keeps stored secret).
**Where**: `internal/connectors/kind.go`
**Depends on**: None
**Reuses**: secrets.Box (core T4)
**Requirement**: CON-01, CON-02, CON-03
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] Missing required → error map `{field: "required"}`; bad url → `{field: "invalid url"}`
- [x] Masked read never contains secret; PATCH with mask keeps original secret
**Tests**: unit
**Gate**: quick
**Commit**: `feat(connectors): add kind registry, config schema and secret masking`

#### T2: Connectors service and endpoints

**What**: service CRUD (owner, enable/disable), sharing via access grants, handlers `GET /connector-kinds`, `GET/POST /connectors`, `GET/PATCH/DELETE /connectors/{id}`, `GET/PUT /connectors/{id}/sharing`, audit entries.
**Where**: `internal/api/connectors_handlers.go`
**Depends on**: T1
**Reuses**: access, audit
**Requirement**: CON-01, CON-02, CON-03, CON-05
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] Auth matrix owner/use-grantee/stranger/admin; 422 names field; secrets masked in every response
**Tests**: e2e
**Gate**: full
**Commit**: `feat(connectors): add connector management endpoints`

#### T3: Manager and supervisor

**What**: `connectors.Manager` start enabled on boot, `Apply/Stop`, supervisor backoff 2→120 s, status persist + `ConnectorStatus` events, error redaction, `Send(connectorID, Outbound)`.
**Where**: `internal/connectors/manager.go`
**Depends on**: T2
**Reuses**: events
**Requirement**: CON-04, CON-05, CON-06
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] Fake connector started within 2 s of create; stopped on disable; Send after stop → error
- [x] Run error containing the secret → status text with `[redacted]`; retries with growing delay (fake clock)
**Tests**: integration
**Gate**: full
**Commit**: `feat(connectors): add connector manager with supervision`

#### T4: Identities and pairing

**What**: `identities` service (CreatePairing, Link, ResolveSender, List, Delete) and endpoints `GET /identities`, `POST /identities/pairing`, `DELETE /identities/{id}`; `IdentityLinked` event.
**Where**: `internal/identities/identities.go`
**Depends on**: T3
**Reuses**: store, events
**Requirement**: IDN-01, IDN-02, IDN-03, IDN-04
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] Code matches `^[A-HJ-NP-Z2-9]{6}$`, expires after 10 min (fake clock), single use
- [x] ResolveSender by sender id or alt id; after Delete → empty
**Tests**: integration
**Gate**: full
**Commit**: `feat(identities): add verified identities with pairing codes`

#### T5: Inbound pipeline and chats

**What**: `inbound.go` per design (self drop, `!link`/`/link`, chat upsert, rooms lookup, media save, PostFromChat with dedupe); chats endpoints `GET /connectors/{id}/chats`, `POST /connectors/{id}/chats/refresh`, `GET /chats`, `POST /chats/{id}/route`.
**Where**: `internal/connectors/inbound.go`
**Depends on**: T4
**Reuses**: rooms.PostFromChat, attachments, identities
**Requirement**: CON-07, CON-08, CON-09, CON-10, CON-11, CON-12, IDN-02, IDN-03
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] Unrouted chat → chat row exists, messages table count unchanged
- [x] Routed chat in 2 rooms → 2 messages; same external id again → still 2
- [x] Oversized media → attachment status `too_large`, no blob
- [x] `/chats/{id}/route` creates private room with chat + agent members
**Tests**: integration
**Gate**: full
**Commit**: `feat(connectors): add inbound pipeline and chat endpoints`

#### T6: Outbound sender wiring

**What**: implement `delivery.Sender` with the Manager: load message, attachments (as OutFile), chat external id, reply quoting (`ReplyToID`/`ReplyToSenderID` when replied message came from the same chat); register in app.
**Where**: `internal/connectors/sender.go`
**Depends on**: T5
**Reuses**: OutboxWorker (core T21)
**Requirement**: CON-05, DLV-07 (integration)
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] Agent reply to a chat message reaches the fake connector with `ReplyToID` = original external id
- [x] Disabled connector → send failure → retry path
**Tests**: integration
**Gate**: build
**Commit**: `feat(connectors): deliver outbox messages through connectors`

### Phase 2: Platforms

#### T7: WhatsApp message parsing

**What**: pure parser from evolution-go `Message` payload to `connectors.Inbound` (envelopes, kinds, quote, bare JIDs, group vs DM, self).
**Where**: `internal/connectors/whatsapp/parse.go`
**Depends on**: T6
**Reuses**: earlier private prototype parsing knowledge (re-implemented)
**Requirement**: WA-01, WA-02
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] Fixtures (synthetic, no real data) for text, extended text, image with caption, ephemeral wrapper, view-once, edit, revoke, reaction, location, unknown
- [x] `5541…:12@s.whatsapp.net` → `5541…@s.whatsapp.net`; chat id `<instance>|<jid>`
**Tests**: unit
**Gate**: quick
**Commit**: `feat(whatsapp): parse evolution-go message events`

#### T8: WhatsApp connector

**What**: kind registration, broadcast WS loop, instance cache, send text/media, list groups, media open (inline base64 or downloadmedia).
**Where**: `internal/connectors/whatsapp/connector.go`
**Depends on**: T7
**Reuses**: parse (T7)
**Requirement**: WA-01, WA-03, WA-04, WA-05
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] Fake evolution server: WS event → Inbound; `/send/text` body includes `quoted` on reply; `/send/media` base64 + type
- [x] Instance not in allowlist ignored; UA header `partyline/<version>` sent
**Tests**: integration
**Gate**: full
**Commit**: `feat(whatsapp): add evolution-go connector`

#### T9: Telegram connector

**What**: kind registration, getMe/getUpdates loop, sendMessage split 4,096 with `reply_parameters`, sendPhoto/sendDocument, getFile media, chat migration.
**Where**: `internal/connectors/telegram/connector.go`
**Depends on**: T8
**Reuses**: none
**Requirement**: TG-01, TG-02, TG-03, TG-04
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] Fake Bot API: offset increments; 9,000-char text → 3 ordered calls; png → sendPhoto, pdf → sendDocument
- [x] Token never appears in errors returned to the manager
**Tests**: integration
**Gate**: full
**Commit**: `feat(telegram): add bot api connector`

#### T10: Discord connector

**What**: kind registration, session seam + discordgo adapter, MessageCreate handler (skip bots), send split 2,000 with reference and files, list guild text channels.
**Where**: `internal/connectors/discord/connector.go`
**Depends on**: T9
**Reuses**: none
**Requirement**: DC-01, DC-02, DC-03
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] Fake session: bot author ignored; 4,500 chars → 3 sends, first with reference; channels listed
**Tests**: integration
**Gate**: full
**Commit**: `feat(discord): add gateway connector`

#### T11: Webhook connector and hook endpoint

**What**: kind registration (generated secret), `POST /api/v1/hooks/{connector_id}` with signature/timestamp checks, 64 KiB limit, 60/min; outbound signed POST with expiring attachment links (`GET /api/v1/hooks/files/{token}`).
**Where**: `internal/connectors/webhook/connector.go`
**Depends on**: T10
**Reuses**: inbound pipeline
**Requirement**: HOOK-01, HOOK-02, HOOK-03
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] Valid signed payload → 202 and stored message in routed room
- [x] Bad signature → 401; timestamp 301 s old → 401; nothing stored
- [x] Outbound received by httptest with valid signature; 500 response → retry path
**Tests**: e2e
**Gate**: build
**Commit**: `feat(webhook): add signed webhook connector`

### Phase 3: Verifier fix iteration 1

#### T12: Scope verified identities to the connector instance (S1)

**What**: identities belong to the connector they were linked through. Migration `0003` recreates `identities` with
`connector_id` (FK to connectors, cascade; unique `(connector_id, external_id)`) and drops existing rows (users re-link).
`Link` stores the connector id; `ResolveSender(connectorID, sender, alt)` matches only that connector. Kinds whose
sender ids are chosen by the caller (`Kind.LinkOwnerOnly`, the webhook) link only codes of the connector's owner.
**Where**: `internal/store/migrations/0003_identities_per_connector.sql`, `internal/store/queries/identities.sql`,
`internal/identities/identities.go`, `internal/connectors/{inbound,manager,kind}.go`, `internal/connectors/webhook/connector.go`,
`internal/api/dto/connectors.go` (`Identity.connector_id`)
**Depends on**: T11
**Reuses**: pipeline, testkit, agent WS
**Requirement**: IDN-02, IDN-04, DLV-06
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] Verifier probe end to end: Alice links webhook sender `alice-id` through her connector; Mallory's webhook posts
  `sender_id: "alice-id"` → stored `author_user_id` empty and Alice's agent receives it with `trusted=false`
- [x] The same sender on another connector of the same kind resolves to no user; on the linking connector it resolves
- [x] A webhook `!link` with another user's code links nothing and does not consume the code; the owner's code links
- [x] Migration drops pre-existing identities
**Tests**: integration, e2e
**Gate**: full
**Commit**: `fix(identities): scope verified identities to the connector instance`

#### T13: Safe outbound HTTP for connectors (S2)

**What**: shared client (`internal/connectors/safehttp`): dial-time check of the resolved address (loopback, private,
link-local, CGNAT, multicast, unspecified blocked), no redirects, timeouts, response size cap; escape hatch
`PARTYLINE_ALLOW_PRIVATE_NETWORKS=true` (config, `Deps.AllowPrivateNetworks`, README). Telegram, WhatsApp (REST + WS),
Discord media and webhook outbound use it. Platform error bodies (Telegram `description`) never reach status text.
**Where**: `internal/connectors/safehttp/`, `internal/config/config.go`, adapters, `README*.md`
**Depends on**: T12
**Requirement**: CON-13, CON-06
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] Loopback/private/link-local/CGNAT/multicast/unspecified destinations fail at dial time, also through a hostname
- [x] Redirects are not followed; oversized responses fail
- [x] Webhook outbound to a loopback URL fails by default and works with the escape hatch
- [x] Telegram error descriptions are not in the error text
**Tests**: unit, e2e
**Gate**: full
**Commit**: `fix(connectors): block private network destinations for outbound calls`

#### T14: Webhook rate limit counts authenticated requests only (S3)

**What**: verify timestamp and signature first; the 60/min budget counts only authenticated requests; invalid attempts
get their own per-IP budget (over it: 429, no audit entry).
**Where**: `internal/connectors/webhook/connector.go`, `internal/connectors/connector.go` (`HookRequest.RemoteIP`),
`internal/api/hooks_handlers.go`
**Depends on**: T13
**Requirement**: HOOK-04
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] 100 bad-signature requests do not stop a valid one
- [x] The 61st authenticated request in a minute gets 429
- [x] An IP over its invalid-attempt budget gets 429
**Tests**: unit, e2e
**Gate**: full
**Commit**: `fix(webhook): count only authenticated requests toward the rate limit`

#### T15: Reject replayed webhook requests (S4)

**What**: an authenticated request whose signature was already seen within 10 minutes is refused (409); dedupe by
`message_id` stays.
**Where**: `internal/connectors/webhook/connector.go`, `internal/connectors/connector.go` (`ErrReplayed`),
`internal/api/hooks_handlers.go`
**Depends on**: T14
**Requirement**: HOOK-05
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] The same signed request twice → 202 then 409, one message stored
- [x] Entries expire after 10 minutes (fake clock)
**Tests**: unit, e2e
**Gate**: full
**Commit**: `fix(webhook): reject replayed signed requests`

#### T16: Per-chat reply quoting test (M31)

**What**: a reply to chat B's message delivered to chat A carries no quote.
**Where**: `internal/connectors/sender_test.go`
**Depends on**: T15
**Requirement**: WA-03 (sender side), DLV-07
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] Two chats in one room; reply to chat B's message → chat A's send has empty `ReplyToID`/`ReplyToSenderID`,
  chat B's send quotes it
**Tests**: integration
**Gate**: full
**Commit**: `test(connectors): quote replies only in the chat they came from`

#### T17: Repeated inbound message downloads nothing (M04)

**What**: a repeated message id with media opens the media once.
**Where**: `internal/connectors/inbound_test.go`
**Depends on**: T16
**Requirement**: CON-10, CON-11
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] Same message id with media emitted twice → `Open` called once, one attachment, one blob
**Tests**: integration
**Gate**: full
**Commit**: `test(connectors): repeated inbound messages download nothing`

#### T18: Consistent Discord chat titles

**What**: inbound guild messages carry the title `Guild #channel`, the same as ListChats.
**Where**: `internal/connectors/discord/connector.go`
**Depends on**: T17
**Requirement**: DC-01, DC-03
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] Inbound title equals the listed title for the same channel
**Tests**: integration
**Gate**: full
**Commit**: `fix(discord): use the guild and channel name as chat title everywhere`

#### T19: Truncate long inbound text

**What**: inbound text over 16,000 characters is stored truncated with a `…[truncated]` marker instead of dropped.
**Where**: `internal/connectors/inbound.go`
**Depends on**: T18
**Requirement**: CON-14
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] 20,000-character text → stored text of 16,000 characters ending in `…[truncated]`; 16,000 exactly is unchanged
**Tests**: integration
**Gate**: full
**Commit**: `fix(connectors): truncate inbound text over the message limit`

#### T20: Chat refresh platform failures answer 502

**What**: a platform error during `POST /connectors/{id}/chats/refresh` answers 502 `unavailable` with a fixed message.
**Where**: `internal/connectors/{chats,manager}.go`, `internal/api/router.go`
**Depends on**: T19
**Requirement**: CON-09
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [ ] Fake ListChats error containing platform text → 502 `unavailable`, message without that text
**Tests**: e2e
**Gate**: full
**Commit**: `fix(connectors): answer 502 when the platform fails a chat refresh`

#### T21: Spec, contract notes and traceability

**What**: TG-02/DC-02 limits in Unicode code points; contract notes for T12-T20; traceability.
**Where**: `.specs/features/connectors/{spec,contract-notes,tasks}.md`
**Depends on**: T20
**Requirement**: TG-02, DC-02
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [ ] Spec, design and contract notes match the behaviour; `make check` passes
**Tests**: none
**Gate**: build
**Commit**: `docs(specs): record connectors fix iteration 1`
