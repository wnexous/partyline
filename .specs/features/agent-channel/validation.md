# Agent Channel Validation

**Date**: 2026-10-20
**Spec**: `.specs/features/agent-channel/spec.md`
**Diff range**: `14ceffc~1..c497608` (feature commits 14ceffc, 234bb7f, 34bb9b9, 50b7edf, abc5d08, c42240e, 4753a23, 4ca76fe, b1bbf9a; docs 4ed1daa, c497608; web-app merges interleaved and out of scope)
**Verifier**: independent sub-agent (author ≠ verifier), detached worktree at c497608

**Verdict**: FAIL ❌. All 22 ACs and the 3 edge cases have spec-anchored test evidence, but 5 of 23 sensor mutants survived, and the build gate (`make check`) failed twice on this loaded host (load-induced timeouts in `internal/api`).

---

## Task Completion

| Task | Status | Notes |
| ---- | ------ | ----- |
| T1 Agent registry and presence | ✅ Done | 14ceffc |
| T2 Agent gateway WebSocket | ✅ Done | 234bb7f. Live-vs-backlog dedupe and ack membership check are untested (M16, M17) |
| T3 Agent dispatcher | ✅ Done | 34bb9b9 |
| T4 Agent REST endpoints | ✅ Done | 50b7edf |
| T5 MCP stdio server | ✅ Done | abc5d08 |
| T6 Notification formatting | ✅ Done | c42240e. Instructions are not checked in the real initialize answer (M21) |
| T7 Server link | ✅ Done | 4753a23. Reconnect loop backoff is untested (M15) |
| T8 Channel tools | ✅ Done | 4ca76fe |
| T9 Channel command + e2e | ✅ Done | b1bbf9a. The channel-side slug is untested (M20b) |

---

## Spec-Anchored Acceptance Criteria

