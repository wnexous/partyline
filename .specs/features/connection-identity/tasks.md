# Connection Identity Tasks

## Execution Protocol (MANDATORY -- do not skip)

Implement these tasks with the `tlc-spec-driven` skill: **activate it by name and follow its Execute flow and Critical Rules.** Do not search for skill files by filesystem path. The skill is the source of truth for the full flow (per-task cycle, sub-agent delegation, adequacy review, Verifier, discrimination sensor).

**If the skill cannot be activated, STOP and tell the user - do not proceed without it.**

---

**Design**: `.specs/features/connection-identity/design.md`
**Status**: Draft

Retired behaviour (CID-15) is retired with its tests. The task that removes a feature deletes the
tests that pin it, and its commit body names the retired requirement IDs (ROUTE-01..03,
ROUTE-06, ROUTE-07, DLV-05, UI-13, ...). Tests of other features that used a retired feature only
as setup switch to the T1 helper and keep their assertions. Weakening an assertion is still
forbidden.

**Worker tier**: tasks tagged **[small]** are isolated and mechanical (a cheaper worker fits);
**[core]** tasks carry the design's rules and need a high-reasoning worker.

---

## Test Coverage Matrix

> Generated from codebase, project guidelines, and spec - confirm before Execute. Guidelines found: `AGENTS.md` (tests derive from ACs; `make check` before every commit; no message text or secrets in logs; synthetic fixtures), `.specs/LESSONS.md` (confirmed L-017, L-018, L-029, L-042), `Makefile`, `scripts/check.sh`, `web/vite.config.ts`, `.specs/features/web-app/conventions.md`, sampled tests in `internal/api/*_test.go`, `internal/connectors/*_test.go`, `internal/store/*_migration_test.go`.

| Code Layer | Required Test Type | Coverage Expectation | Location Pattern | Run Command |
| --- | --- | --- | --- | --- |
| Pure rules (`delivery.Plan`) | unit | All branches; 1:1 to CID-01/03; table covers operator, non-operator agent, non-operator user, chat author, system, listed/unlisted recipients | `internal/delivery/*_test.go` | `go test -count=1 ./internal/delivery/...` |
| Store, migrations, Go hooks | integration (real SQLite) | Every keep/adopt/drop rule of the upgrade with a case where only that condition excludes a room (L-042); index; rollback on hook failure | `internal/store/*_test.go` | `go test -count=1 ./internal/store/...` |
| Services (rooms, access, conversations, connectors pipeline/sender, agents) | integration (real SQLite, fake connector) | Every AC and error path; concurrency where CID-08 names it | `internal/**/*_test.go` | `make test` |
| HTTP routes, channel tools | e2e (testkit, real app on httptest) | Every changed route: happy + edge + error; every new error code observed at its HTTP status (L-017); negative tests isolate the guard under test (L-018) | `internal/api/*_test.go`, `internal/channel/*_test.go` | `make test` |
| Platform adapter (WhatsApp) | integration against the fake evolution server | 0, 1 and 2 instances; send to another instance | `internal/connectors/whatsapp/*_test.go` | `go test -count=1 ./internal/connectors/whatsapp/...` |
| Web components/pages | unit (Vitest + Testing Library) | Each visible AC; i18n keys in `en` and `pt-BR` | `web/src/**/*.test.{ts,tsx}` | `cd web && bun run test` |
| Docs | none | build gate only | `README*.md`, `docs/*.md` | `make check` |

## Gate Check Commands

> Generated from codebase - confirm before Execute.

| Gate Level | When to Use | Command |
| --- | --- | --- |
| Quick | While iterating on unit-only code | `go test -count=1 ./internal/<pkg>/...` or `cd web && bun run test` |
| Full | While iterating on integration/e2e code | `make test` |
| Build | Before **every** commit (AGENTS.md, L-029) | `make check` |
| E2E | T35 and before UAT | `make e2e` |

Every task's commit gate is `make check` (lint, vet, generated-code drift, all Go/web tests,
build). DTO tasks run `make generate` first.

---

## Execution Plan

Phases run in order; tasks within a phase run in order. Arrows show real dependencies; a task
with no incoming arrow inside its phase depends only on earlier phases.

### Phase 1: Outbound identity

```
T1 -> T2 -> T3 -> T4
T3 -> T5 -> T6
```

### Phase 2: Platform conversations

```
T7 -> T8
T9 -> T10
T9 -> T11
T12
```

### Phase 3: Members and DTOs

```
T13 -> T14
T15 -> T16
T17
```

### Phase 4: Operator lifecycle

```
T18 -> T20 -> T21
T19 -> T20
```

### Phase 5: Web

```
T22
T23
T24
T25
T26
```

