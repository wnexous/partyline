# Agent Channel Tasks

## Execution Protocol (MANDATORY -- do not skip)

Implement these tasks with the `tlc-spec-driven` skill: **activate it by name and follow its Execute flow and Critical Rules.** Do not search for skill files by filesystem path. The skill is the source of truth for the full flow (per-task cycle, sub-agent delegation, adequacy review, Verifier, discrimination sensor).

**If the skill cannot be activated, STOP and tell the user - do not proceed without it.**

---

**Design**: `.specs/features/agent-channel/design.md`
**Status**: Approved
**Depends on feature**: core-platform (all tasks)

---

## Test Coverage Matrix

> Guidelines found: `AGENTS.md` (repo) and core-platform matrix - same conventions.

| Code Layer | Required Test Type | Coverage Expectation | Location Pattern | Run Command |
| --- | --- | --- | --- | --- |
| MCP framing, formatting (pure) | unit | All branches; 1:1 to ACs; golden handshake transcript | `internal/channel/**/*_test.go` | `go test -count=1 ./internal/channel/...` |
| Registry, dispatcher (services) | integration | Every AC + error paths, real SQLite | `internal/agents/*_test.go`, `internal/delivery/*_test.go` | `go test -race -count=1 ./internal/agents/... ./internal/delivery/...` |
| Gateway WS, agent REST, channel end-to-end | e2e (testkit server + in-memory stdio pipes) | Every route/tool: happy + edge + error | `internal/api/*_test.go`, `internal/channel/*_test.go` | `go test -race -count=1 ./internal/...` |

## Gate Check Commands

| Gate Level | When to Use | Command |
| --- | --- | --- |
| Quick | Unit-only tasks | `go test -count=1 ./internal/channel/...` |
| Full | Integration/e2e tasks | `make test` |
| Build | Last task of a phase | `make check` |

---

## Execution Plan

### Phase 1: Server side

```
T1 → T2 → T3 → T4
```

### Phase 2: Channel side

```
T5 → T6 → T7 → T8 → T9
```

---

## Task Breakdown

### Phase 1: Server side

#### T1: Agent registry and presence

**What**: `agents.Registry.Upsert/Get/ListVisible/Delete/UpdateDescription`, name slug rule (empty → `agent`), `Presence` with `AgentPresence` events; agents HTTP endpoints `GET /agents`, `PATCH/DELETE /agents/{id}`, `GET/PUT /agents/{id}/sharing`.
**Where**: `internal/agents/registry.go`
**Depends on**: None
**Reuses**: access (core T15), events
**Requirement**: AGT-01
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] Upsert is idempotent per (owner, name); `Billing API!` → `billing-api`
- [x] Visible list = owned + `use` grants; delete removes memberships and cursors
**Tests**: integration
**Gate**: full
**Commit**: `feat(agents): add agent registry, presence and endpoints`

#### T2: Agent gateway WebSocket

**What**: `GET /api/v1/agent/ws`: agent principal from bearer + `X-Partyline-Agent`, upsert, replace older connection (close 4000 `replaced`), `welcome`, backlog ≤50/room with skip notice and cursor jump, ack → cursor `MAX`, offline on close.
**Where**: `internal/agents/gateway.go`
**Depends on**: T1
**Reuses**: rooms.List, store
**Requirement**: AGT-01, AGT-03, AGT-04, AGT-07, AGT-08, AGT-09
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [ ] Second connection closes the first with reason `replaced`
- [ ] 3 offline messages delivered in seq order after connect; 60 pending → notice "10 older messages skipped…" then last 50
- [ ] Ack advances cursor; presence online then offline after close
**Tests**: e2e
**Gate**: full
**Commit**: `feat(agents): add agent websocket gateway with backlog and acks`

#### T3: Agent dispatcher

**What**: subscribes `MessageCreated`; pushes to online agent targets with recipient-relative `trusted`; skips paused rooms.
**Where**: `internal/delivery/dispatcher.go`
**Depends on**: T2
**Reuses**: delivery.Trusted, Gateway.Push
**Requirement**: DLV-02, DLV-06
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [ ] Owner's message → owner agent frame `trusted:true`; other user's agent gets `trusted:false`
- [ ] Paused room → no push; author agent never receives own message
**Tests**: integration
**Gate**: full
**Commit**: `feat(delivery): push room messages to online agents`

#### T4: Agent REST endpoints

