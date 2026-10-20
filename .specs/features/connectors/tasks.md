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
| Full | Integration/e2e tasks | `make test` |
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
- [ ] Missing required → error map `{field: "required"}`; bad url → `{field: "invalid url"}`
- [ ] Masked read never contains secret; PATCH with mask keeps original secret
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
- [ ] Auth matrix owner/use-grantee/stranger/admin; 422 names field; secrets masked in every response
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
- [ ] Fake connector started within 2 s of create; stopped on disable; Send after stop → error
- [ ] Run error containing the secret → status text with `[redacted]`; retries with growing delay (fake clock)
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
- [ ] Code matches `^[A-HJ-NP-Z2-9]{6}$`, expires after 10 min (fake clock), single use
- [ ] ResolveSender by sender id or alt id; after Delete → empty
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
- [ ] Unrouted chat → chat row exists, messages table count unchanged
- [ ] Routed chat in 2 rooms → 2 messages; same external id again → still 2
- [ ] Oversized media → attachment status `too_large`, no blob
- [ ] `/chats/{id}/route` creates private room with chat + agent members
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
- [ ] Agent reply to a chat message reaches the fake connector with `ReplyToID` = original external id
- [ ] Disabled connector → send failure → retry path
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
- [ ] Fixtures (synthetic, no real data) for text, extended text, image with caption, ephemeral wrapper, view-once, edit, revoke, reaction, location, unknown
- [ ] `5541…:12@s.whatsapp.net` → `5541…@s.whatsapp.net`; chat id `<instance>|<jid>`
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
- [ ] Fake evolution server: WS event → Inbound; `/send/text` body includes `quoted` on reply; `/send/media` base64 + type
- [ ] Instance not in allowlist ignored; UA header `partyline/<version>` sent
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
- [ ] Fake Bot API: offset increments; 9,000-char text → 3 ordered calls; png → sendPhoto, pdf → sendDocument
- [ ] Token never appears in errors returned to the manager
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
- [ ] Fake session: bot author ignored; 4,500 chars → 3 sends, first with reference; channels listed
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
- [ ] Valid signed payload → 202 and stored message in routed room
- [ ] Bad signature → 401; timestamp 301 s old → 401; nothing stored
- [ ] Outbound received by httptest with valid signature; 500 response → retry path
**Tests**: e2e
**Gate**: build
**Commit**: `feat(webhook): add signed webhook connector`