### Phase 6: Server retirement and WhatsApp

```
T27
T28
T29
T30 -> T31
T32
```

### Phase 7: Upgrade and release

```
T33 -> T34 -> T35 -> T36
```

---

## Task Breakdown

### Phase 1: Outbound identity

#### T1: Testkit helper for platform conversations [small]

**What**: Add a testkit helper `PlatformConversation(t, connector, operator, chat kind)`. It sets the connection's operator, sets the auto-route scope and pushes one inbound message through the fake connector, then returns the conversation id. Tests then set up platform conversations without `POST /chats/{id}/route`.
**Where**: `internal/testkit/platform.go`
**Depends on**: None
**Reuses**: `internal/connectors/connectortest`, existing testkit app/clients
**Requirement**: CID-06 (test support)
**Done when**:
- [ ] Helper used by one new test that asserts the returned room holds the chat's message
- [ ] Gate check passes: `make check`
**Tests**: integration
**Gate**: build
**Commit**: `test(testkit): add platform conversation helper`

#### T2: Plan sends to a chat only the operator's posts [core]

**What**: `delivery.Plan(members, draft)`: chat members carry `Operator`, and drafts carry `AuthorIID`. A chat receives only agent/user posts whose `AuthorIID` equals its operator (outbound on; listed when recipients are given). Chat and system messages reach no chat. `RoomState` is removed.
**Where**: `internal/delivery/rules.go`
**Depends on**: T1
**Reuses**: existing `Plan` table tests
**Requirement**: CID-01, CID-03, CID-15
**Done when**:
- [ ] Table test: operator agent, operator user, non-operator agent, non-operator user, chat author (no relay), system, recipients listed/unlisted
- [ ] Bridge/broadcast cases deleted (DLV-05, ROUTE-06 retired)
- [ ] Gate check passes: `make check`
**Tests**: unit
**Gate**: build
**Commit**: `feat(delivery): send through a chat only the operator's posts`

#### T3: Rooms feed each chat's operator into Plan [core]

**What**: `ListRoomMembers` returns `COALESCE(operator_iid, owner_id)` for chat rows. `rooms.insert` passes it and the draft's author iid to Plan. `applyNoBroadcast` and `NoBroadcastWarning` are removed.
**Where**: `internal/rooms/messages.go`
**Depends on**: T2
**Reuses**: `internal/store/queries/rooms.sql` `ListRoomMembers`
**Requirement**: CID-01, CID-15
**Done when**:
- [ ] Integration: in a platform group, the operator's post creates one outbox entry. In a DM (verified user, operator AI) carrying the user's chat, the user's web post creates none
- [ ] `no_broadcast_test.go` and the broadcast cases deleted (ROUTE-06 retired); tests that relied on other agents sending switch to T1
- [ ] Gate check passes: `make check`
**Tests**: integration
**Gate**: build
**Commit**: `feat(rooms): plan chat deliveries by the chat's operator`

#### T4: Sender refuses authors that do not operate the connection [core]

**What**: `Sender.Send` fails a `room` entry permanently (`ErrNotOperator`) when the message's `author_iid` is not the connection's operator, and always sends the operator unsigned. The per-member signature lookup is removed. A notify copy leaves only through a connection the notified user operates.
**Where**: `internal/connectors/sender.go`
**Depends on**: T3
**Reuses**: `operatorID`, outbox permanent-failure path
**Requirement**: CID-01, CID-03
**Done when**:
- [ ] A queued entry authored by a non-operator ends `failed` and the fake connector received nothing
- [ ] Operator AI and operator owner messages arrive with `Unsigned=true`
- [ ] A notify copy through an AI-operated connection is not sent; one through a user-operated connection is
- [ ] Gate check passes: `make check`
**Tests**: integration
**Gate**: build
**Commit**: `feat(connectors): refuse to send for anyone but the operator`

#### T5: Post guard for platform conversations [core]

**What**: In a room that holds a transport row, `rooms.Service.Post` refuses with `ErrNotOperator` an author that is not an agent/user member or DM party. A viewer (`use` grantee) gets `ErrNotOperator` instead of `ErrForbidden` there. Callers that cannot view still get `ErrNotFound`.
**Where**: `internal/rooms/messages.go`
**Depends on**: T3
**Reuses**: `ListRoomChatConnectors`, `checkCounterpart` pattern
**Requirement**: CID-02, CID-11
**Done when**:
- [ ] Integration: owner viewing an AI-operated group, a `use` grantee, and a reaction or file by them → `ErrNotOperator`, nothing stored
- [ ] Operator posts pass; a verified user in their own platform DM posts (stored, not sent)
- [ ] Gate check passes: `make check`
**Tests**: integration
**Gate**: build
**Commit**: `feat(rooms): only the operator writes in a platform conversation`

