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

### Phase 3: Verifier fix iteration 1

```
T10 → T11 → T12 → T13 → T14 → T15 → T16 → T17 → T18
```

### Phase 4: Verifier fix iteration 2

```
T19 → T20 → T21 → T22 → T23 → T24 → T25 → T26 → T27
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
- [x] Second connection closes the first with reason `replaced`
- [x] 3 offline messages delivered in seq order after connect; 60 pending → notice "10 older messages skipped…" then last 50
- [x] Ack advances cursor; presence online then offline after close
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
- [x] Owner's message → owner agent frame `trusted:true`; other user's agent gets `trusted:false`
- [x] Paused room → no push; author agent never receives own message
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
- [x] Non-member room → 403 `not a member of room <room>`
- [x] Reply to chat-authored message stores `reply_to` and recipients = that chat member
- [x] 31st post within a minute → 429; history limit 101 → clamped to 100
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
- [x] Golden: discover → -32601; initialize 2025-11-25 → same version, capabilities exactly `{"experimental":{"claude/channel":{}},"tools":{}}`
- [x] Client version `2026-07-28` → answered `2025-11-25`; no `claude/channel/permission` key anywhere
- [x] Notification before initialized is emitted after it
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
- [x] Exact meta map asserted for chat, agent, user and system messages; `reply_to` only when set
- [x] Every key matches `^[A-Za-z0-9_]+$`; instructions mention data-not-instructions, `trusted` and all six tools
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
- [x] Against testkit: message pushed → notification line on stdout → cursor advanced
- [x] Duplicate frame → single notification, two acks
- [x] Server restart → reconnects; invalid token → stderr line once, tools report `not connected (invalid token)`
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
- [x] `tools/list` returns exactly the six tools with JSON schemas
- [x] Each tool exercised against testkit; errors map to `isError:true` texts from spec
- [x] Downloaded file mode 0600 under `$TMPDIR/partyline/<agent>/`
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
- [x] E2E passes; agent name defaults to slug of working directory basename
- [x] Docs explain `--dangerously-load-development-channels server:partyline`, the startup confirmation and the trust model
**Tests**: e2e
**Gate**: build
**Commit**: `feat(cli): add channel command with end-to-end test`

### Phase 3: Verifier fix iteration 1

#### T10: Load-tolerant testkit and api waits (F1)

**What**: The testkit dial and ack timeouts and the api test frame waits (`wsWait`, `liveWait`) become one minute; every wait returns as soon as its frame arrives, so only a loaded host notices. `make test` already has `-timeout 20m` (core T45).
**Where**: `internal/testkit/live.go`, `internal/testkit/agent.go`, `internal/api/agent_ws_test.go`, `internal/api/live_test.go`
**Depends on**: None
**Reuses**: frameStream
**Requirement**: gate stability (no AC)
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] `TestDispatchPushesWithRecipientRelativeTrust`, `TestAgentRoomsListsMembersWithPresence` and `TestDispatchSkipsPausedRoom` pass with `-race -count=5` under load
**Tests**: e2e
**Gate**: full
**Commit**: `test(testkit): give websocket dials and frame waits a load-tolerant deadline`

#### T11: Ack of a foreign room's message (F2, M16)

**What**: A gateway test acks a message of a room the agent is not a member of and asserts no cursor row appears, while an ack in its own room advances.
**Where**: `internal/api/agent_ws_test.go`
**Depends on**: T10
**Reuses**: testkit AgentConn.Ack
**Requirement**: AGT-07
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] An ack for a non-member room's message leaves no cursor for that room; an ack in a member room advances the cursor
**Tests**: e2e
**Gate**: full
**Commit**: `test(api): ignore agent acks for rooms the agent is not in`

#### T12: Live push overlapping the backlog (F5, M17)

**What**: A gateway test queues a live push of a message the backlog already sent and asserts the agent receives it once.
**Where**: `internal/api/agent_ws_test.go`
**Depends on**: T11
**Reuses**: Gateway.Push
**Requirement**: AGT-08
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] A message delivered by the backlog and pushed live again arrives as exactly one frame; a newer pushed message still arrives
**Tests**: e2e
**Gate**: full
**Commit**: `test(api): send a message once when a live push overlaps the backlog`

#### T13: Agent sockets close on token revocation and user disable

**What**: The gateway subscribes to `SessionsRevoked`: revoking the agent's token or disabling its owner closes the agent socket at once (1008); after a lagged bus it re-checks the token.
**Where**: `internal/agents/gateway.go`
**Depends on**: T12
**Reuses**: events.SessionsRevoked.Revokes, users.PrincipalActive
**Requirement**: AGT-02, AUTH-10 (core)
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] Revoking the agent token closes the agent socket within 1 s with 1008; another agent token's socket stays open
- [x] Disabling the owner closes its agent sockets within 1 s
- [x] After a lagged bus, a socket whose token was revoked without an event closes (1008)
**Tests**: e2e
**Gate**: full
**Commit**: `fix(agents): close agent sockets when their token is revoked`

#### T14: Real instructions in the initialize answer (F3, M21)

**What**: The channel command's initialize answer carries `channel.Instructions`.
**Where**: `internal/cli/channel_test.go`
**Depends on**: T13
**Reuses**: mcpClient
**Requirement**: MCP-02
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] `partyline channel`'s initialize result has `instructions` equal to `channel.Instructions`
**Tests**: e2e
**Gate**: full
**Commit**: `test(cli): assert the channel instructions in the initialize answer`

#### T15: Reconnect backoff in the link loop (F4, M15)

**What**: `link.run` waits through an injectable function; a test records the waits across repeated dial failures.
**Where**: `internal/channel/link.go`
**Depends on**: T14
**Reuses**: timing
**Requirement**: AGT-03
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] Repeated dial failures wait 1, 2, 4, 8, 16, 30, 30, 30 s (recorded, not slept) and the link reports the server unreachable
**Tests**: integration
**Gate**: full
**Commit**: `test(channel): assert the reconnect backoff of the link loop`

#### T16: Slugified download dir (F6, M20b)

**What**: A test runs the channel with agent `../Night Shift` and asserts downloads land in `<TempDir>/partyline/night-shift`.
**Where**: `internal/channel/tools_test.go`
**Depends on**: T15
**Reuses**: agents.Slug
**Requirement**: AGT-01, TOOL-06
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] With agent `../Night Shift` the download path is under `<TempDir>/partyline/night-shift/`
**Tests**: integration
**Gate**: full
**Commit**: `test(channel): slugify the agent name in the download directory`

#### T17: Private download directory (F7, S1)

**What**: `download_attachment` creates `<tmp>/partyline/<agent>` with 0700 and refuses a directory that is a symlink, not owned by the user or open to group/others; files are written to a fresh temp file (O_EXCL, 0600) and renamed into place, so a planted symlink is never followed.
**Where**: `internal/channel/tools.go`, `internal/channel/private_unix.go`
**Depends on**: T16
**Reuses**: safeName
**Requirement**: TOOL-06
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] A symlinked or group/other-accessible download dir → isError, nothing written through it
- [x] A symlink planted at the target file name is replaced, its target untouched; the file is 0600
**Tests**: integration
**Gate**: full
**Commit**: `fix(channel): keep attachment downloads in a private directory`

#### T18: Contract notes and traceability for fix iteration 1

**What**: Record the gateway revocation, the link wait hook and the download dir rules; update traceability.
**Where**: `contract-notes.md`
**Depends on**: T17
**Reuses**: none
**Requirement**: all
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] contract-notes.md describes the revocation close, the private download dir and the test deadlines
**Tests**: none
**Gate**: build
**Commit**: `docs(specs): record agent channel contract notes for fix iteration 1`

### Phase 4: Verifier fix iteration 2

#### T19: Symlinked download dir refused by the symlink check alone (F8, N1)

**What**: The symlinked `<TempDir>/partyline` case points at a 0700 directory owned by the user, so only the `Lstat` symlink check can refuse it; nothing is written into the target.
**Where**: `internal/channel/tools_unix_test.go`
**Depends on**: None
**Reuses**: newChanEnv, postFile
**Requirement**: TOOL-06 (S1 hardening)
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] A symlink to a private (0700, own) directory → isError naming the download directory; the target stays empty
- [x] The test fails with `os.Lstat` replaced by `os.Stat` (N1)
**Tests**: integration
**Gate**: full
**Commit**: `test(channel): refuse a symlinked download dir even when its target is private`

#### T20: Group- and other-readable agent dir (F9, N3)

**What**: Download dir cases with mode 0750 (group only) and 0705 (others only) are refused.
**Where**: `internal/channel/tools_unix_test.go`
**Depends on**: T19
**Reuses**: newChanEnv
**Requirement**: TOOL-06 (S1 hardening)
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] 0750 and 0705 agent dirs → isError, nothing written
- [x] The test fails with the mask `0o077` narrowed to `0o007` or `0o070` (N3)
**Tests**: integration
**Gate**: full
**Commit**: `test(channel): refuse download dirs open to the group or to others`

#### T21: Download dir owned by another user (F10, N4)

**What**: `checkPrivate` is called with file info whose `*syscall.Stat_t` carries another UID and a 0700 mode.
**Where**: `internal/channel/private_unix_test.go`
**Depends on**: T20
**Reuses**: checkPrivate
**Requirement**: TOOL-06 (S1 hardening)
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] Another user's 0700 dir → error "belongs to another user"; the user's own 0700 dir → nil
- [x] The test fails with the owner check disabled (N4)
**Tests**: unit
**Gate**: full
**Commit**: `test(channel): refuse a download dir owned by another user`

#### T22: Dedupe window boundary (F11, N11)

**What**: The link's id ring remembers the last 1,000 delivered ids: after 1,000 ids the first is still known; the 1,001st evicts it.
**Where**: `internal/channel/link_test.go`
**Depends on**: T21
**Reuses**: newIDRing, dedupeSize
**Requirement**: AGT-10
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] With 1,000 ids remembered all are known; adding the 1,001st forgets only the oldest
- [x] The test fails with the window set to 1 or 999 (N11)
**Tests**: unit
**Gate**: full
**Commit**: `test(channel): assert the 1000 message dedupe window`

#### T23: Inactive principal at upgrade (F12, N9)

**What**: A gateway whose token checker reports the principal inactive answers the upgrade with 401 and registers no agent.
**Where**: `internal/agents/gateway_test.go`
**Depends on**: T22
**Reuses**: registry fixture
**Requirement**: AGT-02 (contract: inactive → 401)
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] Checker false → 401 `unauthorized`, no agent row for (owner, name)
- [x] The test fails with the pre-upgrade check disabled (N9)
**Tests**: integration
**Gate**: full
**Commit**: `test(agents): refuse the agent upgrade for an inactive token`

#### T24: At most 50 agents per owner (S2)

**What**: `agents.MaxAgentsPerOwner` (50). Registering a new agent name beyond it answers the WebSocket upgrade with 409 `conflict` "agent limit reached"; existing agents still connect.
**Where**: `internal/agents/registry.go`, `internal/store/queries/agents.sql`, `internal/api/agent_ws_test.go`
**Depends on**: T23
**Reuses**: Registry.Upsert
**Requirement**: S2 (security, no AC)
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [ ] With 50 agents the 51st name → HTTP 409 `conflict` "agent limit reached" and no 51st row
- [ ] One of the 50 existing agents still connects
**Tests**: e2e
**Gate**: full
**Commit**: `fix(agents): cap registered agents at 50 per owner`

#### T25: Link keeps reading before initialization (S5)

**What**: Pushed frames are queued for a delivery goroutine that waits for `notifications/initialized`; the WebSocket reader keeps reading, so pings are answered and closes detected before initialization. Acks still follow the notification write.
**Where**: `internal/channel/link.go`, `internal/channel/mcp/server.go`, `internal/channel/link_test.go`
**Depends on**: T24
**Reuses**: link.handle
**Requirement**: AGT-07, AGT-03 (S5)
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [ ] A server ping after pushed frames and before initialize gets its pong
- [ ] After initialize the buffered frames arrive in order, each acked after its notification
**Tests**: integration
**Gate**: full
**Commit**: `fix(channel): keep reading the agent socket before initialization`

#### T26: Skipped count and cursor jump from one read (S6)

**What**: The backlog count and list run in one read transaction (one SQLite snapshot), so a message posted between them cannot make the notice off by one.
**Where**: `internal/store/store.go`, `internal/agents/gateway.go`, `internal/agents/export_test.go`, `internal/agents/gateway_backlog_test.go`
**Depends on**: T25
**Reuses**: CountAgentBacklog, ListAgentBacklog
**Requirement**: AGT-09 (S6)
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [ ] A post between the count and the list (test hook) leaves the notice count equal to the messages before the first delivered one; the new post arrives after the backlog
**Tests**: e2e
**Gate**: full
**Commit**: `fix(agents): read the backlog count and messages from one snapshot`

#### T27: Contract notes and traceability for fix iteration 2

**What**: Record the agent cap, the link's delivery queue, the backlog snapshot and the new tests; update traceability.
**Where**: `contract-notes.md`, `spec.md`
**Depends on**: T26
**Reuses**: none
**Requirement**: all
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [ ] contract-notes.md describes the agent cap, the delivery queue and the backlog snapshot
**Tests**: none
**Gate**: build
**Commit**: `docs(specs): record agent channel contract notes for fix iteration 2`