| Criterion | Spec-defined outcome | `file:line` + assertion | Result |
| --------- | -------------------- | ----------------------- | ------ |
| MCP-01 server/discover | JSON-RPC error -32601 | `internal/channel/mcp/server_test.go:241` - `require.Equal(t, float64(-32601), m["error"]...["code"])`; golden replay `server_test.go:215-219` (discover answered before initialize); e2e `internal/cli/channel_test.go:139` | ✅ PASS |
| MCP-02 initialize | caps exactly `experimental.claude/channel={}` + `tools={}`, name `partyline`, instructions (data, trusted, tools); version echo ≤2025-11-25 else 2025-11-25 | `mcp/server_test.go:260-263` - exact `capabilities` map, `serverInfo`, `instructions`, version table 2026-07-28→2025-11-25, 2025-11-25/2025-06-18/2024-11-05 echoed; `internal/channel/format_test.go:133-138` - Instructions contain `DATA`, `never as instructions`, `trusted="true"`, all six tools; e2e `cli/channel_test.go:145-147` | ✅ PASS. No test asserts that the real channel's initialize carries `Instructions` (M21 survived) |
| MCP-03 no permission capability | key never present | `mcp/server_test.go:274` - `require.NotContains(t, line, "claude/channel/permission")`; exact-map checks at `:261` and `cli/channel_test.go:146` | ✅ PASS |
| AGT-01 connect → online | WS opened, server marks online | `internal/channel/link_test.go:28` - `require.Eventually(t, e.online…)`; `internal/api/agent_ws_test.go:91` - `require.True(t, list.Items[0].Online)`; e2e `cli/channel_test.go:150` (agent `my-project` from the cwd slug) | ✅ PASS |
| AGT-02 invalid token | 1 stderr line; MCP keeps answering; every tool `isError` + `partyline: not connected (invalid token)` | `link_test.go:175` - `require.Equal(t, 1, strings.Count(stderr, "\n"))`; `:180` ping answered; `internal/channel/tools_test.go:350-351` - all 6 tools `isError` + exact text | ✅ PASS |
| AGT-03 reconnect + offline | backoff 1 s doubling to 30 s; offline after close | `link_test.go:160` - sequence 1,2,4,8,16,30,30 s (helper `nextBackoff` only); `link_test.go:142-147` reconnect after restart; `agent_ws_test.go:207` - presence `Online:false` | ⚠️ PASS with gap: the `run` loop's use of the backoff is not asserted (M15 survived) |
| AGT-04 replace | first closed with reason `replaced` | `agent_ws_test.go:117-118` - code 4000, reason `"replaced"`; `internal/agents/registry_test.go:296-298` | ✅ PASS |
| AGT-05 notification shape | content = text + `[attachment id: name, mime]` lines; meta keys room, room_name, message_id, author, author_kind, sender_name, trusted, kind, reply_to when set | `format_test.go:82` - exact meta map for chat/agent/user/system; `:97,:103` exact content with attachments; `link_test.go:32-35` live path | ✅ PASS |
| AGT-06 identifier keys | `^[A-Za-z0-9_]+$` | `format_test.go:126` - `require.Regexp(t, key, k)` over message and notice meta | ✅ PASS |
| AGT-07 ack after write | ack only after the stdout write; cursor advances | `link_test.go:115-127` - no ack before handshake, ack after notification; `agent_ws_test.go:179,184` - cursor = acked seq, never backwards | ✅ PASS |
| AGT-08 backlog | oldest first, ≤50/room after cursor | `agent_ws_test.go:134-142` - IDs in seq order, own message excluded, nothing extra; `link_test.go:45-48` | ✅ PASS |
| AGT-09 >50 pending | one notice with skipped count first; cursor past skipped | `agent_ws_test.go:158` - `"10 older messages skipped; use the history tool"`; `:159-161` last 50; `:163` - cursor = `posted[9].Seq` | ✅ PASS. ⚠️ Spec-precision: the spec says "system message"; it ships as an unstored `notice` frame (`format.go:54-63`, meta `author_kind=system`). The contract notes accept this |
| AGT-10 dedupe | re-ack, no re-emit | `link_test.go:94-103` - one notification, two acks for `m1` | ✅ PASS. ⚠️ The 1,000-id ring eviction (`link.go:329-339`) has no test |
| TOOL-01 six tools | exact set | `tools_test.go:90-93` - `Len(list, 6)` + each name, required args | ✅ PASS |
| TOOL-02 reply | reply_to set; recipients = chat author, else none | `tools_test.go:113,116,122-123`; `internal/api/agent_handlers_test.go:191,194,199-200` | ✅ PASS |
| TOOL-03 send_message | posts as agent by id/slug; optional chat recipients | `tools_test.go:141,147`; `agent_handlers_test.go:110,115-118` (`Support`/`support`) | ✅ PASS |
| TOOL-04 not a member | `isError` + `not a member of room <room>` | `tools_test.go:164-166` (send_message by name, history by id); `agent_handlers_test.go:129,131` (id, name, unknown) | ✅ PASS. `reply`/`download_attachment` for a foreign room answer `message not found` / 404 (they target a message, not a room) |
| TOOL-05 history | ≤100, default 20, before id, push-meta fields | `agent_handlers_test.go:157,166,174` - 20 / clamp 101→100 / before; `tools_test.go:187,192-195` - 20 + exact meta map per entry | ✅ PASS |
| TOOL-06 download | under agent temp dir, 0600, absolute path | `tools_test.go:237-238,244` - `IsAbs`, dir = `<TempDir>/partyline/builder`, `FileMode(0o600)` | ✅ PASS (see security gap S1) |
| TOOL-07 send_file | upload with caption and reply_to | `tools_test.go:263-269` - text, reply_to, author, attachment bytes | ✅ PASS |
| TOOL-08 rate limit | 429 beyond 30/min; tool says `rate limited` | `agent_handlers_test.go:235,237` - 31st post and the reply → 429 `rate_limited`; `tools_test.go:285-286` | ✅ PASS |
| TOOL-09 list_rooms | rooms with members (kind, name, online for agents) | `tools_test.go:312-318,328`; `agent_handlers_test.go:99` - `{"builder@alice": true, "helper@bobby": false}` | ✅ PASS |

**Focus checks requested by the orchestrator**

- Golden handshake: `mcp/server_test.go:166-232` replays `testdata/claude-code-2.1.303.jsonl`. Its `matches` helper compares map length (`:120`), so the capabilities must match exactly. M1, M2 and M3 were killed.
- Server-derived agent identity: `agentOnly` resolves the agent from the token owner plus a slug (`internal/api/agent_handlers.go:58-62`), and the WS registers under `p.UserID` (`internal/agents/gateway.go:169`). The principal's `AgentID` is set only there (`internal/api/middleware.go:198` copies just the name). Evidence: `agent_ws_test.go:226-229` (bob's token with name `builder` gets a different agent and none of alice's messages). M10 (owner-agnostic lookup) was killed.
- Recipient-relative trust: `api/agent_dispatch_test.go:53,56,61,64` covers all four owner/author pairs. M9c was killed.