#### T6: `403 not_operator` over HTTP and the channel [core]

**What**: Map `ErrNotOperator` to `403 not_operator` (`dto.ErrorCodeNotOperator`). Then pin it end to end: messages, files and reactions over HTTP, and the channel's `send_message`/`reply`/`react` errors.
**Where**: `internal/api/rooms_handlers.go`
**Depends on**: T5
**Reuses**: existing error mapping, testkit clients
**Requirement**: CID-02, CID-11
**Done when**:
- [ ] e2e `not_operator_test.go`: owner viewer and operator-AI grantee get 403 `not_operator`; another AI of the owner gets 404; the operator gets 201 (L-017, L-018)
- [ ] `make generate` run; types regenerated
- [ ] Gate check passes: `make check`
**Tests**: e2e
**Gate**: build
**Commit**: `feat(api): answer not_operator in platform conversations`

### Phase 2: Platform conversations

#### T7: Create platform groups as a system call [core]

**What**: `rooms.CreatePlatformGroup(ctx, ownerID, name, operator, chatID)` creates, in one transaction and without member checks, the group (owner = connection owner), the operator member and the transport row (outbound on).
**Where**: `internal/rooms/platform.go`
**Depends on**: None
**Reuses**: `CreateWithMembers` insert queries
**Requirement**: CID-06, CID-08
**Done when**:
- [ ] Integration: the room exists with exactly the operator and the transport row; `MembersChanged`/`RoomChanged` published
- [ ] Gate check passes: `make check`
**Tests**: integration
**Gate**: build
**Commit**: `feat(rooms): create platform groups for chats`

#### T8: Inbound routing by scope and operator [core]

**What**: `Pipeline.route` delivers a chat that has a conversation there, whatever the scope (a DM chat still follows its operator through `platformDM`). A chat without one gets a conversation only when the scope includes its kind (CID-18): DM chats via `platformDM`, group/channel chats via `autoGroup` → `CreatePlatformGroup`, with caps and serialization unchanged. The catch-all `autoRoute` and the skip check are removed from the pipeline.
**Where**: `internal/connectors/autoroute.go`
**Depends on**: T7
**Reuses**: `routeState`, `setDetail`/`clearDetail`, `platformDM`
**Requirement**: CID-06, CID-07, CID-08, CID-09, CID-17, CID-18
**Done when**:
- [ ] New group chat (scope groups) → conversation with the operator, and the message stored (CID-06); a second sender is recorded in `chat_people` (CID-07)
- [ ] Concurrent first messages → one conversation (CID-08)
- [ ] Scope `dms` + group chat with an existing conversation → message delivered there (CID-18)
- [ ] Scope `off` + unknown group chat → chat recorded, no message kept; scope `off` + chat whose platform conversation was deleted → no message kept (CID-18; each case excluded by its own condition, L-042)
- [ ] Own-account message lands in the platform group authored by the operator (CID-17)
- [ ] e2e: operator offline, 3 messages, reconnect → backlog delivers them (CID-09)
- [ ] `catch_all_test.go` catch-all cases deleted (ROUTE-01..03, ROUTE-08 room cap, ROUTE-11 retired); `auto_groups_test.go` updated
- [ ] Gate check passes: `make check`
**Tests**: integration
**Gate**: build
**Commit**: `feat(connectors): route every chat to its operator's conversation`

#### T9: Platform group access level [core]

**What**: `UserRoomLevel` resolves a group that holds a transport row by `platformGroupLevel`:
- manage for the owner;
- post for a user member;
- view for the owner of the operator AI and its named `use` grantees;
- grants ignored.
**Where**: `internal/access/access.go`
**Depends on**: None
**Reuses**: `dmLevel`, `viewsAIOf`
**Requirement**: CID-10
**Done when**:
- [ ] Integration matrix: owner, operator AI (member), operator-AI `use` grantee, connector `use` grantee, everyone-grant holder, admin, other AI. Each excluded case is excluded by only that condition (L-042)
- [ ] Gate check passes: `make check`
**Tests**: integration
**Gate**: build
**Commit**: `feat(access): platform groups are visible to their operator's viewers only`

#### T10: Refuse sharing a platform conversation [small]

**What**: `SetVisibility` on a room holding a transport row returns `ErrPlatformNoSharing`, which the API maps to 422 `platform_no_sharing`.
**Where**: `internal/access/access.go`
**Depends on**: T9
**Reuses**: `ErrDMNoSharing` path
**Requirement**: CID-10
**Done when**:
- [ ] e2e: PUT `/rooms/{id}/sharing` on a platform group → 422 `platform_no_sharing`, grants unchanged (L-017)
- [ ] Gate check passes: `make check`
**Tests**: e2e
**Gate**: build
**Commit**: `feat(access): refuse sharing platform conversations`

