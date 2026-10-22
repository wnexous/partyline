# Intelligences Tasks

## Execution Protocol (MANDATORY -- do not skip)

Implement these tasks with the `tlc-spec-driven` skill: **activate it by name and follow its Execute flow and Critical Rules.** Do not search for skill files by filesystem path. The skill is the source of truth for the full flow (per-task cycle, sub-agent delegation, adequacy review, Verifier, discrimination sensor).

**If the skill cannot be activated, STOP and tell the user - do not proceed without it.**

---

**Design**: `.specs/features/intelligences/design.md`
**Status**: Approved

Spec issues 2, 4, 5 and 6 of the design were confirmed as recommended (2026-10-21); the tasks
implement those fixes.

---

## Test Coverage Matrix

> Generated from codebase, project guidelines, and spec - confirm before Execute. Guidelines found: `AGENTS.md` (tests derive from the spec's ACs; `make check` before every commit; no message text or secrets in logs; synthetic fixtures), every feature's `contract-notes.md`, `.specs/LESSONS.md` (confirmed L-017, L-018, L-019, L-029), `web/vite.config.ts`, `web/playwright.config.ts`, `.specs/features/web-app/conventions.md`, `Makefile`.

| Code Layer | Required Test Type | Coverage Expectation | Location Pattern | Run Command |
| --- | --- | --- | --- | --- |
| Pure rules (`handles`, `avatars`, tree builder, menu key decoder, `delivery.Wants`/prefs) | unit | All branches; 1:1 to the ACs they implement; every listed edge case and boundary (1/64/65 chars, 2 MiB ± 1, width < 40) | `internal/**/*_test.go` | `go test -count=1 ./internal/<pkg>/...` |
| Store, migrations, Go hooks | integration (real SQLite) | Every migrated table/column; v0.3 fixture before/after; rollback on failure | `internal/store/*_test.go` | `go test -count=1 ./internal/store/...` |
| Services (agents, conversations, intelligences, connectors pipeline/sender, delivery, access) | integration (real SQLite, fake connector) | Every AC + error paths; races where the spec names them | `internal/**/*_test.go` | `make test` |
| HTTP/WS routes, channel tools, CLI commands | e2e (testkit, real app on httptest) | Every route/tool/command: happy + edge + error; authorization matrix for every new route (owner, grantee/other user, agent token where relevant); every new sentinel error observed at its HTTP status (L-017); negative tests isolate the guard under test (L-018) | `internal/api/*_test.go`, `internal/channel/*_test.go`, `internal/cli/*_test.go` | `make test` |
| Platform adapters | integration against fakes built from captured real responses | Each new field/call (account label, self id) | `internal/connectors/<kind>/*_test.go` | `make test` |
| TS adapters (`integrations/`) | unit (bun, fake host APIs) | Each changed behaviour (exit code 3, framing) | `integrations/**/*.test.ts` | `make integrations-test` |
| Web components/pages | unit (Vitest + Testing Library) | Each visible AC; a11y test for new layouts | `web/src/**/*.test.{ts,tsx}` | `cd web && bun run test` |
| Web end-to-end | e2e (Playwright, real binary, desktop-1280 + mobile-360) | Messenger smoke: DM, group, contacts, profile, mobile navigation, no horizontal scroll | `web/e2e/*.spec.ts` | `make e2e` |
| Docs | none | build gate only | `README*.md`, `docs/*.md` | `make check` |

## Gate Check Commands

> Generated from codebase - confirm before Execute.

| Gate Level | When to Use | Command |
| --- | --- | --- |
| Quick | While iterating on unit-only code | `go test -count=1 ./internal/<pkg>/...` or `cd web && bun run test` |
| Full | While iterating on integration/e2e code | `make test` |
| Build | Before **every** commit (AGENTS.md, L-029) | `make check` |
| E2E | Tasks that touch the web shell or the Playwright suite (T52) and before the UAT | `make e2e` |

Every task's commit gate is `make check` (lint, vet, generated-code drift, all Go/web/TS tests,
build). `make check` takes about 10 minutes on the devbox; iterate with the quick/full gates.

---

## Execution Plan

Phases run in order; tasks within a phase run in order. Phase sizes target one worker batch
(~7 tasks); Phases 5 and 6 are small and can share one batch, as can Phase 9 after Phase 8.

### Phase 1: Store foundation

```
T1 → T2 → T3 → T4 → T5 → T6
```

### Phase 2: Intelligence core and profiles

```
T7 → T8 → T9 → T10 → T11 → T12
```

### Phase 3: Folder identity and the live session

```
T13 → T14 → T15 → T16 → T17 → T18
```

### Phase 4: Conversations

```
T19 → T20 → T21 → T22 → T23 → T24 → T25
```

### Phase 5: People, contacts and operators

```
T26 → T27 → T28 → T29 → T30 → T31
```

### Phase 6: Account rules, notifications and merge

```
T32 → T33 → T34 → T35
```

### Phase 7: Channel tools and CLI

```
T36 → T37 → T38 → T39 → T40 → T41
```

### Phase 8: Web messenger

```
T42 → T43 → T44 → T45 → T46 → T47 → T48 → T49
```

### Phase 9: Cleanup, docs and UAT

```
T50 → T51 → T52 → T53 → T54
```

---

## Task Breakdown

### Phase 1: Store foundation

#### T1: Single-transaction migration runner with Go hooks

**What**: `Store.Migrate` applies every pending migration file in one write transaction, running each file's optional Go hook (`hooks map[string]func(ctx, *sql.Tx) error`, raw SQL only) right after the file, recording each version, committing once (AD-012). The file system and hooks are injectable for tests.
**Where**: `internal/store/store.go`
**Depends on**: None
**Reuses**: `applyMigration`, `migrationFiles`
**Requirement**: MIG-05
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] Two pending test migrations where the second (or its hook) fails leave `schema_migrations`, tables and rows exactly as before (MIG-05)
- [x] A hook runs after its file and sees the file's schema; a fresh database and an already-migrated one behave as before
- [x] Gate check passes: `make check`
**Tests**: integration
**Gate**: build
**Commit**: `feat(store): apply pending migrations in one transaction`

#### T2: Handle and default-name rules

**What**: leaf package `internal/handles`: `Slug`, `Default(kind, base)`, `Validate`, `Allocate`, `DisplayName` exactly as in the design (accent folding table, `ai-`/`user-`/`contact-` prefixes, reserved words, `-2`, `-3` suffixes, 36-byte base cut, first rune upper-cased).
**Where**: `internal/handles/handles.go`
**Depends on**: T1
**Reuses**: none (leaf)
**Requirement**: PROF-02, AID-03
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] Table tests: `discord` → handle `discord`, display `Discord`; two bases `notes` → `notes`, `notes-2`; `2024-notes` → `ai-2024-notes`; `/` and `~` bases; accents; 40-char boundary (40 ok, 41 refused); uppercase input lower-cased; reserved words refused; must start with a letter
- [x] Gate check passes: `make check`
**Tests**: unit
**Gate**: build
**Commit**: `feat(handles): add handle and default name rules`

#### T3: Intelligences schema (migration 0015)

**What**: `0015_intelligences.sql` with every table, column, index, trigger and the transitional `message_views` from the design; sqlc queries for intelligences, people, chat_people, machines, read cursors and the new room/connector/outbox columns (CRUD only).
**Where**: `internal/store/migrations/0015_intelligences.sql`
**Depends on**: T2
**Reuses**: sqlc config, `openMigratedBefore`
**Requirement**: PROF-01, MIG-01
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] Fresh database: handle unique case-insensitively; `rooms_dm_pair` refuses a second DM for a pair; `kind` CHECKs refuse unknown values
- [x] Deleting a user cascades to its agents, their intelligences and their DM rooms (triggers + FK); deleting a connector deletes its people and their intelligences
- [x] Existing tests stay green (the view keeps v0.3 author names through its fallbacks)
- [x] Gate check passes: `make check`
**Tests**: integration
**Gate**: build
**Commit**: `feat(store): add the intelligences schema`

#### T4: Backfill intelligences, people and authors (0015 hook)

**What**: the Go hook of `0015`: intelligences for users and agents (handles via `internal/handles`, ordered by `created_at`), people from identities and chat senders (alt ids folded, latest name), `messages.author_iid`, `chat_people`, `chats.person_id`.
**Where**: `internal/store/intelligences_backfill.go`
**Depends on**: T3
**Reuses**: `internal/handles`, T1 hooks
**Requirement**: MIG-01, MIG-04
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] Every user and agent has one intelligence with a valid unique handle and the v0.3 display name; agents are unlinked (`folder = ''`)
- [x] One person per (connector, sender id); `author_iid` per author case (user, agent, linked chat sender, unlinked sender, senderless chat, system) (MIG-04)
- [x] Gate check passes: `make check`
**Tests**: integration
**Gate**: build
**Commit**: `feat(store): backfill intelligences and message authors`

#### T5: Convert v0.3 projects (0015)

