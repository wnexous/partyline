# Routing and Context Tasks

## Execution Protocol (MANDATORY -- do not skip)

Implement these tasks with the `tlc-spec-driven` skill: **activate it by name and follow its Execute flow and Critical Rules.** Do not search for skill files by filesystem path. The skill is the source of truth for the full flow (per-task cycle, sub-agent delegation, adequacy review, Verifier, discrimination sensor).

**If the skill cannot be activated, STOP and tell the user - do not proceed without it.**

---

**Design**: `.specs/features/routing-context/design.md`
**Status**: Approved

---

## Test Coverage Matrix

> Guidelines found: `AGENTS.md`, all features' `contract-notes.md`, `.specs/LESSONS.md` (confirmed lessons apply).

| Code Layer | Required Test Type | Coverage Expectation | Location Pattern | Run Command |
| --- | --- | --- | --- | --- |
| Pure rules (`delivery.Wants`, title helpers) | unit | All branches; 1:1 ACs; boundaries | `internal/**/*_test.go` | `go test -count=1 ./internal/...` |
| Services, inbound pipeline, sender | integration | Every AC + error paths, real SQLite | `internal/**/*_test.go` | `make test` |
| HTTP/WS, channel tools | e2e (testkit) | Every route/tool: happy + edge + error; auth matrix for new routes | `internal/api/*_test.go`, `internal/channel/*_test.go` | `make test` |
| Platform adapters | integration against fakes built from captured real responses | Each new call | `internal/connectors/<kind>/*_test.go` | `make test` |
| Web components/pages | unit (Vitest) | Each visible AC | `web/src/**/*.test.tsx` | `cd web && bun run test` |

## Gate Check Commands

| Gate Level | When to Use | Command |
| --- | --- | --- |
| Quick | Unit-only tasks | `go test -count=1 ./internal/...` or `cd web && bun run test` |
| Full | Integration/e2e tasks | `make test` |
| Build | Last task of a phase and before every commit of orchestrator edits | `make check` |

---

## Execution Plan

### Phase 1: Data and settings

```
T1 → T2 → T3 → T4
```

### Phase 2: Delivery behavior

```
T5 → T6 → T7 → T8
```

### Phase 3: Channel and reactions

```
T9 → T10 → T11 → T12
```

### Phase 4: Web

```
T13 → T14 → T15 → T16 → T17
```

---

## Task Breakdown

### Phase 1: Data and settings

#### T1: Migration, queries and DTO contract

**What**: `0006_routing_context.sql` per design, sqlc queries, DTO additions (RoomMember settings, Room.instructions, Connector auto_route, ChatRef on Message/AgentMessage, RoomInfo, ReactRequest), tygo regen.
**Where**: `internal/store/migrations/0006_routing_context.sql`
**Depends on**: None
**Reuses**: migration runner, dto package
**Requirement**: MEM-05, INS-01, ROUTE-04 (shapes)
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] Migration test: defaults applied to existing rows; CHECK constraints reject bad values
- [x] `make generate` drift-free; web typecheck still passes
**Tests**: integration
**Gate**: full
**Commit**: `feat(store): add routing and context columns`

#### T2: Agent membership settings API

**What**: `rooms.UpdateMember` accepts display_name, sign_outbound, delivery, keywords for agent members (422 for other kinds and for MEM-05 limits); responses carry them.
**Where**: `internal/rooms/members.go`
**Depends on**: T1
**Reuses**: auth matrix helper
**Requirement**: MEM-05
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] Manager can set each field; viewer 403; stranger 404; 65-char name / 21 keywords → 422
**Tests**: e2e
**Gate**: full
**Commit**: `feat(rooms): add per-room agent membership settings`

#### T3: Room instructions

**What**: instructions on create/update (≤4,000 chars, 422 beyond), returned by room endpoints.
**Where**: `internal/rooms/rooms.go`
**Depends on**: T2
**Reuses**: rooms service
**Requirement**: INS-01
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] 4,000 chars saved; 4,001 → 422; only `manage` can change
**Tests**: e2e
**Gate**: full
**Commit**: `feat(rooms): add room instructions`