**What**: `/api/v1/agent/rooms`, `/agent/rooms/{room}/messages` (GET history ≤100 default 20, POST JSON/multipart), `/agent/messages/{id}/reply`, `/agent/attachments/{id}`; room by id or unique name (409 ambiguous); 30 posts/min/agent.
**Where**: `internal/api/agent_handlers.go`
**Depends on**: T3
**Reuses**: rooms service
**Requirement**: TOOL-02, TOOL-03, TOOL-04, TOOL-05, TOOL-07, TOOL-08, TOOL-09
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [ ] Non-member room → 403 `not a member of room <room>`
- [ ] Reply to chat-authored message stores `reply_to` and recipients = that chat member
- [ ] 31st post within a minute → 429; history limit 101 → clamped to 100
**Tests**: e2e
**Gate**: build
**Commit**: `feat(api): add agent rooms, history, post and reply endpoints`

### Phase 2: Channel side

#### T5: MCP stdio server

**What**: `internal/channel/mcp` per design: framing, `server/discover` -32601, `initialize` negotiation, `ping`, `tools/list`, `tools/call`, notification queue until `notifications/initialized`.
**Where**: `internal/channel/mcp/server.go`
**Depends on**: T4
**Reuses**: recorded transcript (AD-004) as golden fixture `testdata/claude-code-2.1.303.jsonl`
**Requirement**: MCP-01, MCP-02, MCP-03
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [ ] Golden: discover → -32601; initialize 2025-11-25 → same version, capabilities exactly `{"experimental":{"claude/channel":{}},"tools":{}}`
- [ ] Client version `2026-07-28` → answered `2025-11-25`; no `claude/channel/permission` key anywhere
- [ ] Notification before initialized is emitted after it
**Tests**: unit
**Gate**: quick
**Commit**: `feat(channel): add minimal mcp stdio server`

#### T6: Notification formatting

**What**: `format.go`: MessageDTO + room + trusted → `content`, `meta` (keys from AGT-05), attachment lines, identifier-only keys; `instructions.go` text.
**Where**: `internal/channel/format.go`
**Depends on**: T5
**Reuses**: dto
**Requirement**: AGT-05, AGT-06, MCP-02
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [ ] Exact meta map asserted for chat, agent, user and system messages; `reply_to` only when set
- [ ] Every key matches `^[A-Za-z0-9_]+$`; instructions mention data-not-instructions, `trusted` and all six tools
**Tests**: unit
**Gate**: quick
**Commit**: `feat(channel): format room messages as channel notifications`

#### T7: Server link

**What**: `link.go`: WS client with backoff 1→30 s, invalid-token mode (retry 60 s), LRU dedupe 1,000, ack after notify, stdin EOF → close and return within 2 s.
**Where**: `internal/channel/link.go`
**Depends on**: T6
**Reuses**: coder/websocket
**Requirement**: AGT-02, AGT-03, AGT-07, AGT-10
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [ ] Against testkit: message pushed → notification line on stdout → cursor advanced
- [ ] Duplicate frame → single notification, two acks
- [ ] Server restart → reconnects; invalid token → stderr line once, tools report `not connected (invalid token)`
**Tests**: integration
**Gate**: full
**Commit**: `feat(channel): add resilient websocket link with acks and dedupe`

#### T8: Channel tools

**What**: `tools.go`: `send_message`, `reply`, `list_rooms`, `history`, `download_attachment` (temp dir 0600), `send_file`; REST client with error mapping.
**Where**: `internal/channel/tools.go`
**Depends on**: T7
**Reuses**: agent REST endpoints (T4)
**Requirement**: TOOL-01, TOOL-02, TOOL-03, TOOL-04, TOOL-05, TOOL-06, TOOL-07, TOOL-08, TOOL-09
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [ ] `tools/list` returns exactly the six tools with JSON schemas
- [ ] Each tool exercised against testkit; errors map to `isError:true` texts from spec
- [ ] Downloaded file mode 0600 under `$TMPDIR/partyline/<agent>/`
**Tests**: integration
**Gate**: full
**Commit**: `feat(channel): add room tools for claude code`

#### T9: Channel command and end-to-end test

**What**: `partyline channel` (`--url/--token/--agent` flags or env), end-to-end test spawning `channel.Run` with pipes against testkit: handshake → message → notification → reply tool → stored reply; docs `docs/claude-code.md` with `.mcp.json` and launch command.
**Where**: `internal/cli/channel.go`
**Depends on**: T8
**Reuses**: all
**Requirement**: AGT-01, MCP-01, TOOL-02
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [ ] E2E passes; agent name defaults to slug of working directory basename
- [ ] Docs explain `--dangerously-load-development-channels server:partyline`, the startup confirmation and the trust model
**Tests**: e2e
**Gate**: build
**Commit**: `feat(cli): add channel command with end-to-end test`