**What**: the SQL part of `0015` that turns each project room member into a direct member of the project's most recently connected agent (settings copied, existing direct membership wins), copies project cursors into agent cursors (`MAX`), deletes project members, project grants and project rows.
**Where**: `internal/store/migrations/0015_intelligences.sql`
**Depends on**: T4
**Reuses**: v0.3 active-session order (`connected_at DESC`, NULL last, then id)
**Requirement**: MIG-03
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] Project member → direct member of the latest-connected agent with `display_name`, `sign_outbound`, `delivery`, `keywords_json`, `outbound` copied; a direct member already present keeps its own settings; a project without agents loses its membership
- [x] No projects, project members or project grants remain; agents' `project_id` is NULL
- [x] Gate check passes: `make check`
**Tests**: integration
**Gate**: build
**Commit**: `feat(store): convert v0.3 projects into agent members`

#### T6: v0.3 upgrade fixture and production-route test

**What**: the migration test plan of the design (fixture, assertions 1-5) and `testkit.WithDatabase(path)`; an api test that starts the app on the migrated fixture and proves a catch-all route still delivers both ways.
**Where**: `internal/store/intelligences_migration_test.go`
**Depends on**: T5
**Reuses**: `openMigratedBefore`, `testkit`, fake connector
**Requirement**: MIG-01, MIG-05, GRP-01
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] Row counts and every member/grant/cursor/outbox/attachment survive; rooms are groups with `read_baseline` = last seq (MIG-01)
- [x] Forced hook failure leaves the database unchanged (MIG-05)
- [x] Upgraded catch-all room: inbound telegram DM reaches the agent member and the agent's reply is queued to the chat (GRP-01)
- [x] Gate check passes: `make check`
**Tests**: integration
**Gate**: build
**Commit**: `test(store): upgrade a v0.3 database to intelligences`

### Phase 2: Intelligence core and profiles

#### T7: Intelligences service and creation hooks

**What**: `internal/intelligences` service: `Create` called inside the transactions that create users (setup, invites, admin), agents and (later) people; `Get`, `CanSee`, `CanEdit`, `Update` with validation, `handle_taken`, `events.IntelligenceChanged`, `MembersChanged` for the intelligence's rooms.
**Where**: `internal/intelligences/service.go`
**Depends on**: T6
**Reuses**: `internal/handles`, `access.CanUseAgent`, `access.CanUseConnector`, `events.Bus`
**Requirement**: PROF-01, PROF-02, PROF-10
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] Every new user and agent gets exactly one intelligence (PROF-01); handle rules and 409 `handle_taken` (PROF-02, PROF-10); display 1/64/65 and bio 280/281 boundaries
- [x] `CanSee`/`CanEdit` cases of the design's security section (self, other user, AI owner, AI grantee, stranger, person via connector use)
- [x] Gate check passes: `make check`
**Tests**: integration
**Gate**: build
**Commit**: `feat(intelligences): add the intelligence profile service`

#### T8: Avatar processing

**What**: `internal/avatars.Process`: 2 MiB + 1 read limit, sniffed PNG/JPEG/WebP only, `DecodeConfig` bound 4096², center crop, 256×256 CatmullRom, PNG out; `ErrInvalid`. Adds `golang.org/x/image`.
**Where**: `internal/avatars/avatars.go`
**Depends on**: T7
**Reuses**: stdlib `image/*`, `golang.org/x/image/{webp,draw}`
**Requirement**: PROF-06, PROF-07
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] PNG, JPEG and WebP inputs (synthetic test images; a tiny WebP in `testdata`) become 256×256 PNG, centre-cropped (a 400×200 input keeps its middle square) (PROF-07)
- [x] GIF, text, truncated image, 2 MiB + 1 byte and 5000×10 dimensions → `ErrInvalid` (PROF-06)
- [x] Gate check passes: `make check`
**Tests**: unit
**Gate**: build
**Commit**: `feat(avatars): crop and resize avatar uploads`

#### T9: Profile API

**What**: `GET/PATCH /api/v1/intelligences/{id}` (`IntelligenceProfile`, `UpdateProfileRequest`), `Me.intelligence`, `PATCH /me` display name written to the intelligence, users directory with `IntelligenceRef`; DTOs in `dto/intelligences.go`, tygo regenerated; new error codes mapped.
**Where**: `internal/api/intelligences_handlers.go`
**Depends on**: T8
**Reuses**: T7 service, `mapError`, convert helpers
**Requirement**: PROF-03, PROF-05, PROF-10, AID-06
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] A user edits their own profile; an AI's owner edits the AI's profile (rename shows in the AI's profile and the agents list, AID-06); grantee 403, stranger 404; 409 `handle_taken`; 422 per invalid field with nothing applied (L-017 for each status)
- [x] Gate check passes: `make check`
**Tests**: e2e
**Gate**: build
**Commit**: `feat(api): read and edit intelligence profiles`

#### T10: Avatar upload, serving and the agent profile endpoints

**What**: `PUT/DELETE/GET /api/v1/intelligences/{id}/avatar`, `PATCH /api/v1/agent/profile`, `PUT /api/v1/agent/profile/avatar` (the AI's own profile), blob storage, response headers of the design, retention sweep counting avatar references, `avatar_url` with `?v=sha`.
**Where**: `internal/api/avatar_handlers.go`
**Depends on**: T9
**Reuses**: `attachments.Store`, `rooms.Cleanup` sweep callback, T8
**Requirement**: PROF-04, PROF-06, PROF-07
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] Upload → 256×256 PNG served to users who can see the intelligence, 404 to others; headers `nosniff`, CSP, cache (PROF-07)
- [x] Invalid upload → 422 `avatar_invalid` and the old avatar is still served (PROF-06)
- [x] An agent token updates its own AI's display name, handle, bio and avatar (PROF-04 server side); it cannot edit another AI
- [x] A replaced avatar's blob is swept after the grace period; the current one is kept
- [x] Gate check passes: `make check`
**Tests**: e2e
**Gate**: build
**Commit**: `feat(api): upload and serve avatars`

#### T11: Message and member authors as intelligences

**What**: `rooms.insert` writes `author_iid` for user and agent posts; `Message.author` (`IntelligenceRef`), `RoomMember.intelligence`, room owner refs carry handles; convert and tygo.
**Where**: `internal/api/convert/convert.go`
**Depends on**: T10
**Reuses**: `message_views` author columns
**Requirement**: AID-06, PROF-03, GRP-03
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] After renaming a user and an AI, old and new messages in the timeline and the member list show the new display name, handle and avatar (AID-06, PROF-03); the per-room agent display name still wins in its room (GRP-04)
- [x] Chat-authored messages expose `author` when `author_iid` is set (GRP-03 display path)
- [x] Gate check passes: `make check`
**Tests**: e2e
**Gate**: build
**Commit**: `feat(api): name message authors and members by intelligence`

#### T12: Mentions and signatures follow the profile

**What**: delivery prefs names = member display name, intelligence display name and `@handle` (handle boundary rule); agent `name`/`auto_name` dropped from matching; outbound signatures use the renamed display name.
**Where**: `internal/delivery/wants.go`
**Depends on**: T11
**Reuses**: `containsWord`, `connectors.Sender`
**Requirement**: AID-06, PROF-03, GRP-04
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] A mentions-mode AI receives `@newhandle` and `@New Name` after a rename and not `@oldhandle`; `@atlas` does not reach `@atlas-2` (AID-06)
- [x] Outbound to a chat carries the AI's new display name as signature; delivery modes, keywords, broadcast unchanged (GRP-04)
- [x] Gate check passes: `make check`
**Tests**: integration
**Gate**: build
**Commit**: `feat(delivery): match mentions by handle and profile name`

### Phase 3: Folder identity and the live session

#### T13: Resolve AIs by folder

**What**: `Registry.ResolveFolder` (machines upsert, lookup, adoption of unlinked identities, creation with default names, cap 200) and server-side folder validation (`folder_required`, `folder_invalid`), under `allocMu`; audit `agent.adopt`.
**Where**: `internal/agents/folder.go`
**Depends on**: T12
**Reuses**: `allocMu`, `internal/handles`, T7 `Create`
**Requirement**: AID-01, AID-02, AID-03, AID-05, MIG-02
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] (O, M, F) creates once and resolves to the same id from any harness, updating the app (AID-01, AID-02); same folder on two machines → two identities; same base name in two folders → `name`, `name-2` with the same display name (AID-03)
- [x] Validation table: empty, NUL, 1025 bytes, relative, trailing slash, `..` → the right code (AID-05)
- [x] Unlinked identity adopted by handle or by `Adopt`, once; a stale `Adopt` naming a linked or missing identity is ignored (MIG-02); concurrent resolves of one new folder create one identity
- [x] Gate check passes: `make check`
**Tests**: integration
**Gate**: build
**Commit**: `feat(agents): resolve AI identities by machine and folder`

#### T14: Server accepts folder headers