#### T11: List platform groups viewed through an AI [core]

**What**: The conversation list includes platform groups the user views through an operator AI they own or may use, as it does for AI-viewed DMs.
**Where**: `internal/conversations/list.go`
**Depends on**: T9
**Reuses**: the AI-viewed DM listing query
**Requirement**: CID-10
**Done when**:
- [ ] e2e: the grantee's `GET /conversations` lists the group; another user's list does not
- [ ] Gate check passes: `make check`
**Tests**: e2e
**Gate**: build
**Commit**: `feat(conversations): list platform groups seen through an AI`

#### T12: Notify options only on connections the user operates [small]

**What**: `ListNotifyOptions` keeps only DM chats whose connection is operated by the user, so `SetNotify` refuses any other chat.
**Where**: `internal/store/queries/conversations.sql`
**Depends on**: None
**Reuses**: `NotifyUsable`
**Requirement**: CID-01
**Done when**:
- [ ] e2e: a chat on an AI-operated connection is absent from `/me/notify-options` and PUT `/me/notify` with it → 422
- [ ] Gate check passes: `make check`
**Tests**: e2e
**Gate**: build
**Commit**: `feat(conversations): notify only through accounts the user operates`

### Phase 3: Members and DTOs

#### T13: Chats are never members; platform members are fixed [core]

**What**: `AddMember`/`CreateWithMembers`/group creation refuse kind `chat` with `ErrChatsNotMembers`. Add/remove in a room holding a transport row returns `ErrPlatformMembersFixed`. `UpdateMember` keeps editing the operator's delivery settings.
**Where**: `internal/rooms/members.go`
**Depends on**: None
**Reuses**: `ErrDMMembersFixed` pattern
**Requirement**: CID-12
**Done when**:
- [ ] Integration: each refusal, each tested with an otherwise valid request (L-018); operator delivery change succeeds
- [ ] Chat-member tests of `checkUse` deleted (ACL-06 chat branch retired)
- [ ] Gate check passes: `make check`
**Tests**: integration
**Gate**: build
**Commit**: `feat(rooms): refuse chats as members`

#### T14: HTTP codes for member refusals [small]

**What**: Map `ErrChatsNotMembers` → 422 `chats_not_members` and `ErrPlatformMembersFixed` → 422 `platform_members_fixed`, with DTO constants.
**Where**: `internal/api/rooms_handlers.go`
**Depends on**: T13
**Reuses**: error mapping table
**Requirement**: CID-12
**Done when**:
- [ ] e2e: `POST /rooms/{id}/members {kind: chat}` → 422 `chats_not_members`; add/remove in a platform group → 422 `platform_members_fixed` (L-017)
- [ ] Gate check passes: `make check`
**Tests**: e2e
**Gate**: build
**Commit**: `feat(api): report chats_not_members and platform_members_fixed`

#### T15: Members endpoint lists intelligences and people [core]

**What**: The members listing (`rooms.Service.Members`) filters out chat rows; delivery keeps reading every row through `ListRoomMembers`. `GET /rooms/{id}/members` returns agent/user members plus, for platform conversations, the people seen as `kind: "person"` (`MemberKindPerson`, output only). `member_count` counts the same entries.
**Where**: `internal/api/rooms_handlers.go`
**Depends on**: None
**Reuses**: `ListChatPeople`, `MaxChatPeople`
**Requirement**: CID-06, CID-07, CID-13
**Done when**:
- [ ] e2e: a platform group lists the operator and two people, and no chat; a regular group lists only intelligences
- [ ] Gate check passes: `make check`
**Tests**: e2e
**Gate**: build
**Commit**: `feat(api): list people instead of chats as members`

#### T16: `Room.platform` block [small]

**What**: The Room DTO gains `platform {connector_id, connector_kind, chat_kind, operator}` for rooms holding a transport row.
**Where**: `internal/api/dto/rooms.go`
**Depends on**: T15
**Reuses**: `ListRoomChatConnectors`, `IntelligenceRef` conversion
**Requirement**: CID-10, CID-11
**Done when**:
- [ ] e2e: GET `/rooms/{id}` on a platform group and a platform DM carries the block; a regular group has none
- [ ] Gate check passes: `make check`
**Tests**: e2e
**Gate**: build
**Commit**: `feat(api): describe a conversation's platform and operator`

#### T17: `Chat.conversation_id` [small]