**Status**: ✅ All 22 ACs covered with spec-anchored assertions. ⚠️ 2 spec-precision notes (AGT-09 "system message" vs notice frame, AGT-10 bounded ring).

---

## Edge Cases

- [x] Stdin closes → WS closed, exit 0 within 2 s: `link_test.go:262-265` (`took < 2s`, agent offline); `cli/channel_test.go:184` (exit code 0)
- [x] Notification before `notifications/initialized` is held: `mcp/server_test.go:285-298`; `link_test.go:115-121`
- [x] Empty slug → `agent`: `internal/agents/registry_test.go:133-134` (`""`, `"!!!"` → `agent`), `registry_test.go:170`. The channel-side `agents.Slug` call (`run.go:393`) is not covered (M20b survived)

---

## Discrimination Sensor

Expanded tier. Scratch at `git worktree add --detach /tmp/claude-1001/pl-agent-mutants c497608`. Each mutant was applied, the covering packages were run with `-race`, and the file was restored with `git checkout`. The scratch was then removed. Real-tree porcelain matched the baseline (empty) before and after.

| # | File:line | Mutation | Killed? |
| - | --------- | -------- | ------- |
| M1 | `internal/channel/mcp/server.go:282` | Echo any client protocol version | ✅ Killed (TestInitializeNegotiatesVersion) |
| M2 | `mcp/server.go:289` | Add `claude/channel/permission` capability | ✅ Killed (golden, NoPermissionCapability, ChannelEndToEnd) |
| M3 | `mcp/server.go:108-112` | Notify does not wait for `initialized` | ✅ Killed (NotificationHeldUntilInitialized, golden, LinkAcksOnly…) |
| M4 | `internal/channel/link.go:281-293` | Ack before writing the notification | ✅ Killed (TestLinkAcksOnlyAfterNotificationWritten) |
| M5 | `link.go:285` | Drop `remember` (no dedupe) | ✅ Killed (TestLinkDedupesRepeatedMessage) |
| M6 | `internal/channel/format.go:38` | Meta key `room_name` → `room-name` | ✅ Killed (FormatMeta, KeysAreIdentifiers, ToolHistory) |
| M7 | `internal/agents/gateway.go:29` | BacklogLimit 50 → 51 | ✅ Killed (TestAgentWSBacklogCapSkipsOlderMessages) |
| M8 | `gateway.go:294-296` | No cursor jump past skipped messages | ✅ Killed (same) |
| M9c | `internal/delivery/dispatcher.go:133` | Trust any user-authored message (not recipient-relative) | ✅ Killed (DispatchPushesWithRecipientRelativeTrust, SkipsAuthorAgent) |
| M10 | `internal/store/db/agents.sql.go` GetAgentByName | Lookup ignores the token owner | ✅ Killed (UpsertIdempotentPerOwnerAndName, AgentWSIdentityScopedToTokenOwner) |
| M11 | `internal/agents/limit.go:406` | `>=` → `>` (31 posts allowed) | ✅ Killed (AgentPostRateLimit, ToolRateLimited) |
| M12 | `internal/channel/tools.go:245,258` | Download mode 0644, no chmod | ✅ Killed (TestToolDownloadAttachment) |
| M13 | `internal/api/agent_handlers.go:34` | Drop `<room>` from the not-a-member text | ✅ Killed (AgentRoomRefErrors, ToolNotAMember) |
| M14 | `agent_handlers.go:194-196` | Reply to a chat goes to every chat | ✅ Killed (AgentReply, ToolReply) |
| M18 | `link.go:217` | 401/403 treated as unreachable | ✅ Killed (LinkInvalidToken, LinkErrInvalidToken, ToolsWithInvalidToken) |
| M19b | `internal/agents/presence.go:67` | Do not close the replaced session | ✅ Killed (PresenceReplaceAndOffline, AgentWSSecondConnectionReplacesFirst) |
| M15 | `link.go:169` | Dial failures retry at 1 s forever (no doubling) | ❌ Survived |
| M16 | `gateway.go:344-346` | Ack advances the cursor in rooms the agent is not a member of | ❌ Survived |
| M17 | `gateway.go:253` | Live dedupe `<=` → `<` (backlog/live overlap re-sent) | ❌ Survived |
| M20b | `internal/channel/run.go:393` | Channel stops slugifying the agent name | ❌ Survived |
| M21 | `run.go:395` | Real channel sends no `instructions` | ❌ Survived |