**What**: WebSocket, `agentOnly` REST tools, `/agent/register` and `/agent/pending` resolve by folder headers when `X-Partyline-Folder` is present (legacy name path kept for now); `GET /api/v1/agent/session` and `GET /api/v1/agent/whoami`; header constants in `dto/common.go`; welcome frame carries `intelligence` and `owner`.
**Where**: `internal/api/agent_handlers.go`
**Depends on**: T13
**Reuses**: `Gateway.ServeHTTP` order, `middleware` principal
**Requirement**: AID-02, AID-04, SES-01
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] A tools-only REST call with machine+folder acts as that folder's AI (posts authored by it) and creates it on first sight (AID-04); WebSocket connect with the same folder resolves the same id and sets the app (AID-02)
- [x] `/agent/session` reports exists/online/app/connected_at and creates nothing (SES-01 lookup); `/agent/whoami` returns the owner's intelligence
- [x] Gate check passes: `make check`
**Tests**: e2e
**Gate**: build
**Commit**: `feat(api): identify agent sessions by folder headers`

#### T15: CLI sends the canonical folder

**What**: channel, `channel --tools-only`, `listen` and `hook` compute the canonical folder (`PARTYLINE_FOLDER` > cwd/stdin cwd; Abs → EvalSymlinks → Clean) and home, send machine/folder/home/harness on every dial and call, a per-process live id on the WebSocket, `X-Partyline-Adopt` from `--as`/`PARTYLINE_AGENT`; `procenv` recovers `PARTYLINE_FOLDER`, `PARTYLINE_HARNESS`, `PARTYLINE_TAKEOVER`; no name/project/session headers or session files any more.
**Where**: `internal/channel/identity.go`
**Depends on**: T14
**Reuses**: `channel.Identity.setHeaders`, `procenv.withLauncherEnv`
**Requirement**: AID-07, MIG-02
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] A symlinked working directory and a trailing-slash `PARTYLINE_FOLDER` send the resolved absolute path; headers are percent-encoded (spaces, non-ASCII) and decode server-side to the same folder (AID-07)
- [x] Channel + tools-only + listen + hook started in one folder act as one identity (the v0.3 OpenCode split is gone); `--as old` adopts the unlinked `old` (MIG-02)
- [x] Gate check passes: `make check`
**Tests**: e2e
**Gate**: build
**Commit**: `feat(channel): send the canonical folder with every connection`

#### T16: Require the folder and remove v0.3 naming

**What**: requests without a folder get 422 `folder_required` ("update partyline"), invalid ones `folder_invalid`; delete temporary/fixed allocation, unique mode, `X-Partyline-Agent`/`-Agent-Mode`/`-Project`/`-Session`, session holds, the archive job and their tests (replaced by the folder tests of T13-T15). DTO fields `kind`/`archived`/`project` keep constant values until T51.
**Where**: `internal/agents/identity.go`
**Depends on**: T15
**Reuses**: T13 validation
**Requirement**: AID-05
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] WebSocket, REST tool, register and pending without a folder → 422 `folder_required`; each invalid folder case → 422 `folder_invalid` (AID-05, L-017)
- [x] No reference to session keys, temporary names, archiving or the removed headers remains outside migrations and specs (grep in the commit body)
- [x] Gate check passes: `make check`
**Tests**: e2e
**Gate**: build
**Commit**: `refactor(agents): require a folder and drop session-named agents`

#### T17: One live session per AI

**What**: `Presence.Attach` check-and-swap with live id and takeover; gateway answers 409 `identity_busy` before the upgrade, closes a lost race with 4003, closes the old session with 4002 `taken_over`; `X-Partyline-Takeover`; link states `Busy` (3 retries) and `TakenOver` (no retry), takeover sent on the first successful dial only; `replaced`/4000 removed.
**Where**: `internal/agents/presence.go`
**Depends on**: T16
**Reuses**: `Gateway.ServeHTTP` pre-upgrade error path, `link.run` state machine
**Requirement**: SES-02, SES-04, SES-06, SES-08
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] Second connection without takeover → 409 `identity_busy` with name/app/since, first stays live (SES-04); with takeover → old closed `taken_over`, new live (SES-02); same live id reconnecting is accepted silently
- [x] Two simultaneous takeovers → exactly one live, the other closed `taken_over` (spec edge case)
- [x] Closing the live socket → offline event within 5 s and the next connection needs no takeover (SES-06); messages posted while offline arrive as backlog with the 50 cap and notice (SES-08)
- [x] Gate check passes: `make check`
**Tests**: e2e
**Gate**: build
**Commit**: `feat(agents): keep one live session per AI with explicit takeover`

#### T18: Busy and taken-over sessions explain themselves

**What**: every channel tool answers the busy text (SES-04) or the taken-over text; the taken-over channel sends the SES-05 notice once and delivers nothing more; `listen` prints the notice JSON line and exits 3; the pi/OpenCode/omp adapters stop restarting on exit code 3 and show the notice.
**Where**: `internal/channel/listen.go`
**Depends on**: T17
**Reuses**: `guardFor`, `FormatNotice`, `superviseListen` (TS)
**Requirement**: SES-04, SES-05
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] Busy channel: `reply`, `send_message`, `history` … answer the exact busy text with the harness name and HH:MM (SES-04)
- [x] Taken over: exactly one `This intelligence was opened in another session; Partyline is disconnected here.` notification, no further deliveries, tools answer the taken-over text (SES-05)
- [x] `listen` exit code 3 + notice line; bun tests: adapters do not restart on 3 and still restart on other codes
- [x] Gate check passes: `make check`
**Tests**: e2e
**Gate**: build
**Commit**: `feat(channel): report busy and taken-over sessions`

### Phase 4: Conversations

#### T19: Access for DMs and group members

**What**: `access.UserRoomLevel` rules of the design (DM participants post, AI owner/use grantee view, group user members post, admins nothing); `GrantedRoomLevels` includes member groups and the user's DMs; `SetVisibility` on a DM → 422 `dm_no_sharing`.
**Where**: `internal/access/access.go`
**Depends on**: T18
**Reuses**: `level`, `grantedLevels`, `CanUseAgent`
**Requirement**: DM-08
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] Matrix: participant post; AI owner and `use` grantee view; another user, an admin and a `*` grant none; group user member post; disabled user none (DM-08)
- [x] Sharing a DM → 422 `dm_no_sharing` with nothing written (DM-08)
- [x] Gate check passes: `make check`
**Tests**: integration
**Gate**: build
**Commit**: `feat(access): give DM participants and group members access`

#### T20: Direct messages

**What**: `conversations.OpenDM` (pair key, race-safe insert, owner rules, rate limit hook for AIs), `POST /api/v1/conversations/dm`, `Room.kind` and `participants`, DM guards in `rooms` (`dm_members_fixed`, no manage, `counterpart_disabled`), AI deletion removes its DMs.
**Where**: `internal/conversations/dm.go`
**Depends on**: T19
**Reuses**: `rooms` create/insert, T19 access
**Requirement**: DM-01, DM-02, DM-08
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] Opening a DM twice (also concurrently) returns the same room; 201 then 200 (DM-01); self → 422 `dm_self`; invisible target → 404 `contact_not_found`
- [x] Add/remove/update member on a DM → 422 `dm_members_fixed`; PATCH/DELETE room 403; disabled counterpart → readable, post 403 `counterpart_disabled` (DM-02)
- [x] Deleting the AI deletes its DMs and memberships (DM-08 edge case)
- [x] Gate check passes: `make check`
**Tests**: e2e
**Gate**: build
**Commit**: `feat(conversations): open one direct message per pair`

#### T21: Groups with members

**What**: `conversations.CreateGroup` (all-or-nothing member checks with the owner's add-member rules, 50 members max) and `POST /api/v1/groups`.
**Where**: `internal/conversations/groups.go`
**Depends on**: T20
**Reuses**: `rooms.checkUse`, `rooms.Create` defaults
**Requirement**: LAY-04
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] Group created with the user as owner and every chosen user and AI as member; the members see it (LAY-04)
- [x] One AI the user may not use → 422 `members_refused` listing it, nothing created
- [x] Gate check passes: `make check`
**Tests**: e2e
**Gate**: build
**Commit**: `feat(conversations): create groups with members`

#### T22: Read state and the conversation list

**What**: `read_cursors`, `PUT /api/v1/rooms/{id}/read`, `GET /api/v1/conversations` (groups + own DMs, last message preview, unread with `read_baseline`, newest first, member names), live `room.read` frame to the reader.
**Where**: `internal/conversations/list.go`
**Depends on**: T21
**Reuses**: `ListRoomsForUser`, `events`, live hub `render`
**Requirement**: DM-06, DM-07, LAY-01
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] List orders DMs and groups by newest activity with per-user unread counts that exclude the user's own messages and the pre-upgrade baseline (DM-06)
- [x] Marking read sets unread 0 for that user only, never moves backwards, and emits `room.read` to that user's sockets (DM-07)
- [x] Preview: 80 code points, sender name in groups, `[file name]` for attachments only (LAY-01 data)
- [x] Gate check passes: `make check`
**Tests**: e2e
**Gate**: build
**Commit**: `feat(conversations): list conversations with unread counts`

#### T23: AIs receive and see DMs