#### T4: Connector catch-all settings API

**What**: connector update accepts `auto_route_scope` and `auto_route_room_id`; room must exist and the caller needs `manage` on it (403 otherwise); audit entry.
**Where**: `internal/connectors/service.go`
**Depends on**: T3
**Reuses**: access authorizer
**Requirement**: ROUTE-04
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] Valid settings saved; room without manage → 403; unknown room → 422; scope outside enum → 422
**Tests**: e2e
**Gate**: build
**Commit**: `feat(connectors): add catch-all routing settings`

### Phase 2: Delivery behavior

#### T5: Delivery filter

**What**: `delivery.Wants` (pure) + dispatcher and gateway backlog apply it per agent member; skipped backlog messages still advance the cursor.
**Where**: `internal/delivery/wants.go`
**Depends on**: T4
**Reuses**: dispatcher, gateway
**Requirement**: MEM-03, MEM-04
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] Table tests: all/trusted/mentions × DM, reply-to-me, @name (case, word boundary), keyword boundary, non-matching
- [x] e2e: agent with `mentions` receives only matching pushes; backlog skips non-matching and cursor advances
**Tests**: integration
**Gate**: full
**Commit**: `feat(delivery): filter agent deliveries by membership mode`

#### T6: Outbound signing and display name

**What**: sender sets `Outbound.AuthorName` from member settings (empty when unsigned, display name when set); timeline `author_name` uses display name for agent messages.
**Where**: `internal/connectors/sender.go`
**Depends on**: T5
**Reuses**: AuthorLine
**Requirement**: MEM-01, MEM-02
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] Fake connector receives no header when unsigned, display name when set, agent name by default
**Tests**: integration
**Gate**: full
**Commit**: `feat(connectors): honor agent signature and display name outbound`

#### T7: Catch-all inbound routing

**What**: inbound pipeline auto-adds unrouted chats matching scope to the catch-all room (idempotent under concurrency), 24 h skip after manual removal, `auto-route room unavailable` status when invalid.
**Where**: `internal/connectors/inbound.go`
**Depends on**: T6
**Reuses**: rooms.AddMember, identities
**Requirement**: ROUTE-01, ROUTE-02, ROUTE-03, ROUTE-05
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] DM with scope `dms` reaches the agent; group does not; chat in another room not added
- [x] Two concurrent first messages → one membership; removal → not re-added for 24 h (fake clock)
- [x] Deleted room → status detail set, nothing stored; auto-routed sender `trusted=false`
**Tests**: integration
**Gate**: full
**Commit**: `feat(connectors): route new chats to a catch-all room`

#### T8: Chat titles and room names

**What**: WhatsApp group subject via `/group/info` (cached 10 min) and DM title fallback to number; Telegram titles; title updates on change; routed room named after title.
**Where**: `internal/connectors/whatsapp/connector.go`
**Depends on**: T7
**Reuses**: captured real `/group/info` shape (design)
**Requirement**: CTX-05, CTX-06, CTX-07, CTX-08
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] Fake evolution-go built from the real response shape: group chat gets its Name; DM without push name gets the number
- [x] Title change updates the chat; `/chats/{id}/route` names the room after the title
**Tests**: integration
**Gate**: build
**Commit**: `feat(connectors): name chats and routed rooms after their titles`

### Phase 3: Channel and reactions

#### T9: Chat context in agent messages

**What**: `Message.chat`/`AgentMessage.chat` filled for chat-authored messages; channel meta adds chat keys; history items carry them; instructions text updated.
**Where**: `internal/channel/format.go`
**Depends on**: T8
**Reuses**: api/convert
**Requirement**: CTX-01, CTX-02, CTX-03, CTX-04
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [ ] Exact meta for chat, agent, user messages; history JSON has chat fields; instructions mention them
**Tests**: e2e
**Gate**: full
**Commit**: `feat(channel): tell agents which chat a message came from`

#### T10: Room info tool and instructions notice