Invalid mutants that did not compile and were replaced: M9 → M9c, M19 → M19b, M20 → M20b.

**Sensor depth**: expanded (P0: identity, trust, delivery)
**Result**: 16/21 valid mutants killed (5 survived). FAIL ❌

---

## Code Quality

| Principle | Status |
| --------- | ------ |
| Minimum code | ✅ Hand-written MCP is small (`mcp/server.go` 306 lines), with no SDK (AD-004) |
| Surgical changes | ✅ Shared touches are small helpers (rooms `Details`/`MessageRef`, testkit, dto) |
| No scope creep | ✅ The replaced/deleted/unreachable states come from the contract notes |
| Matches patterns | ✅ httpx errors, sqlc queries, testkit |
| Spec-anchored outcome check | ✅ Exact texts and values asserted (see table) |
| Per-layer coverage | ⚠️ Gateway error paths (non-member ack, live/backlog overlap) and the link reconnect loop lack assertions |
| Every test maps to a requirement | ✅ Every test names an AC, contract item or design row in its comment |
| Documented guidelines | `AGENTS.md` and the tasks.md coverage matrix were followed |

---

## Gate Check

- **Gate command**: `make check` (check-generate, golangci-lint, go vet, web-check, `go test -race ./...`, web-test, web-build, go build)
- **Run 1**: EXIT 2. Generate, lint (0 issues), vet and web-check passed. `internal/api` failed `TestDispatchPushesWithRecipientRelativeTrust` (the WS dial hit the testkit's fixed 5 s deadline at `internal/testkit/live.go:55`), then the package hit Go's 10 m test timeout. Load average about 16 on 12 cores
- **Run 2 (re-run)**: EXIT 2. Only `internal/api` failed, this time `TestAgentRoomsListsMembersWithPresence`: "no agent frame within 5s" in the `newTwoAgents` fixture (`internal/api/agent_dispatch_test.go:36`, `internal/testkit/agent.go:61`). All other packages passed
- **Follow-up runs**:
  - `go test -race ./internal/api/` alone: ok (125 s)
  - `go test -race -json ./...`: 408 passed, 0 failed, 0 skipped
  - The suspect tests at `-count=5`: ok
  - `make web-test`: 27 files, 216 tests passed
  - `make web-build` and `go build ./...`: ok
- **Flaky (load-induced, listed separately)**: `TestDispatchPushesWithRecipientRelativeTrust`, `TestAgentRoomsListsMembersWithPresence`, `TestDispatchSkipsPausedRoom` (one failure in a `-count=3` run, then 5/5 and 6/6 clean). All three use the `newTwoAgents` or agent-dial fixtures with fixed 5 s budgets. No assertion failed on a value
- **Test count**: Go `func Test` 172 → 241 (+69 in this range, none removed)
- **Skipped**: none

---

## Ranked Gaps

1. **Gate not green under load**: `make check` failed twice on this host. The testkit agent and live waits are fixed at 5 s (`internal/testkit/agent.go:61`, `internal/testkit/live.go:55`), and under load `internal/api` approaches Go's 10 m package timeout. Fix: load-tolerant budgets in the testkit agent and live helpers, or an explicit `-timeout` in `make test`.
2. **M16 survived (contract: "acks for rooms without the agent are ignored")**: no test sends an ack for a message in a non-member room (`internal/agents/gateway.go:343-347`). Fix: a test acks a foreign-room message and asserts no cursor row appears.
3. **M21 survived (MCP-02)**: no test asserts that `partyline channel`'s initialize answer carries the real `Instructions` text (`internal/channel/run.go:395`). Fix: assert `res["instructions"] == channel.Instructions` in `cli/channel_test.go` or a `Run`-level test.
4. **M15 survived (AGT-03)**: only the `nextBackoff` helper is tested. The reconnect loop (`internal/channel/link.go:166-178`) could stop doubling unnoticed. Fix: an injectable sleeper or clock that records the waits across repeated dial failures (1→2→4… capped).
5. **M17 survived (AGT-08, at-least-once)**: the live/backlog overlap filter (`gateway.go:253`) is untested; the channel LRU hides duplicates. Fix: a gateway test that pushes the same message during the backlog and asserts a single frame.
6. **M20b survived (AGT-01 edge)**: the channel's own `agents.Slug` (`run.go:393`) is untested. Without it, the download dir would use the raw name, e.g. `../x` in `filepath.Join` at `run.go:407`. Fix: a test where `--agent "../Night Shift"` asserts the dir `<TempDir>/partyline/night-shift`.

### Security weaknesses (not fixed; noted for the orchestrator)

- **S1 (medium, multi-user hosts)**: downloads go to `$TMPDIR/partyline/<agent>/`, which defaults to shared `/tmp`.
  - `os.MkdirAll(dir, 0o700)` (`internal/channel/tools.go:241`) neither checks ownership or mode of an existing `partyline` or `<agent>` dir nor rejects symlinks.
  - `os.OpenFile(..., O_CREATE|O_TRUNC)` (`tools.go:245`) follows symlinks.
  - Another local user could pre-create the directory, then swap or read files there, or redirect writes.
  - Fix: a per-user dir (`os.UserCacheDir` or `$XDG_RUNTIME_DIR`), an `Lstat` ownership and mode check, and `O_EXCL` or `O_NOFOLLOW`.
- **S2 (low)**: any valid agent token registers an unlimited number of agents just by varying `X-Partyline-Agent` (`gateway.go:169`, `Registry.Upsert`). There is no cap per owner or per token.
- **S3 (low, by design per AD-006)**: messages from the owner's own agents are `trusted=true` even when they quote untrusted chat content. Trust can be laundered through a relaying agent. Instructions and docs tell the session to confirm destructive actions.
- **S4 (low)**: `send_file` reads any local path the session names (`tools.go:293`). An injected room message could ask for `~/.ssh/...`. This is mitigated only by Claude Code's tool-permission prompts. The docs warn against `--dangerously-skip-permissions` (`docs/claude-code.md:126`).
- **S5 (low, robustness)**: before `notifications/initialized`, `Notify` blocks the link's only WS reader (`link.go:282`, `mcp/server.go:108`). Server pings (every 25 s) then go unanswered, and the gateway may drop the connection if Claude Code delays initialization.
- **S6 (low)**: the backlog count and list are two separate reads (`gateway.go:278,285`). A message arriving between them makes the "N older messages skipped" count low by one. The cursor jump itself stays correct.

---

## Fix Plans

| Fix | Task | Priority |
| --- | ---- | -------- |
| F1 | Load-tolerant testkit budgets for agent and live waits and dials (env-scalable), plus `-timeout 20m` in `make test` | Major |
| F2 | Gateway test: ack for a non-member room's message leaves no cursor (kills M16) | Major |
| F3 | Assert real instructions in the channel initialize answer (kills M21) | Major |
| F4 | Injectable wait in `link.run`; assert the 1→2→4…30 s sequence on dial failures (kills M15) | Minor |
| F5 | Gateway test for a live push overlapping the backlog → one frame (kills M17) | Minor |
| F6 | Channel test for the slugified agent dir with a hostile `--agent` (kills M20b) | Minor |
| F7 | S1 hardening: per-user download dir with ownership check and `O_NOFOLLOW`/`O_EXCL` | Major (security) |

---

## Requirement Traceability Update

| Requirement | Previous | New |
| ----------- | -------- | --- |
| MCP-01, MCP-03, AGT-01, AGT-02, AGT-04..AGT-07, AGT-09, AGT-10, TOOL-01..TOOL-09 | Implementing | ✅ Verified (evidence above) |
| MCP-02 | Implementing | ⚠️ Needs test fix (M21) |
| AGT-03 | Implementing | ⚠️ Needs test fix (M15) |
| AGT-08 | Implementing | ⚠️ Needs test fix (M17) |

spec.md was not edited by the Verifier.

---

## Summary

**Overall**: ❌ Not Ready

**Spec-anchored check**: 22/22 ACs and 3/3 edge cases have spec-anchored evidence; 2 spec-precision notes
**Sensor**: 16/21 killed (M15, M16, M17, M20b, M21 survived)
**Gate**: `make check` failed twice under load (timeouts only). Isolated and repeat runs are green: Go 408/0, web 216/0, lint 0 issues, build ok

**What works**:
- Golden handshake and version negotiation
- No permission capability
- Identifier-only meta
- Ack after write
- Backlog cap, notice and cursor jump
- Replace (4000)
- Server-derived identity
- Recipient-relative trust
- 30/min limit
- 0600 downloads
- Exact not-a-member text
- Real Claude Code 2.1.303 smoke test (uat.md)

**Next steps**: run F1–F6 (test strength and gate stability) and F7 (security hardening), then re-verify (iteration 1 of 3).