**What**: `AgentRoom.kind`/`with`; notification meta `conversation`, `author_id`, `author_handle`, DM `room_name`; `channel.Frame` + `integrations/testdata/framing.json` parity; DM rooms pass `all`/`mentions` delivery; `list_rooms` includes DMs; `resolveRoom` accepts `@handle`.
**Where**: `internal/channel/format.go`
**Depends on**: T22
**Reuses**: dispatcher, `Wants`, framing shared cases
**Requirement**: DM-03, GRP-04
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [ ] A user's DM to a live AI arrives with `conversation="dm"`, the sender's name and handle; the AI's `reply` appears in the DM (DM-03)
- [ ] Group deliveries carry `conversation="group"` and keep delivery modes, keywords and signatures (GRP-04); Go and TS framing parity tests pass
- [ ] Gate check passes: `make check`
**Tests**: e2e
**Gate**: build
**Commit**: `feat(channel): deliver direct messages to AIs`

#### T24: AI-initiated DMs and groups

**What**: `POST /api/v1/agent/dms` (`to` = `@handle`, handle or id; visibility for the AI's owner) and `POST /api/v1/agent/groups` (owner's add-member rules, all-or-nothing), `AllowCreate` (10 new conversations per hour per AI).
**Where**: `internal/api/agent_conversations_handlers.go`
**Depends on**: T23
**Reuses**: T20 `OpenDM`, T21 `CreateGroup`, `agents/limit.go`
**Requirement**: DM-04, AGRP-01, AGRP-02
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [ ] AI posts `to: @owner-handle` with no prior conversation → DM created and visible in the owner's conversation list (DM-04); invisible target → 404 `contact_not_found`
- [ ] AI creates a group with its owner and another visible AI → owned by the owner, AI is a member (AGRP-01); a refused member → 422 listing it, nothing created (AGRP-02)
- [ ] 11th new conversation within an hour → 429 `rate_limited`; reopening an existing DM is not counted
- [ ] Gate check passes: `make check`
**Tests**: e2e
**Gate**: build
**Commit**: `feat(api): let AIs open DMs and create groups`

#### T25: Machine › folder tree builder

**What**: pure `intelligences.BuildTree(home, ais)` and the machine sort, exactly as the design's algorithm and examples.
**Where**: `internal/intelligences/tree.go`
**Depends on**: T24
**Reuses**: none
**Requirement**: TREE-01, TREE-02, TREE-03
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] `~/a`, `~/a/b`, `~/c/d/e` → `a` with child `b`, and `c/d › e` (TREE-01, TREE-02); home folder → `~`; outside home → `/srv › api`; `/` → `/`
- [x] Machines with an online AI first, then by label; siblings by label, case-insensitive (TREE-03)
- [x] Gate check passes: `make check`
**Tests**: unit
**Gate**: build
**Commit**: `feat(intelligences): build the machine and folder tree`

### Phase 5: People, contacts and operators

#### T26: People from inbound messages

**What**: `People.Seen` in the inbound pipeline (find by id/alt id, create person + intelligence, platform name update unless locked, `chat_people`, `chats.person_id`, throttled `last_seen_at`); `PostFromChat` writes `author_iid` (linked user, else person).
**Where**: `internal/connectors/people.go`
**Depends on**: T25
**Reuses**: `Pipeline.handle`, `identities.ResolveSender`, T7 `Create`
**Requirement**: PEOP-01, PEOP-02, PEOP-05, PEOP-06, GRP-03
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [ ] New sender → one person intelligence per (connector, external id) with the platform name; the same sender in two chats is one person (PEOP-01)
- [ ] Linked sender's messages are authored by the user's intelligence (PEOP-02); trusted flags unchanged for every author case (PEOP-06)
- [ ] Platform rename updates the person on the next message; after a user rename it does not (PEOP-05); timeline shows the person's name with the platform (GRP-03)
- [ ] Gate check passes: `make check`
**Tests**: integration
**Gate**: build
**Commit**: `feat(connectors): record people who write through connectors`

#### T27: People visibility, person profiles and group participants

**What**: `CanSee`/`CanEdit` for people (connector owner/use, shared conversations), person profile (`PersonDetail`, apps, conversations), person edits lock the name, `GET /rooms/{id}/members` adds `people` per chat member.
**Where**: `internal/intelligences/people.go`
**Depends on**: T26
**Reuses**: T7 service, T9 handlers
**Requirement**: PEOP-03, PEOP-04, PROF-11, GRP-02
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [ ] Connector owner and `use` grantee see and edit the person; a user without connector access gets 404 (PEOP-04)
- [ ] Person page lists name, platform account and only the conversations the caller can view (PEOP-03)
- [ ] A user's rename survives later platform name changes (PROF-11)
- [ ] Group members list shows each chat with its people and platform (GRP-02)
- [ ] Gate check passes: `make check`
**Tests**: e2e
**Gate**: build
**Commit**: `feat(intelligences): show people and their conversations`

#### T28: Contacts API

**What**: `GET /api/v1/contacts` (me, machine trees, unlinked, shared AIs, users, people groups), `GET /api/v1/contacts/people` (paged, search), `PUT /api/v1/machines/{hostname}` label, `GET /api/v1/agent/contacts/tree`.
**Where**: `internal/api/contacts_handlers.go`
**Depends on**: T27
**Reuses**: T25 `BuildTree`, presence
**Requirement**: CONT-01, TREE-03, TREE-04, TREE-06
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [ ] Contacts lists own AIs as trees per machine, AIs shared with the caller, other users and people per usable connector (CONT-01); rows carry online, app and last seen (TREE-03)
- [ ] Machine label 1-64 replaces the hostname, `''` resets, other owner's machine 404 (TREE-04); legacy AIs appear under `unlinked` (TREE-06)
- [ ] Gate check passes: `make check`
**Tests**: e2e
**Gate**: build
**Commit**: `feat(api): list contacts and the machine tree`

#### T29: Connector operators and account labels

**What**: `operator_id` on `PATCH /connectors/{id}` (owner only; owner or owned AI), audit `connector.operator`, `Connector.operator`/`account_label`; `Handler.Account` reported by telegram (`getMe`), discord (bot user) and whatsapp (masked numbers); operator profile `apps` lists the connector.
**Where**: `internal/connectors/service.go`
**Depends on**: T28
**Reuses**: `connectors.Service.Update`, `audit.Record`, `manager.persist`
**Requirement**: ACC-01, ACC-07
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [ ] Owner sets an owned AI as operator → audit entry with from/to (ACC-01); grantee 403; another owner's AI 422; deleting the AI falls back to the owner
- [ ] Adapter tests (fakes from captured responses) report the account label; the operator's profile shows `Discord: @bot` (ACC-07)
- [ ] Gate check passes: `make check`
**Tests**: integration
**Gate**: build
**Commit**: `feat(connectors): give each connector an operator`

#### T30: Platform DMs reach the operator

**What**: `EnsurePlatformDM` in `autoRoute` for `dm` chats when the scope includes DMs and there is no catch-all room; re-home on operator change; no self-DM; `OpenDM` to a person only through a DM whose connector has the caller as operator (`no_route_to_person`).
**Where**: `internal/connectors/inbound.go`
**Depends on**: T29
**Reuses**: T20 `OpenDM`, `autoRoute` caps
**Requirement**: ACC-02, ACC-06, DM-05
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [ ] A person's platform DM to a connector operated by an AI arrives in the DM between the person and the AI, created once (ACC-02); scope `off` routes nothing; a catch-all connector keeps the v0.3 behaviour
- [ ] After an operator change, the next message goes to the new operator's DM; a deleted operator AI → the owner (spec edge case)
- [ ] The operator AI `send_message to:` the person goes out through that chat; another AI gets 422 `no_route_to_person` (DM-05)
- [ ] Gate check passes: `make check`
**Tests**: e2e
**Gate**: build
**Commit**: `feat(connectors): route platform DMs to the operator`

#### T31: New group chats become groups

**What**: `autoRoute` for `group`/`channel` chats when the scope includes groups and there is no catch-all room: a new group (chat title, connector owner, broadcast on) with the operator and the chat as members.
**Where**: `internal/connectors/autoroute.go`
**Depends on**: T30
**Reuses**: `roomName`, `rooms` create, `autoRoute` caps
**Requirement**: ACC-06
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [ ] Scope `groups`/`all`: first message of a new group chat creates one group with the operator (AI member or user) and the bridged chat; the operator AI receives it and its reply goes back (ACC-06)
- [ ] Scope `dms` leaves group chats unrouted; the 60/hour cap still applies
- [ ] Gate check passes: `make check`
**Tests**: integration
**Gate**: build
**Commit**: `feat(connectors): turn new group chats into groups`

### Phase 6: Account rules, notifications and merge

#### T32: Operator signatures

**What**: `Sender.Send` drops the author line when the author intelligence is the chat connector's operator; otherwise the existing rules.
**Where**: `internal/connectors/sender.go`
**Depends on**: T31
**Reuses**: `authorMember`, `AuthorLine`
**Requirement**: ACC-03, ACC-04
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [ ] AI operator replying through its own account → no signature (ACC-03); an AI through a user-operated connector → signature per `sign_outbound` (on and off cases) (ACC-04); a user operator from the web through their own account → no author line
- [ ] Gate check passes: `make check`
**Tests**: integration
**Gate**: build
**Commit**: `feat(connectors): sign only messages written through someone else's account`

#### T33: Messages from the operator's own account