**What**: Connector chat listings carry the id of the chat's conversation when it has one.
**Where**: `internal/api/chats_handlers.go`
**Depends on**: None
**Reuses**: `ListChatRooms`
**Requirement**: CID-08, CID-15
**Done when**:
- [ ] e2e: `GET /connectors/{id}/chats` shows `conversation_id` for a routed chat and none for an unrouted one
- [ ] Gate check passes: `make check`
**Tests**: e2e
**Gate**: build
**Commit**: `feat(api): link chats to their conversation`

### Phase 4: Operator lifecycle

#### T18: Swap the operator in platform groups [core]

**What**: `rooms.SwapPlatformGroupOperator(ctx, q, connectorID, from, to, now)` is a tx helper. In every group holding a transport row of the connection it replaces `from`'s member row with `to`'s; a new AI's cursor is set to the last seq. It returns the room ids.
**Where**: `internal/rooms/platform.go`
**Depends on**: None
**Reuses**: member insert/delete queries, `AdvanceAgentCursor`
**Requirement**: CID-05, CID-16
**Done when**:
- [ ] Integration: AI→AI, AI→owner and owner→AI swaps; a group of another connection untouched; new AI backlog empty
- [ ] Gate check passes: `make check`
**Tests**: integration
**Gate**: build
**Commit**: `feat(rooms): swap the operator of platform groups`

#### T19: Move platform DMs to a new operator [core]

**What**: Extract `EnsurePlatformDM`'s body into a tx helper. Reorder it so a chat leaves its other DMs before it joins, which keeps one row per chat at every statement. Add `MovePlatformDMs(ctx, q, connectorID, from, to, ownerID, now)`.
**Where**: `internal/conversations/platform.go`
**Depends on**: None
**Reuses**: `EnsurePlatformDM`, `ListChatDMRoomIDs`, `DeletePendingRoomChatOutbox`
**Requirement**: CID-05, CID-08
**Done when**:
- [ ] Integration: person DM moves to DM(person, new) with old history kept; a DM whose author is the new operator only loses the chat; pending outbox of the old DM deleted
- [ ] Existing platform DM tests still pass unchanged
- [ ] Gate check passes: `make check`
**Tests**: integration
**Gate**: build
**Commit**: `feat(conversations): move platform DMs to a new operator`

#### T20: Operator change moves every platform conversation [core]

**What**: In `connectors.Service.Update`, when the operator changes, the same transaction runs T18 and T19 and records one `connector.operator` audit entry `{from, to, groups, dms}`. Events are published after commit.
**Where**: `internal/connectors/service.go`
**Depends on**: T18, T19
**Reuses**: existing operator audit code
**Requirement**: CID-05
**Done when**:
- [ ] e2e PATCH `operator_id`: groups have the new operator only, DM chats are in DM(x, new), exactly one audit entry
- [ ] A forced failure in the helper leaves members, DMs and `operator_iid` unchanged
- [ ] Gate check passes: `make check`
**Tests**: e2e
**Gate**: build
**Commit**: `feat(connectors): move conversations when the operator changes`

#### T21: Deleted operator hands groups to the owner [core]

**What**: Before `agents.Registry.Delete` removes the AI's memberships, it swaps each connection the AI operates to the owner (T18) and records `connector.operator` with `reason: operator_deleted`. A test pins that merging an operator AI keeps its platform groups with the surviving AI.
**Where**: `internal/agents/registry.go`
**Depends on**: T20
**Reuses**: `ListConnectorsOperatedBy`, T18
**Requirement**: CID-16
**Done when**:
- [ ] Integration: after delete the group's members are the owner (plus people), `operator_iid` is NULL and one audit entry exists; the next DM message creates DM(person, owner)
- [ ] Merge case asserted
- [ ] Gate check passes: `make check`
**Tests**: integration
**Gate**: build
**Commit**: `feat(agents): give a deleted operator's groups to the owner`

### Phase 5: Web

#### T22: Member picker offers users and AIs only [small]

**What**: Remove the chats section and the `useChats` (GET /chats) dependency from the picker.
**Where**: `web/src/components/member-picker.tsx`
**Depends on**: None
**Reuses**: existing picker tests
**Requirement**: CID-13
**Done when**:
- [ ] Unit: picker renders users and AIs and no chat entry
- [ ] Gate check passes: `make check`
**Tests**: unit
**Gate**: build
**Commit**: `feat(web): add only users and AIs to groups`

#### T23: Member list shows people, not chats [small]

