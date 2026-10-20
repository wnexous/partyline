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

### Phase 5: Verifier fixes and hardening (fix iteration 1)

```
T18 → T19 → T20 → T21 → T22 → T23 → T24 → T25 → T26 → T27 → T28 → T29 → T30
```

### Phase 6: Web hardening and records

```
T31 → T32 → T33 → T34
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
- [x] Exact meta for chat, agent, user messages; history JSON has chat fields; instructions mention them
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
- [x] Tool returns name, instructions, members, own settings; non-member → error text; notice once per room on connect
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
- [x] Reaction to a chat message reaches the fake Reactor with target external id; non-chat target → `can only react to chat messages`
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
- [x] Each fake receives the exact platform payload (WhatsApp includes participant only for groups)
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
- [x] PATCH payload per field; 422 reasons shown; only managers see the control
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
- [x] Saves; 4,001 chars blocked client-side and 422 mapped
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
- [x] Saving sends both fields; warning visible when scope ≠ off; 403 shown
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
- [x] Title and badge rendered; untitled chat falls back to `<Platform> chat`
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
- [x] Manager sees the action; viewer does not; confirm calls DELETE, navigates to /rooms and the room is gone from the list
**Tests**: unit
**Gate**: build
**Commit**: `feat(web): delete rooms from the room settings`

### Phase 5: Verifier fixes and hardening (fix iteration 1)

#### T18: Reply-to-me names this agent (G5)

**What**: Two agents in mentions mode; a reply to agent A's message reaches A only, live and in the backlog.
**Where**: `internal/api/delivery_filter_test.go`
**Depends on**: T17
**Reuses**: existing test helpers (contract notes)
**Requirement**: MEM-03
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] Test kills mutant G5 (reply-to-me true for a reply to any agent)
**Tests**: e2e
**Gate**: build
**Commit**: `test(delivery): prove replies reach only the replied agent`

#### T19: Instructions notice wording and paused rooms (G23, G26)

**What**: INS-03 states the literal notice text; tests compare to that literal; a paused room with instructions still sends the notice and no messages.
**Where**: `internal/api/room_info_test.go`, `internal/channel/room_info_test.go`, `spec.md`
**Depends on**: T18
**Reuses**: existing test helpers (contract notes)
**Requirement**: INS-03
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] Tests kill G23 (notice text replaced) and G26 (no notice for paused rooms)
**Tests**: e2e
**Gate**: build
**Commit**: `test(agents): pin the instructions notice wording and paused rooms`

#### T20: React to a listen-only chat

**What**: Reactions honor outbound=false like messages: 422 `recipients: listen-only`, nothing stored.
**Where**: `internal/api/react_test.go`, `spec.md`
**Depends on**: T19
**Reuses**: existing test helpers (contract notes)
**Requirement**: REACT-03
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] 422 field recipients "listen-only"; no message stored; no outbox row
**Tests**: e2e
**Gate**: build
**Commit**: `test(api): refuse reactions to listen-only chats`

#### T21: Unsigned webhook reactions omit the author

**What**: Webhook reaction payload omits `message.author_name` when the agent member has `sign_outbound` off.
**Where**: `internal/connectors/webhook/connector.go`, `internal/api/hook_reaction_test.go`
**Depends on**: T20
**Reuses**: existing test helpers (contract notes)
**Requirement**: REACT-04
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] Payload has no author_name key when unsigned; signed payload unchanged
**Tests**: e2e
**Gate**: build
**Commit**: `fix(connectors): omit the author of unsigned webhook reactions`

#### T22: Migration 0008 and DTO

**What**: `rooms.broadcast` (default 1) and `rooms.instructions_by`; DTO Room.broadcast, UpdateRoomRequest.broadcast, RoomInfo.instructions_by/instructions_trusted, Message.warning; tygo regen.
**Where**: `internal/store/migrations/0008_room_safety.sql`
**Depends on**: T21
**Reuses**: existing test helpers (contract notes)
**Requirement**: ROUTE-06, INS-04 (shapes)
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] Migration test: defaults on existing rows
- [x] `make generate` drift-free
**Tests**: integration
**Gate**: build
**Commit**: `feat(store): add room broadcast and instructions author`

#### T23: No broadcast in catch-all rooms

**What**: Rooms with broadcast=false queue posts without recipients to no chat (replies go to the replied chat) and answer a warning; choosing a room as catch-all forces broadcast and bridge off; PATCH broadcast/bridge_chats true answers 422 while it is a catch-all target.
**Where**: `internal/rooms/messages.go`, `internal/rooms/rooms.go`, `internal/connectors/service.go`, `internal/api/*`
**Depends on**: T22
**Reuses**: existing test helpers (contract notes)
**Requirement**: ROUTE-06, ROUTE-07
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] Service + HTTP tests: no outbox rows, stored, pushed to agents, exact warning text; reply goes to the replied chat; forcing; 422s; second owner
**Tests**: e2e
**Gate**: build
**Commit**: `feat(rooms): stop broadcasts in catch-all rooms`

#### T24: Channel tool says when nothing went to a chat

**What**: `send_message`, `reply` and `send_file` results append the warning.
**Where**: `internal/channel/tools.go`
**Depends on**: T23
**Reuses**: existing test helpers (contract notes)
**Requirement**: ROUTE-06
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] Tool result text exact for a broadcast=false room; unchanged otherwise
**Tests**: e2e
**Gate**: build
**Commit**: `feat(channel): tell agents when a post reached no chat`

#### T25: Catch-all caps

**What**: At most 60 auto-added chats per connector per hour and 1,000 chats per catch-all room; beyond that the chat is not added, nothing is stored, status detail `auto-route limit reached`.
**Where**: `internal/connectors/inbound.go`
**Depends on**: T24
**Reuses**: existing test helpers (contract notes)
**Requirement**: ROUTE-08
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] Fake clock: 61st chat in the hour refused, accepted after the hour; room at 1,000 chats refused; detail set
**Tests**: integration
**Gate**: build
**Commit**: `feat(connectors): cap catch-all routing`

#### T26: Clear sticky auto-route status

**What**: A successful route or a catch-all settings change clears `auto-route room unavailable` / `auto-route limit reached` and publishes the status.
**Where**: `internal/connectors/inbound.go`, `internal/connectors/service.go`
**Depends on**: T25
**Reuses**: existing test helpers (contract notes)
**Requirement**: ROUTE-09
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] Detail cleared after success and after PATCH; other details untouched
**Tests**: integration
**Gate**: build
**Commit**: `fix(connectors): clear auto-route status once routing works again`

#### T27: No room-existence oracle

**What**: Connector PATCH with an unknown or hidden room answers the same 403 `forbidden` message.
**Where**: `internal/connectors/service.go`, `internal/api/catch_all_settings_test.go`
**Depends on**: T26
**Reuses**: existing test helpers (contract notes)
**Requirement**: ROUTE-04
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] Unknown and hidden ids answer identical status, code and message
**Tests**: e2e
**Gate**: build
**Commit**: `fix(connectors): answer unknown and hidden catch-all rooms alike`

#### T28: One emoji grapheme

**What**: `react` accepts exactly one emoji grapheme (pictographic, VS16, skin tones, ZWJ, keycaps, flags).
**Where**: `internal/validation/emoji.go`
**Depends on**: T27
**Reuses**: existing test helpers (contract notes)
**Requirement**: REACT-05
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] Table test accepts and rejects each class
**Tests**: unit
**Gate**: build
**Commit**: `fix(api): accept only one emoji per reaction`

#### T29: Instructions trust

**What**: Store `instructions_by`; room_info returns `instructions_by` and `instructions_trusted`; the tool labels untrusted instructions as data; the notice says whether the writer is trusted.
**Where**: `internal/rooms/rooms.go`, `internal/api/agent_handlers.go`, `internal/channel/tools.go`, `internal/agents/gateway.go`
**Depends on**: T28
**Reuses**: existing test helpers (contract notes)
**Requirement**: INS-03, INS-04, INS-05
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] A second owner's edit is untrusted for alice's agent; the owner's edit is trusted; literal notice texts
**Tests**: e2e
**Gate**: build
**Commit**: `feat(rooms): mark who wrote room instructions`

#### T30: Mask DM numbers in titles

**What**: The DM title fallback shows only the last 4 digits of the number.
**Where**: `internal/connectors/whatsapp/parse.go`
**Depends on**: T29
**Reuses**: existing test helpers (contract notes)
**Requirement**: CTX-06
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] Title `+•••• 0000`; chat id unchanged
**Tests**: integration
**Gate**: build
**Commit**: `fix(connectors): mask phone numbers in DM titles`

### Phase 6: Web hardening and records

#### T31: Broadcast-off rooms in the web app

**What**: Composer hint "Answer a chat explicitly" and a required recipient when broadcast is off; settings show the broadcast state (locked while the room is a catch-all target).
**Where**: `web/src/components/composer.tsx`, `web/src/components/room-settings-sheet.tsx`
**Depends on**: T30
**Reuses**: existing test helpers (contract notes)
**Requirement**: ROUTE-06, ROUTE-07
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] Send disabled until a chat is picked; hint visible; settings reflect state; 422 mapped
**Tests**: unit
**Gate**: build
**Commit**: `feat(web): require an explicit chat in catch-all rooms`

#### T32: Mentions is not a security filter

**What**: Delivery select help text and channel instructions (+ pt-BR).
**Where**: `web/src/components/member-settings.tsx`, `internal/channel/instructions.go`
**Depends on**: T31
**Reuses**: existing test helpers (contract notes)
**Requirement**: MEM-06
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] Exact help text in both locales; instructions contain the sentence
**Tests**: unit
**Gate**: build
**Commit**: `docs(web): say mentions mode is not a security filter`

#### T33: Form accessibility fixes

**What**: aria-live + aria-describedby for keyword and scope errors; commit a typed keyword on Save; instructions counter described; catch-all Save explains why it is disabled.
**Where**: `web/src/components/member-settings.tsx`, `connector-catch-all.tsx`, `room-settings-sheet.tsx`
**Depends on**: T32
**Reuses**: existing test helpers (contract notes)
**Requirement**: A11Y-01, A11Y-02, A11Y-03, A11Y-04
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] Vitest assertions per item
**Tests**: unit
**Gate**: build
**Commit**: `fix(web): announce form errors and keep typed keywords`

#### T34: Contract notes and traceability

**What**: contract-notes and spec traceability updated for T18-T33.
**Where**: `.specs/features/routing-context/`
**Depends on**: T33
**Reuses**: existing test helpers (contract notes)
**Requirement**: all
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [ ] Notes cover every new behavior
**Tests**: none
**Gate**: build
**Commit**: `docs(specs): record routing-context fix iteration 1`