**What**: `FromSelf` messages are held until the chat has no `sending` outbox row (≤10 s, injected clock), dropped when they echo an outbox external id, else posted into the chat's existing rooms authored by the operator (`author_iid` operator, `author_user_id` connector owner); Discord marks its own bot messages `FromSelf`.
**Where**: `internal/connectors/selfmsg.go`
**Depends on**: T32
**Reuses**: `Pipeline.handle`, `GetSentExternalID`, fake connector
**Requirement**: ACC-05
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [ ] A phone-typed message (FromSelf, unknown id) in a routed chat is authored by the operator and trusted for the owner's AIs (ACC-05)
- [ ] The echo of a Partyline send, also when it arrives before the outbox row is marked sent, is dropped; FromSelf in an unrouted chat creates nothing
- [ ] Gate check passes: `make check`
**Tests**: integration
**Gate**: build
**Commit**: `feat(connectors): author own-account messages as the operator`

#### T34: Notify me on another app

**What**: `GET /api/v1/me/notify-options`, `PUT /api/v1/me/notify`, outbox rows `purpose notify` when an AI posts in a DM with that user, `ListDueOutbox` without the membership filter for them, sender format `<AI name>: <text>` + attachment links, usability check at send time.
**Where**: `internal/conversations/notify.go`
**Depends on**: T33
**Reuses**: outbox worker, `Sender`, identities
**Requirement**: NOTI-01, NOTI-02, NOTI-03
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [ ] Options list only the caller's linked accounts with a DM chat on usable connectors; choosing another chat → 422; `null` clears (NOTI-01)
- [ ] AI DM to the user → `<AI name>: <text>` (plus attachment links) sent to that chat, outbox marked sent (NOTI-02); a user's own DM posts are not copied
- [ ] Connector down → retries with backoff, `failed` after 5, the DM still readable (NOTI-03)
- [ ] Gate check passes: `make check`
**Tests**: e2e
**Gate**: build
**Commit**: `feat(conversations): copy AI DMs to the user's chosen app`

#### T35: Merge identities

**What**: `conversations.Merge` and `POST /api/v1/agents/{id}/merge` per the design (memberships, cursors, grants, DMs incl. appended history, authored messages, operated connectors, delete, audit `agent.merge`, events).
**Where**: `internal/conversations/merge.go`
**Depends on**: T34
**Reuses**: T20 DM keys, `presence.Online`
**Requirement**: MRG-01, MRG-02
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [ ] Unlinked A merged into folder AI B: groups (B's settings win), cursors (max), grants, DMs (re-keyed or appended after the notice), authored messages and operators move to B; A is gone; audit written (MRG-01)
- [ ] A online → 409 `identity_online`, nothing changed (MRG-02); another owner's AI 404; A = B 422
- [ ] Gate check passes: `make check`
**Tests**: e2e
**Gate**: build
**Commit**: `feat(conversations): merge one AI identity into another`

### Phase 7: Channel tools and CLI

#### T36: Takeover prompt in the launcher

**What**: launcher flags `--take-over`, `--as` (`--name` alias, `--project` ignored with a notice), session lookup with a 2 s timeout, prompt on stdin+stderr terminals for claude/opencode/pi/omp, informational line for codex/gemini, environment (`PARTYLINE_FOLDER`, `PARTYLINE_HARNESS`, `PARTYLINE_TAKEOVER`, `PARTYLINE_AGENT`); `stdio.isTerminal` injection.
**Where**: `internal/cli/launcher.go`
**Depends on**: T35
**Reuses**: `launch`, `execProgram`, `cliconfig.Resolve`
**Requirement**: SES-01, SES-02, SES-03, SES-07
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [ ] Live identity + terminal → exact prompt text with display name, app and HH:MM (SES-01); `y` → harness exec'd with `PARTYLINE_TAKEOVER=1` and the old session closed `taken_over` (SES-02); `n`/enter/EOF → exit 1, harness not started (SES-03)
- [ ] No terminal → no prompt, started without takeover unless `--take-over` or `PARTYLINE_TAKEOVER=1` (SES-07); server unreachable → started without asking
- [ ] Gate check passes: `make check`
**Tests**: e2e
**Gate**: build
**Commit**: `feat(cli): ask before taking over a live session`

#### T37: Conversation and profile tools for AIs

**What**: `send_message` with `room` or `to`; new `create_group` and `update_profile` (avatar file read locally, ≤2 MiB); first-welcome identity notice; MCP instructions describe DMs, `to`, groups and profiles.
**Where**: `internal/channel/tools.go`
**Depends on**: T36
**Reuses**: T24 endpoints, T10 agent profile endpoints
**Requirement**: DM-04, PROF-04, AGRP-01, AGRP-02
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [ ] `send_message to:"@owner"` creates the DM and posts (DM-04); both `room` and `to`, or neither → tool error
- [ ] `update_profile` renames and sets an avatar from a local PNG; returns the profile; invalid file → `avatar_invalid` (PROF-04)
- [ ] `create_group` returns the group, or the refused members with nothing created (AGRP-01, AGRP-02)
- [ ] Gate check passes: `make check`
**Tests**: e2e
**Gate**: build
**Commit**: `feat(channel): add DM, group and profile tools`

#### T38: partyline login

**What**: `partyline login [--url] [--token-stdin]`: URL resolution, reachability, device flow (reused) or stdin token, save config, `GET /agent/whoami`, the CLI-01 line; on a terminal with no registered harness opens the setup menu (wired in T40).
**Where**: `internal/cli/login.go`
**Depends on**: T37
**Reuses**: `setupCredentials`, `approveInBrowser`, `cliconfig.Save`
**Requirement**: CLI-01
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [ ] Device approval and `--token-stdin` both save URL+token (0600) and print `Logged in as @<handle> on <instance> (machine <hostname>)` (CLI-01); denied/expired approvals exit 1 with the existing messages
- [ ] Gate check passes: `make check`
**Tests**: e2e
**Gate**: build
**Commit**: `feat(cli): add partyline login`

#### T39: Checklist menu

**What**: `cli.Menu` (raw byte key decoder, render with ANSI redraw, truncation to width, cursor hide/restore) driven by injected reader/writer/width; `golang.org/x/term` behind `stdio.rawTerm`.
**Where**: `internal/cli/menu.go`
**Depends on**: T38
**Reuses**: `stdio` injection
**Requirement**: CLI-03
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] Arrows and j/k move, space toggles, `a` selects all, enter applies, `q` and a lone ESC cancel without changes, unknown keys ignored (CLI-03 + edge case)
- [x] Width 30 renders one harness per line with truncated titles (edge case); rows show installed/registered state and pre-selection
- [x] Gate check passes: `make check`
**Tests**: unit
**Gate**: build
**Commit**: `feat(cli): add a checklist menu for the terminal`

#### T40: partyline setup as a checklist

**What**: setup flow: not logged in → `Run partyline login first` exit 1; credential flags run login first (`--browser` too); terminal → menu with detected unregistered pre-selected; apply registers/removes and prints one line per harness; non-terminal flags `--harness`, `--all-detected`, `--remove` (`--yes` accepted), else exit 2 with the hint; `login` opens the menu when nothing is registered.
**Where**: `internal/cli/setup.go`
**Depends on**: T39
**Reuses**: `harness.Registrar` (`Detected`, `Entries`, `Apply`, `Remove`), T38, T39
**Requirement**: CLI-02, CLI-04, CLI-05, CLI-06, CLI-07
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [ ] Menu apply: selected registered, deselected registered removed, untouched ones unchanged (fake runner records exactly those calls), one result line each (CLI-04)
- [ ] No terminal without flags → exit 2 + hint; with each flag → the expected registrar calls (CLI-05); not logged in → message + exit 1 (CLI-06)
- [ ] `login` on a terminal with nothing registered opens the menu (CLI-02); `setup --browser` approves then shows the menu; `--token-stdin --harness claude --yes` still works (CLI-07)
- [ ] Gate check passes: `make check`
**Tests**: e2e
**Gate**: build
**Commit**: `feat(cli): pick harnesses from a checklist in setup`

#### T41: partyline agents prints the tree

**What**: `partyline agents` reads `/agent/contacts/tree` and prints the design's format (machines, levels, markers, columns, Unlinked, empty message).
**Where**: `internal/cli/agents.go`
**Depends on**: T40
**Reuses**: `tabwriter`, T28 endpoint
**Requirement**: TREE-07
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [ ] Golden output for two machines with nested and collapsed folders, online markers, a labelled machine and an unlinked AI (TREE-07); empty list message
- [ ] Gate check passes: `make check`
**Tests**: e2e
**Gate**: build
**Commit**: `feat(cli): print AIs as a machine and folder tree`

### Phase 8: Web messenger

#### T42: Web data layer for conversations, contacts and profiles

**What**: query keys and hooks (conversations, contacts, people, profile, avatar upload/remove, groups, DM open, read, machine label, merge, notify, operator), live handlers (`room.message` → conversations order/preview/unread, `room.read`, `intelligence.changed` patches timelines/members/contacts), fixtures `makeIntelligence`, `makeConversation`, `makeContacts`.
**Where**: `web/src/lib/api/queries.ts`
**Depends on**: T41
**Reuses**: `api`, `queryKeys`, `handleLiveEvent`, `catchUp`
**Requirement**: LAY-02, DM-06
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [ ] Handler tests: a live message moves its conversation to the top and increments unread unless open or own; `room.read` zeroes it; unknown room triggers a refetch (LAY-02, DM-06)
- [ ] Gate check passes: `make check`
**Tests**: unit
**Gate**: build
**Commit**: `feat(web): add the conversations and contacts data layer`