**What**: The member list renders `person` entries with their platform badge. It drops chat rows and signature/outbound settings (`member-settings.tsx`), and hides add/remove for rooms with `platform`.
**Where**: `web/src/components/member-list.tsx`
**Depends on**: None
**Reuses**: `participant-list.tsx`, `platformLabel`
**Requirement**: CID-06, CID-13, CID-15
**Done when**:
- [ ] Unit: platform group shows operator + people with badges, no add button; regular group unchanged
- [ ] Gate check passes: `make check`
**Tests**: unit
**Gate**: build
**Commit**: `feat(web): show the people of a platform conversation`

#### T24: Composer is read-only for non-operators [small]

**What**: Remove the chat recipients picker. When `room.platform.operator.id` is not the current user's own intelligence, show "Only {operator} writes here" (en, pt-BR) instead of the input. Map a `not_operator` error to the same text.
**Where**: `web/src/components/composer.tsx`
**Depends on**: None
**Reuses**: existing composer error mapping
**Requirement**: CID-02, CID-11, CID-15
**Done when**:
- [ ] Unit: owner viewing an AI-operated group sees the notice and no input; the operator sees the input without a chat picker
- [ ] Gate check passes: `make check`
**Tests**: unit
**Gate**: build
**Commit**: `feat(web): read-only composer for viewers of platform conversations`

#### T25: Room settings without chat settings [small]

**What**: Remove the bridge and broadcast toggles and the catch-all notice, and hide sharing for rooms with `platform`.
**Where**: `web/src/components/room-settings-sheet.tsx`
**Depends on**: None
**Reuses**: `room-visibility.tsx`
**Requirement**: CID-10, CID-15
**Done when**:
- [ ] Unit: no bridge/broadcast fields; platform conversation shows no sharing control
- [ ] Gate check passes: `make check`
**Tests**: unit
**Gate**: build
**Commit**: `feat(web): drop chat settings from rooms`

#### T26: Connector page without routing tools [small]

**What**: Remove `RouteChatDialog` and the catch-all room picker. Keep the scope select, labelled "Receive messages from: nobody, DMs, groups, everyone" (en, pt-BR). Each chat links to its `conversation_id`.
**Where**: `web/src/routes/_app/connectors/$connectorId.tsx`
**Depends on**: None
**Reuses**: `connector-catch-all.tsx` scope select
**Requirement**: CID-15, CID-18
**Done when**:
- [ ] Unit: no route action, no room picker; scope select saves `auto_route_scope`; chat row links to its conversation
- [ ] `route-chat-dialog.tsx` and its test removed (UI-13 retired)
- [ ] Gate check passes: `make check`
**Tests**: unit
**Gate**: build
**Commit**: `feat(web): connector page without chat routing`

### Phase 6: Server retirement and WhatsApp

#### T27: WhatsApp connection uses exactly one instance [small]

**What**: `instances` becomes required, with new help text. `Run` with 0 or ≥2 instances reports `StatusError` with the detail `choose the instance (number) of this connection`, does not dial and waits for ctx. `Send` refuses a chat of another instance.
**Where**: `internal/connectors/whatsapp/connector.go`
**Depends on**: None
**Reuses**: `allowed`, fake evolution server
**Requirement**: CID-04
**Done when**:
- [ ] Integration: 0 and 2 instances → exact detail, no WebSocket dial, a frame is not delivered; 1 instance connects; send to another instance fails
- [ ] `idleWarning` allowlist-empty branch removed with its test
- [ ] Gate check passes: `make check`
**Tests**: integration
**Gate**: build
**Commit**: `feat(whatsapp): one instance per connection`

#### T28: Retire chat routing endpoints [small]

**What**: Remove `POST /chats/{id}/route`, `GET /chats`, `connectors.RouteChat`, `RouteChatRequest` and their tests. Tests that used them as setup switch to T1.
**Where**: `internal/api/chats_handlers.go`
**Depends on**: None
**Reuses**: T1 helper
**Requirement**: CID-15
**Done when**:
- [ ] e2e: both routes answer 404/405
- [ ] `make generate` run
- [ ] Gate check passes: `make check`
**Tests**: e2e
**Gate**: build
**Commit**: `feat(api)!: retire routing chats to agents`

#### T29: Retire catch-all settings [small]

**What**: Remove `auto_route_room_id` from the connector DTOs and `Update` (a sent value is ignored), plus `StopCatchAllConnectors`, `SetRoomCatchAllSafety`, `skipAutoRoute`, `Room.catch_all` and `AutoRouteUnavailable`.
**Where**: `internal/connectors/service.go`
**Depends on**: None
**Reuses**: none
**Requirement**: CID-15
**Done when**:
- [ ] e2e: PATCH with `auto_route_room_id` leaves the connector unchanged; GET has no such field
- [ ] `catch_all_settings_test.go` deleted (ROUTE-03, ROUTE-04, ROUTE-07, ROUTE-09 retired)
- [ ] Gate check passes: `make check`
**Tests**: e2e
**Gate**: build
**Commit**: `feat(connectors)!: retire catch-all rooms`