**What**: `GET /agent/rooms/{room}/info`, `room_info` tool, welcome notice for rooms with instructions, `send_message` `chat_ids` alias.
**Where**: `internal/channel/tools.go`
**Depends on**: T9
**Reuses**: agent REST conventions
**Requirement**: INS-02, INS-03, CTX-04
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [ ] Tool returns name, instructions, members, own settings; non-member → error text; notice once per room on connect
**Tests**: e2e
**Gate**: full
**Commit**: `feat(channel): add room info tool and instructions notice`

#### T11: React tool and outbox path

**What**: `POST /agent/messages/{id}/react`, `react` tool, reaction stored as message kind `reaction`, sender routes it to `connectors.Reactor`.
**Where**: `internal/api/agent_handlers.go`
**Depends on**: T10
**Reuses**: outbox, sender
**Requirement**: REACT-01, REACT-02
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [ ] Reaction to a chat message reaches the fake Reactor with target external id; non-chat target → `can only react to chat messages`
**Tests**: e2e
**Gate**: full
**Commit**: `feat(api): let agents react to chat messages`

#### T12: Platform reactions

**What**: Reactor in WhatsApp (`/message/react`), Telegram (`setMessageReaction`), Discord (`MessageReactionAdd`), webhook (`event: reaction`).
**Where**: `internal/connectors/whatsapp/react.go`
**Depends on**: T11
**Reuses**: adapters' clients
**Requirement**: REACT-01
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [ ] Each fake receives the exact platform payload (WhatsApp includes participant only for groups)
**Tests**: integration
**Gate**: build
**Commit**: `feat(connectors): send reactions on every platform`

### Phase 4: Web

#### T13: Agent member settings popover

**What**: members panel settings for agent members (display name, sign switch, delivery select, keywords).
**Where**: `web/src/components/member-settings.tsx`
**Depends on**: T12
**Reuses**: member-list, form-fields
**Requirement**: MEM-01, MEM-02, MEM-03, MEM-04, MEM-05
**Tools**: MCP: NONE; Skill: `impeccable`
**Done when**:
- [ ] PATCH payload per field; 422 reasons shown; only managers see the control
**Tests**: unit
**Gate**: quick
**Commit**: `feat(web): add agent membership settings`

#### T14: Room instructions editor

**What**: instructions textarea with counter in the room settings sheet.
**Where**: `web/src/components/room-settings.tsx`
**Depends on**: T13
**Reuses**: settings sheet
**Requirement**: INS-01
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [ ] Saves; 4,001 chars blocked client-side and 422 mapped
**Tests**: unit
**Gate**: quick
**Commit**: `feat(web): edit room instructions`

#### T15: Catch-all card on the connector page

**What**: scope select + room picker (managed rooms) + exposure warning.
**Where**: `web/src/components/connector-catch-all.tsx`
**Depends on**: T14
**Reuses**: queries
**Requirement**: ROUTE-01, ROUTE-04
**Tools**: MCP: NONE; Skill: `impeccable`
**Done when**:
- [ ] Saving sends both fields; warning visible when scope ≠ off; 403 shown
**Tests**: unit
**Gate**: quick
**Commit**: `feat(web): configure catch-all routing`

#### T16: Chat titles in the timeline and members

**What**: chat messages show `message.chat.title` + platform badge; member list and add-member picker show chat titles.
**Where**: `web/src/components/message-item.tsx`
**Depends on**: T15
**Reuses**: platformLabel
**Requirement**: CTX-07
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [ ] Title and badge rendered; untitled chat falls back to `<Platform> chat`
**Tests**: unit
**Gate**: build
**Commit**: `feat(web): show chat titles in rooms`

#### T17: Delete a room

**What**: "Delete room" action in the room settings sheet for managers, alert-dialog confirmation, navigate to `/rooms`, toast, cache updated.
**Where**: `web/src/components/room-settings-sheet.tsx`
**Depends on**: T16
**Reuses**: `useDeleteRoom`, `ui/alert-dialog.tsx`
**Requirement**: ROOMDEL-01, ROOMDEL-02
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [ ] Manager sees the action; viewer does not; confirm calls DELETE, navigates to /rooms and the room is gone from the list
**Tests**: unit
**Gate**: build
**Commit**: `feat(web): delete rooms from the room settings`