#### T43: Avatar, kind badge and name components

**What**: `IntelligenceAvatar` (image or initials on an id-derived token colour), `KindBadge` (AI, platform, You), `IntelligenceName` (name + badge, @handle tooltip/detail), i18n keys.
**Where**: `web/src/components/intelligence/intelligence-avatar.tsx`
**Depends on**: T42
**Reuses**: `ui/avatar`, `ui/badge`, `ui/tooltip`
**Requirement**: PROF-08, PROF-09
**Tools**: MCP: NONE; Skill: `web-design-guidelines`
**Done when**:
- [x] No avatar → initials with a colour stable per id (PROF-08); avatar → image with alt text
- [x] An AI named like a human still shows the AI badge and @handle; people show their platform (PROF-09)
- [x] Gate check passes: `make check`
**Tests**: unit
**Gate**: build
**Commit**: `feat(web): show avatars, kind badges and handles`

#### T44: Messenger shell and conversation list

**What**: `_app.tsx` two-column layout, conversation sidebar (avatar, title, preview, time, unread, newest first), header menu with every former nav destination, mobile: `/` = list, other routes full screen with back; overview/rooms/agents routes redirect; a11y and e2e page lists updated.
**Where**: `web/src/routes/_app.tsx`
**Depends on**: T43
**Reuses**: `ui/sidebar` primitives, `NAV_GROUPS`, `CommandMenu`
**Requirement**: LAY-01, LAY-02, LAY-05, LAY-06, DM-06
**Tools**: MCP: NONE; Skill: `impeccable`
**Done when**:
- [ ] Sidebar lists DMs and groups with avatar, name, 80-char preview (sender prefix in groups), time and unread, newest first (LAY-01, DM-06); a live message reorders and badges it (LAY-02)
- [ ] Below 768 px the list is the first screen and a conversation opens full screen with a back button (LAY-05); Contacts, Connections, Settings and admin pages are reachable from the header menu (LAY-06)
- [ ] Gate check passes: `make check`
**Tests**: unit
**Gate**: build
**Commit**: `feat(web): switch to the messenger layout`

#### T45: New conversation, new group and search

**What**: sidebar search over titles and member names; "New conversation" dialog (contacts picker → open DM → navigate); "New group" dialog (name + users/AIs → create, refused members shown); the room member picker drops projects and lists intelligences.
**Where**: `web/src/components/new-group-dialog.tsx`
**Depends on**: T44
**Reuses**: `ui/command`, `ui/dialog`, form helpers
**Requirement**: LAY-03, LAY-04, DM-01
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [ ] Search filters by conversation name and member (LAY-03); picking a contact opens its existing DM or a new one (DM-01)
- [ ] Creating a group navigates to it; a refused member is listed in the dialog (LAY-04)
- [ ] Gate check passes: `make check`
**Tests**: unit
**Gate**: build
**Commit**: `feat(web): start DMs and groups from the sidebar`

#### T46: Conversation page

**What**: header (avatar, title, badge, DM counterpart handle/app, back on mobile); mark read on open and on new messages while visible; timeline author avatars and names; participants panel with intelligences and people per chat; DM hides member management.
**Where**: `web/src/routes/_app/rooms/$roomId.tsx`
**Depends on**: T45
**Reuses**: `MessageTimeline`, `MessageItem`, `MemberList`
**Requirement**: DM-07, GRP-02, PROF-09, DM-02
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [ ] Opening a conversation sends the read call with the newest seq (DM-07); authors show avatar, badge and handle (PROF-09)
- [ ] Group participants show AIs, users and each chat's people with platform badges (GRP-02); a DM shows its two participants and no add/remove controls (DM-02)
- [ ] Gate check passes: `make check`
**Tests**: unit
**Gate**: build
**Commit**: `feat(web): show conversations with participants and read state`

#### T47: Contacts page

**What**: `/contacts`: My AIs (machine groups, tree with `Path ›` labels, online/app/last seen, inline machine rename, filter keeping ancestors), Unlinked (merge into… / delete), Shared with me, People (per connector, paged search), Users; Message action; the old agents page is removed.
**Where**: `web/src/routes/_app/contacts.index.tsx`
**Depends on**: T46
**Reuses**: `ui/alert-dialog`, `StatusDot`, T43 components
**Requirement**: CONT-01, TREE-01, TREE-02, TREE-03, TREE-04, TREE-05, TREE-06, MRG-01
**Tools**: MCP: NONE; Skill: `impeccable`
**Done when**:
- [ ] Two machines render as two groups with `b` nested under `a` and `c/d › e` collapsed, `~` for home (TREE-01, TREE-02); rows show name, online, app, last seen; online machines first (TREE-03)
- [ ] Machine rename replaces the hostname (TREE-04); filter by name/handle/folder/machine keeps ancestors (TREE-05); Unlinked offers merge and delete (TREE-06, MRG-01)
- [ ] Shared AIs, users and people sections render from the contacts data (CONT-01)
- [ ] Gate check passes: `make check`
**Tests**: unit
**Gate**: build
**Commit**: `feat(web): add the contacts page with the machine tree`

#### T48: Profile page and editor

**What**: `/contacts/$id`: profile header, editor when `can_edit` (display name, handle, bio, avatar upload/remove with preview and field errors), AI details, apps (operated connectors), conversations list.
**Where**: `web/src/routes/_app/contacts.$id.tsx`
**Depends on**: T47
**Reuses**: form helpers (`TextField`, `applyFieldErrors`), T43
**Requirement**: PROF-03, PROF-05, PROF-11, PEOP-03, ACC-07
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [ ] Owner edits an AI (PROF-05) and connector users edit a person (PROF-11); `handle_taken` and `avatar_invalid` show as field errors; non-editors see no editor
- [ ] Person page shows name, platform account and visible conversations (PEOP-03); an operator AI lists `Discord: @bot` (ACC-07)
- [ ] Gate check passes: `make check`
**Tests**: unit
**Gate**: build
**Commit**: `feat(web): view and edit intelligence profiles`

#### T49: Own profile, notifications and connector operator

**What**: Settings: own profile editor (display name, handle, bio, avatar) and "Notify me here"; connector page: operator select (owner + owned AIs) and the legacy catch-all notice with "Switch to per-chat conversations".
**Where**: `web/src/routes/_app/settings.tsx`
**Depends on**: T48
**Reuses**: `SettingsPage` sections, `ConnectorSettingsSheet`
**Requirement**: PROF-03, NOTI-01, ACC-01
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [ ] Own profile changes show in the sidebar header right away (PROF-03); notify select lists the options and saves/clears (NOTI-01)
- [ ] Connector owner picks an operator; grantees see it read-only (ACC-01)
- [ ] Gate check passes: `make check`
**Tests**: unit
**Gate**: build
**Commit**: `feat(web): edit your profile, notifications and connector operators`

### Phase 9: Cleanup, docs and UAT

#### T50: Remove v0.3 projects

**What**: delete projects API (`/projects*`), `access.KindProject`/`ProjectLevel`, `rooms` project member kind (422 `kind`), delivery project targets/cursors/backlog, `Presence.projectRoomsChanged`, sender project fallback, live project grants, project queries; their tests are replaced by the T5 conversion tests.
**Where**: `internal/agents/projects.go`
**Depends on**: T49
**Reuses**: none
**Requirement**: MIG-03
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [ ] `/api/v1/projects` 404; adding a `project` member → 422 `kind`; no project code paths remain (grep in the commit body) (MIG-03)
- [ ] Gate check passes: `make check`
**Tests**: e2e
**Gate**: build
**Commit**: `refactor: remove v0.3 projects`

#### T51: Drop the legacy identity columns (migration 0016)

**What**: `0016_drop_v03_identity.sql` per the design; `message_views` without fallbacks; every query reading `users.display_name` reads the intelligence; DTO legacy fields (`name`, `project`, `kind`, `archived`, agent `display_name`, `Project`) removed; tygo; fixture test assertion 6.
**Where**: `internal/store/migrations/0016_drop_v03_identity.sql`
**Depends on**: T50
**Reuses**: T6 fixture
**Requirement**: MIG-01, MIG-05, GRP-01
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [ ] v0.3 fixture upgrades through 0015 and 0016 in one transaction; author names equal the pre-upgrade ones; a failure in 0016 leaves the v0.3 database untouched (MIG-05)
- [ ] The production-route test of T6 still passes (MIG-01)
- [ ] Gate check passes: `make check`
**Tests**: integration
**Gate**: build
**Commit**: `refactor(store): drop the v0.3 identity columns`

#### T52: Messenger smoke tests