#### T30: Retire bridge and broadcast settings [small]

**What**: Remove `bridge_chats`/`broadcast` from the Room DTOs, room create/update and queries. Add migration `0017_drop_room_chat_settings.sql`, which drops both columns.
**Where**: `internal/store/migrations/0017_drop_room_chat_settings.sql`
**Depends on**: None
**Reuses**: AD-012 drop-column pattern
**Requirement**: CID-15
**Done when**:
- [ ] Store test: columns absent after migrate; e2e: Room DTO without the fields, PATCH with them changes nothing
- [ ] `make generate` run
- [ ] Gate check passes: `make check`
**Tests**: integration
**Gate**: build
**Commit**: `feat(rooms)!: retire bridges and broadcast to chats`

#### T31: Retire chat signature and outbound fields of members [small]

**What**: Drop `sign_outbound` and `outbound` from `RoomMember` output and `MemberUpdate`, and remove the `checkUse` chat branch.
**Where**: `internal/api/dto/rooms.go`
**Depends on**: T30
**Reuses**: none
**Requirement**: CID-15
**Done when**:
- [ ] e2e: member DTO has neither field; PATCH member with them changes nothing
- [ ] Gate check passes: `make check`
**Tests**: e2e
**Gate**: build
**Commit**: `feat(api)!: retire per-member chat signatures`

#### T32: Channel stops advertising chat_ids [small]

**What**: Remove `chat_ids`/`recipients` from the `send_message` schema and the broadcast/chat sentences from the instructions. Arguments carrying them are still accepted and forwarded.
**Where**: `internal/channel/tools.go`
**Depends on**: None
**Reuses**: channel tool tests
**Requirement**: CID-15
**Done when**:
- [ ] Channel test: schema has no `chat_ids`; a call with `chat_ids` still posts; instructions golden updated
- [ ] Gate check passes: `make check`
**Tests**: e2e
**Gate**: build
**Commit**: `feat(channel): stop advertising chat_ids`

### Phase 7: Upgrade and release

#### T33: Upgrade migration 0018 with its Go hook [core]

**What**: `0018_connection_identity.sql` plus the hook `upgradeConnectionIdentity` implement the design's upgrade rules 1-8: keep DM chats only in their operator DM, adopt one-chat operator groups, delete other chat rows and their pending outbox, drop grants of adopted rooms, clean non-operator pending entries, clear `auto_route_room_id` and non-qualifying `notify_chat_id`, add `chats.route_pending` and set it for every chat that was in a room and keeps none, write per-connection audit rows, create `room_members_chat_once`.
**Where**: `internal/store/connection_identity_upgrade.go`
**Depends on**: None
**Reuses**: `intelligences_backfill.go` hook pattern, `openMigratedBefore`
**Requirement**: CID-14, CID-08
**Done when**:
- [ ] Store test from a pre-0018 fixture. A mixed room (owner, two AIs, one chat) loses the chat and keeps messages and intelligences. A one-chat operator group is adopted (same id, grants gone). Each adoption condition excludes a room on its own (L-042). A catch-all room loses its chats and the connector's room is NULL. A DM chat stays only in its operator DM
- [ ] `route_pending` set exactly for chats that were in a room and keep none (not for adopted, kept-DM or never-routed chats)
- [ ] Index present; duplicate transport insert fails; hook failure rolls everything back
- [ ] Gate check passes: `make check`
**Tests**: integration
**Gate**: build
**Commit**: `feat(store): upgrade rooms to one identity per connection`

#### T34: Upgraded chats get their conversation whatever the scope [core]

**What**: In `Pipeline.route`, a chat without a conversation also gets one when `route_pending` is set. Creating the conversation (`CreatePlatformGroup` or `EnsurePlatformDM`) clears the flag in the same transaction.
**Where**: `internal/connectors/autoroute.go`
**Depends on**: T33
**Reuses**: T8 routing, `UpsertChat`
**Requirement**: CID-14, CID-18
**Done when**:
- [ ] Integration: scope `off` + `route_pending` group chat → conversation created, message stored, flag cleared; the same for a DM chat
- [ ] Scope `off`, flag cleared, conversation deleted → next message keeps nothing
- [ ] Gate check passes: `make check`
**Tests**: integration
**Gate**: build
**Commit**: `feat(connectors): route chats that were routed before the upgrade`

#### T35: Upgrade end to end [core]