**What**: Playwright: messenger desktop and mobile (list → conversation → back), DM with an AI via a fake agent client, group creation, contacts tree, profile avatar upload; no-horizontal-scroll list includes `/contacts` and a profile; old project/agents specs replaced.
**Where**: `web/e2e/smoke.spec.ts`
**Depends on**: T51
**Reuses**: `e2e/server.ts`, `e2e/fixtures.ts`
**Requirement**: LAY-01, LAY-05, DM-01, PROF-03, TREE-01
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [ ] `make e2e` passes on desktop-1280 and mobile-360
- [ ] Gate check passes: `make check`
**Tests**: e2e
**Gate**: build
**Commit**: `test(web): cover the messenger in the smoke suite`

#### T53: Docs

**What**: README (features, quickstart with `partyline login` and `setup`), README.pt-BR, `docs/agents.md` (identity per folder, takeover, `--as`, tools, contacts, operators, notify, upgrade note: update the CLI with the server), `docs/claude-code.md`.
**Where**: `docs/agents.md`
**Depends on**: T52
**Reuses**: existing docs structure
**Requirement**: CLI-01, SES-01, AID-01
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [ ] Every command, flag, header-free behaviour and error code users meet is documented in English and Portuguese READMEs; no private data
- [ ] Gate check passes: `make check`
**Tests**: none
**Gate**: build
**Commit**: `docs: describe intelligences, DMs and the new login and setup`

#### T54: Real Claude Code UAT

**What**: on this devbox against a local `partyline serve` (loopback, temporary data dir, admin via `POST /setup`, agent token kept in a 0600 file), temporary HOMEs that start with only a copy of the Claude Code credentials file, `partyline login --token-stdin` + `partyline setup --harness claude` in each, sessions in Orca terminals with a clean environment; record `uat.md` (no secrets, no private data).
**Where**: `.specs/features/intelligences/uat.md`
**Depends on**: T53
**Reuses**: agent-identity UAT procedure (`uat.md`), Orca terminals
**Requirement**: AID-01, AID-02, SES-01, SES-02, SES-03, SES-05, DM-03, DM-04, PROF-04, AGRP-01, TREE-07, CLI-01, CLI-04
**Tools**: MCP: NONE; Skill: `orca-cli`
**Done when**:
- [ ] Recorded pass/fail with observations for: session in folder `atlas` → AI "Atlas"; exit and resume (`partyline claude -c`) → same identity (AID-01, AID-02); second session in `atlas` → prompt, `n` exits 1, `y` takes over and the first shows the notice (SES-01..03, SES-05); web DM "oi" → `conversation="dm"` and a reply in the DM (DM-03); the AI `send_message to:` the user → DM at the top of the sidebar (DM-04); `update_profile` rename + avatar (PROF-04); `create_group` (AGRP-01); `partyline agents` shows `atlas` and a nested `atlas/notes` session (TREE-07); login/setup output (CLI-01, CLI-04); server log has no message text, tokens or errors
- [ ] Failures become fix tasks in a new phase
- [ ] Gate check passes: `make check`
**Tests**: e2e
**Gate**: build
**Commit**: `docs(specs): record the intelligences UAT`

---

## Phase Execution Map

```
Phase 1 → Phase 2 → Phase 3 → Phase 4 → Phase 5 → Phase 6 → Phase 7 → Phase 8 → Phase 9

Phase 1:  T1 → T2 → T3 → T4 → T5 → T6
Phase 2:  T7 → T8 → T9 → T10 → T11 → T12
Phase 3:  T13 → T14 → T15 → T16 → T17 → T18
Phase 4:  T19 → T20 → T21 → T22 → T23 → T24 → T25
Phase 5:  T26 → T27 → T28 → T29 → T30 → T31
Phase 6:  T32 → T33 → T34 → T35
Phase 7:  T36 → T37 → T38 → T39 → T40 → T41
Phase 8:  T42 → T43 → T44 → T45 → T46 → T47 → T48 → T49
Phase 9:  T50 → T51 → T52 → T53 → T54
```

Suggested batches (~7 tasks, whole phases): P1 · P2 · P3 · P4 · P5+P6 (10, or two batches) · P7 · P8 · P9.

---

## Requirement Coverage

| Requirement | Tasks |
| --- | --- |
| AID-01 | T13, T53, T54 |
| AID-02 | T13, T14, T54 |
| AID-03 | T2, T13 |
| AID-04 | T14 |
| AID-05 | T13, T16 |
| AID-06 | T9, T11, T12 |
| AID-07 | T15 |
| SES-01 | T14, T36, T53, T54 |
| SES-02 | T17, T36, T54 |
| SES-03 | T36, T54 |
| SES-04 | T17, T18 |
| SES-05 | T18, T54 |
| SES-06 | T17 |
| SES-07 | T36 |
| SES-08 | T17 |
| TREE-01 | T25, T47, T52 |
| TREE-02 | T25, T47 |
| TREE-03 | T25, T28, T47 |
| TREE-04 | T28, T47 |
| TREE-05 | T47 |
| TREE-06 | T28, T47 |
| TREE-07 | T41, T54 |
| CONT-01 | T28, T47 |
| DM-01 | T20, T45, T52 |
| DM-02 | T20, T46 |
| DM-03 | T23, T54 |
| DM-04 | T24, T37, T54 |
| DM-05 | T30 |
| DM-06 | T22, T42, T44 |
| DM-07 | T22, T46 |
| DM-08 | T19, T20 |
| GRP-01 | T6, T51 |
| GRP-02 | T27, T46 |
| GRP-03 | T11, T26 |
| GRP-04 | T12, T23 |
| PEOP-01 | T26 |
| PEOP-02 | T26 |
| PEOP-03 | T27, T48 |
| PEOP-04 | T27 |
| PEOP-05 | T26 |
| PEOP-06 | T26 |
| ACC-01 | T29, T49 |
| ACC-02 | T30 |
| ACC-03 | T32 |
| ACC-04 | T32 |
| ACC-05 | T33 |
| ACC-06 | T30, T31 |
| ACC-07 | T29, T48 |
| PROF-01 | T3, T7 |
| PROF-02 | T2, T7 |
| PROF-03 | T9, T11, T12, T48, T49, T52 |
| PROF-04 | T10, T37, T54 |
| PROF-05 | T9, T48 |
| PROF-06 | T8, T10 |
| PROF-07 | T8, T10 |
| PROF-08 | T43 |
| PROF-09 | T43, T46 |
| PROF-10 | T7, T9 |
| PROF-11 | T27, T48 |
| LAY-01 | T22, T44, T52 |
| LAY-02 | T42, T44 |
| LAY-03 | T45 |
| LAY-04 | T21, T45 |
| LAY-05 | T44, T52 |
| LAY-06 | T44 |
| AGRP-01 | T24, T37, T54 |
| AGRP-02 | T24, T37 |
| CLI-01 | T38, T53, T54 |
| CLI-02 | T40 |
| CLI-03 | T39 |
| CLI-04 | T40, T54 |
| CLI-05 | T40 |
| CLI-06 | T40 |
| CLI-07 | T40 |
| MIG-01 | T3, T4, T6, T51 |
| MIG-02 | T13, T15 |
| MIG-03 | T5, T50 |
| MIG-04 | T4 |
| MIG-05 | T1, T6, T51 |
| MRG-01 | T35, T47 |
| MRG-02 | T35 |
| NOTI-01 | T34, T49 |
| NOTI-02 | T34 |
| NOTI-03 | T34 |

84 of 84 requirements map to at least one task.

---

## Task Granularity Check