**What**: An e2e test boots the app on the pre-0018 fixture and checks the spec's CID-14 independent test through the API.
**Where**: `internal/api/upgrade_test.go`
**Depends on**: T34
**Reuses**: existing upgrade e2e harness
**Requirement**: CID-14
**Done when**:
- [ ] The mixed room's members list the owner and the two AIs, and no chat
- [ ] With the connection's scope `off`, the group chat's next inbound message lands in its operator's conversation (adopted room, or a new one for the mixed room's chat)
- [ ] `make e2e` passes
- [ ] Gate check passes: `make check`
**Tests**: e2e
**Gate**: build
**Commit**: `test(api): pin the connection identity upgrade`

#### T36: Docs and v0.5.0 release notes [small]

**What**: Update the README(s) and `docs/agents.md` to the one-identity model, and write the breaking-change notes. The notes cover the operator rule, the retired features, CID-18 scope behaviour, WhatsApp one instance and notify narrowing.
**Where**: `README.md`
**Depends on**: T35
**Reuses**: none
**Requirement**: CID-15
**Done when**:
- [ ] No doc mentions route-to-agent, catch-all rooms, bridges or `chat_ids`; no private names
- [ ] Gate check passes: `make check`
**Tests**: none
**Gate**: build
**Commit**: `docs: one identity per connection (v0.5.0)`

---

## Phase Execution Map

```
Phase 1 -> Phase 2 -> Phase 3 -> Phase 4 -> Phase 5 -> Phase 6 -> Phase 7
```

Batches (~7 tasks, whole phases): [P1] 6, [P2] 6, [P3 + P4] 9, [P5] 5, [P6] 6, [P7] 4. Phase 5
(web) and the [small] tasks of Phase 6 suit a cheaper worker; Phases 1, 2, 4 and T33-T35 need a
high-reasoning worker.

---

## Task Granularity Check

| Task | Scope | Status |
| --- | --- | --- |
| T1-T6 | one helper / rule / service method / mapping each | ✅ |
| T7-T12 | one function, policy or query each | ✅ |
| T13-T17 | one service rule, mapping or DTO field each | ✅ |
| T18-T21 | one tx helper or one call site each | ✅ |
| T22-T26 | one component each | ✅ |
| T27-T32 | one adapter or one retired feature each | ✅ (T30: migration + its queries, cohesive) |
| T33-T36 | migration hook, routing flag, e2e, docs | ✅ |

## Diagram-Definition Cross-Check

| Task | Depends On (task body) | Diagram Shows | Status |
| --- | --- | --- | --- |
| T1 | None | — | ✅ |
| T2 | T1 | T1 -> T2 | ✅ |
| T3 | T2 | T2 -> T3 | ✅ |
| T4 | T3 | T3 -> T4 | ✅ |
| T5 | T3 | T3 -> T5 | ✅ |
| T6 | T5 | T5 -> T6 | ✅ |
| T7 | None | — | ✅ |
| T8 | T7 | T7 -> T8 | ✅ |
| T9 | None | — | ✅ |
| T10 | T9 | T9 -> T10 | ✅ |
| T11 | T9 | T9 -> T11 | ✅ |
| T12 | None | — | ✅ |
| T13 | None | — | ✅ |
| T14 | T13 | T13 -> T14 | ✅ |
| T15 | None | — | ✅ |
| T16 | T15 | T15 -> T16 | ✅ |
| T17 | None | — | ✅ |
| T18 | None | — | ✅ |
| T19 | None | — | ✅ |
| T20 | T18, T19 | T18 -> T20, T19 -> T20 | ✅ |
| T21 | T20 | T20 -> T21 | ✅ |
| T22-T29, T32 | None | — | ✅ |
| T30 | None | — | ✅ |
| T31 | T30 | T30 -> T31 | ✅ |
| T33 | None | — | ✅ |
| T34 | T33 | T33 -> T34 | ✅ |
| T35 | T34 | T34 -> T35 | ✅ |
| T36 | T35 | T35 -> T36 | ✅ |

## Test Co-location Validation

| Task | Code Layer Created/Modified | Matrix Requires | Task Says | Status |
| --- | --- | --- | --- | --- |
| T1 | testkit | integration | integration | ✅ |
| T2 | pure rules | unit | unit | ✅ |
| T3, T4, T5, T7, T8, T9, T13, T18, T19, T21, T34 | services | integration | integration | ✅ |
| T6, T10, T11, T12, T14, T15, T16, T17, T20, T28, T29, T31, T35 | routes / API | e2e | e2e | ✅ |
| T32 | channel tools | e2e | e2e | ✅ |
| T22-T26 | web components | unit | unit | ✅ |
| T27 | platform adapter | integration | integration | ✅ |
| T30, T33 | store / migrations | integration | integration | ✅ |
| T36 | docs | none | none | ✅ |