| Task | Scope | Status |
| --- | --- | --- |
| T1 | runner in one file | ✅ Granular |
| T2 | one leaf package | ✅ Granular |
| T3 | one migration + its queries | ⚠️ Cohesive (schema and generated CRUD belong together) |
| T4 | one hook | ✅ Granular |
| T5 | one SQL section | ✅ Granular |
| T6 | fixture test + one testkit option | ⚠️ Cohesive (the option exists only for this test) |
| T7 | one service + creation calls | ⚠️ Cohesive (creation must be in the same transactions) |
| T8 | one function | ✅ Granular |
| T9 | one handler file + DTOs | ✅ Granular |
| T10 | avatar endpoints | ✅ Granular |
| T11 | author/member conversion | ✅ Granular |
| T12 | delivery prefs | ✅ Granular |
| T13 | one resolver | ✅ Granular |
| T14 | agent header handling + 2 lookups | ⚠️ Cohesive (one protocol change) |
| T15 | client identity | ✅ Granular |
| T16 | removal of one mechanism | ⚠️ Cohesive (deletion spans its call sites) |
| T17 | presence + its client states | ⚠️ Cohesive (protocol both ends, keeps the gate green) |
| T18 | busy/taken-over UX incl. TS adapters | ⚠️ Cohesive (same behaviour in Go and TS) |
| T19 | access rules | ✅ Granular |
| T20 | DM service + endpoint | ✅ Granular |
| T21 | group service + endpoint | ✅ Granular |
| T22 | list + read | ✅ Granular |
| T23 | agent DM delivery data | ✅ Granular |
| T24 | two agent endpoints + limit | ✅ Granular |
| T25 | one pure function | ✅ Granular |
| T26 | people upsert | ✅ Granular |
| T27 | people visibility/profile | ✅ Granular |
| T28 | contacts endpoints | ✅ Granular |
| T29 | operator + account label | ⚠️ Cohesive (ACC-07 needs both) |
| T30 | platform DM routing | ✅ Granular |
| T31 | group auto-route | ✅ Granular |
| T32 | one rule in the sender | ✅ Granular |
| T33 | own-account handling | ✅ Granular |
| T34 | notify end to end | ⚠️ Cohesive (setting is useless without the send) |
| T35 | merge | ✅ Granular |
| T36 | launcher | ✅ Granular |
| T37 | three tools | ✅ Granular |
| T38 | one command | ✅ Granular |
| T39 | one component | ✅ Granular |
| T40 | one command flow | ✅ Granular |
| T41 | one command | ✅ Granular |
| T42 | data layer | ✅ Granular |
| T43 | three small components | ✅ Granular |
| T44 | shell + list | ⚠️ Cohesive (the list is the shell's sidebar) |
| T45 | two dialogs + search | ✅ Granular |
| T46 | one page | ✅ Granular |
| T47 | one page | ✅ Granular |
| T48 | one page | ✅ Granular |
| T49 | settings + connector sheet | ⚠️ Cohesive (small additions to two forms) |
| T50 | removal of one mechanism | ⚠️ Cohesive |
| T51 | one migration + DTO cleanup | ⚠️ Cohesive (sqlc/tygo drift otherwise) |
| T52 | smoke suite | ✅ Granular |
| T53 | docs | ✅ Granular |
| T54 | UAT record | ✅ Granular |

---

## Diagram-Definition Cross-Check

| Task | Depends On (task body) | Diagram Shows | Status |
| --- | --- | --- | --- |
| T1 | None | start of Phase 1 | ✅ Match |
| T2 | T1 | T1 → T2 | ✅ Match |
| T3 | T2 | T2 → T3 | ✅ Match |
| T4 | T3 | T3 → T4 | ✅ Match |
| T5 | T4 | T4 → T5 | ✅ Match |
| T6 | T5 | T5 → T6 | ✅ Match |
| T7 | T6 (Phase 1) | start of Phase 2 | ✅ Match |
| T8 | T7 | T7 → T8 | ✅ Match |
| T9 | T8 | T8 → T9 | ✅ Match |
| T10 | T9 | T9 → T10 | ✅ Match |
| T11 | T10 | T10 → T11 | ✅ Match |
| T12 | T11 | T11 → T12 | ✅ Match |
| T13 | T12 (Phase 2) | start of Phase 3 | ✅ Match |
| T14 | T13 | T13 → T14 | ✅ Match |
| T15 | T14 | T14 → T15 | ✅ Match |
| T16 | T15 | T15 → T16 | ✅ Match |
| T17 | T16 | T16 → T17 | ✅ Match |
| T18 | T17 | T17 → T18 | ✅ Match |
| T19 | T18 (Phase 3) | start of Phase 4 | ✅ Match |
| T20 | T19 | T19 → T20 | ✅ Match |
| T21 | T20 | T20 → T21 | ✅ Match |
| T22 | T21 | T21 → T22 | ✅ Match |
| T23 | T22 | T22 → T23 | ✅ Match |
| T24 | T23 | T23 → T24 | ✅ Match |
| T25 | T24 | T24 → T25 | ✅ Match |
| T26 | T25 (Phase 4) | start of Phase 5 | ✅ Match |
| T27 | T26 | T26 → T27 | ✅ Match |
| T28 | T27 | T27 → T28 | ✅ Match |
| T29 | T28 | T28 → T29 | ✅ Match |
| T30 | T29 | T29 → T30 | ✅ Match |
| T31 | T30 | T30 → T31 | ✅ Match |
| T32 | T31 (Phase 5) | start of Phase 6 | ✅ Match |
| T33 | T32 | T32 → T33 | ✅ Match |
| T34 | T33 | T33 → T34 | ✅ Match |
| T35 | T34 | T34 → T35 | ✅ Match |
| T36 | T35 (Phase 6) | start of Phase 7 | ✅ Match |
| T37 | T36 | T36 → T37 | ✅ Match |
| T38 | T37 | T37 → T38 | ✅ Match |
| T39 | T38 | T38 → T39 | ✅ Match |
| T40 | T39 | T39 → T40 | ✅ Match |
| T41 | T40 | T40 → T41 | ✅ Match |
| T42 | T41 (Phase 7) | start of Phase 8 | ✅ Match |
| T43 | T42 | T42 → T43 | ✅ Match |
| T44 | T43 | T43 → T44 | ✅ Match |
| T45 | T44 | T44 → T45 | ✅ Match |
| T46 | T45 | T45 → T46 | ✅ Match |
| T47 | T46 | T46 → T47 | ✅ Match |
| T48 | T47 | T47 → T48 | ✅ Match |
| T49 | T48 | T48 → T49 | ✅ Match |
| T50 | T49 (Phase 8) | start of Phase 9 | ✅ Match |
| T51 | T50 | T50 → T51 | ✅ Match |
| T52 | T51 | T51 → T52 | ✅ Match |
| T53 | T52 | T52 → T53 | ✅ Match |
| T54 | T53 | T53 → T54 | ✅ Match |

No task depends on a later phase.

---

## Test Co-location Validation

| Task | Code Layer Created/Modified | Matrix Requires | Task Says | Status |
| --- | --- | --- | --- | --- |
| T1 | Store | integration | integration | ✅ OK |
| T2 | Pure rules | unit | unit | ✅ OK |
| T3 | Store/migrations | integration | integration | ✅ OK |
| T4 | Store/migrations | integration | integration | ✅ OK |
| T5 | Store/migrations | integration | integration | ✅ OK |
| T6 | Store + HTTP (upgrade route) | integration, e2e | integration (with an api e2e test) | ✅ OK |
| T7 | Services | integration | integration | ✅ OK |
| T8 | Pure rules | unit | unit | ✅ OK |
| T9 | HTTP | e2e | e2e | ✅ OK |
| T10 | HTTP | e2e | e2e | ✅ OK |
| T11 | HTTP | e2e | e2e | ✅ OK |
| T12 | Services (delivery, sender) | integration | integration | ✅ OK |
| T13 | Services | integration | integration | ✅ OK |
| T14 | HTTP/WS | e2e | e2e | ✅ OK |
| T15 | Channel/CLI | e2e | e2e | ✅ OK |
| T16 | HTTP/WS | e2e | e2e | ✅ OK |
| T17 | WS + channel | e2e | e2e | ✅ OK |
| T18 | Channel + TS adapters | e2e, unit (bun) | e2e (with bun tests) | ✅ OK |
| T19 | Services (access) | integration | integration | ✅ OK |
| T20 | Services + HTTP | e2e | e2e | ✅ OK |
| T21 | Services + HTTP | e2e | e2e | ✅ OK |
| T22 | Services + HTTP + live | e2e | e2e | ✅ OK |
| T23 | Channel + delivery | e2e | e2e | ✅ OK |
| T24 | HTTP | e2e | e2e | ✅ OK |
| T25 | Pure rules | unit | unit | ✅ OK |
| T26 | Services (pipeline) | integration | integration | ✅ OK |
| T27 | Services + HTTP | e2e | e2e | ✅ OK |
| T28 | HTTP | e2e | e2e | ✅ OK |
| T29 | Services + adapters | integration | integration | ✅ OK |
| T30 | Pipeline + HTTP | e2e | e2e | ✅ OK |
| T31 | Pipeline | integration | integration | ✅ OK |
| T32 | Sender | integration | integration | ✅ OK |
| T33 | Pipeline + adapter | integration | integration | ✅ OK |
| T34 | Services + HTTP + outbox | e2e | e2e | ✅ OK |
| T35 | Services + HTTP | e2e | e2e | ✅ OK |
| T36 | CLI | e2e | e2e | ✅ OK |
| T37 | Channel tools | e2e | e2e | ✅ OK |
| T38 | CLI | e2e | e2e | ✅ OK |
| T39 | Pure rules (menu) | unit | unit | ✅ OK |
| T40 | CLI | e2e | e2e | ✅ OK |
| T41 | CLI | e2e | e2e | ✅ OK |
| T42 | Web | unit | unit | ✅ OK |
| T43 | Web | unit | unit | ✅ OK |
| T44 | Web | unit | unit | ✅ OK |
| T45 | Web | unit | unit | ✅ OK |
| T46 | Web | unit | unit | ✅ OK |
| T47 | Web | unit | unit | ✅ OK |
| T48 | Web | unit | unit | ✅ OK |
| T49 | Web | unit | unit | ✅ OK |
| T50 | HTTP/services (removal) | e2e | e2e | ✅ OK |
| T51 | Store/migrations | integration | integration | ✅ OK |
| T52 | Web end-to-end | e2e (Playwright) | e2e | ✅ OK |
| T53 | Docs | none | none | ✅ OK |
| T54 | UAT (real harness) | e2e (manual UAT) | e2e | ✅ OK |

Tests for removed v0.3 behaviour (T16, T50) are replaced by tests of the rule that supersedes it,
named in the commit body; no other test is deleted or weakened.

---

## Tools per task

- MCP: none needed (filesystem and shell only).
- Skills: `web-design-guidelines` (T43 review), `impeccable` (T44, T47 layout work), `orca-cli`
  (T54 terminals). Confirm or adjust before Execute.
