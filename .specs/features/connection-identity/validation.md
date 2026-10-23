# Connection Identity Validation

**Date**: 2026-10-23
**Spec**: `.specs/features/connection-identity/spec.md` (CID-01..CID-27)
**Diff range**: `9233ad6..0e0e959` (main), 245 files
**Verifier**: independent sub-agent (author ≠ verifier); final pass over the whole feature, fresh effort on T25-T39
**Verdict**: PASS

---

## Task Completion

| Task | Status | Notes |
| ---- | ------ | ----- |
| T1-T24 | ✅ Done | Mutation-tested by earlier per-phase reviews; re-covered here end to end |
| T25-T32 (web) | ✅ Done | 10 web mutants, all killed |
| T33-T35 (server retirement) | ✅ Done | 4 mutants, all killed |
| T36 (migration 0018) | ✅ Done | 5 migration mutants plus a 0017 default mutant, all killed; real v0.4.2 upgrade passes |
| T37 (upgrade e2e) | ✅ Done | Kills the migration and idle-operator mutants |
| T38 (docs) | ✅ Done | Retired features named only in the v0.5.0 "Retired" notes |
| T39 (dropped notice for polling) | ✅ Done | 5 mutants, all killed |

No task in `tasks.md` is left unchecked.

---

## Spec-Anchored Acceptance Criteria

| ID | Spec-defined outcome | `file:line` + assertion | Result |
| -- | -------------------- | ----------------------- | ------ |
| CID-01 | Operator must be an AI the owner owns | `internal/api/connector_operator_test.go:203` - `require.Equal(t, map[string]string{"operator_id": "must be an AI you own"}, e.Fields, c.name)` | ✅ PASS (⚠️ spec gives no refusal code; tests pin 422 `operator_id`) |
| CID-02 | No operator → nothing delivered, detail "choose the AI that operates this connection" | `internal/api/connector_operator_test.go:89` - `require.Equal(t, "choose the AI that operates this connection", f.conn.LastError)`; `internal/api/upgrade_test.go` (T37) - `[]any{dto.ConnectorStatusError, dto.ConnectorNoOperator}` and `require.Nil(t, fake.Lookup("k-wa"))` | ✅ PASS |
| CID-03 | Non-operator send → 403 `not_operator`, nothing sent | `internal/api/agent_connection_send_test.go:96` - `RequireError(http.StatusForbidden, dto.ErrorCodeNotOperator)` | ✅ PASS |
| CID-04 | 0 or several instances → detail "choose the instance (number) of this connection" | `internal/connectors/whatsapp/connector_test.go:342` - `require.Equal(t, "choose the instance (number) of this connection", rec.NextStatusDetail(t))` | ✅ PASS |
| CID-05 | Queue moves to new operator; one audit entry | `internal/api/connector_operator_queue_test.go:135` - `require.Len(t, entries, 2, "choosing Atlas, then the change to Notes")` + `:138` `e.Detail["moved"] == 3` | ✅ PASS |
| CID-06 | Event carries platform, connection, chat id/title/kind, sender name/id, trusted, from_self, text, attachments | `internal/api/agent_platform_ws_test.go:184` - exact key set; `:194` full `dto.PlatformEvent{...}` equality | ✅ PASS |
| CID-07 | Inbound never stored as a conversation message | `internal/connectors/inbound_test.go:246` (no message/person/room); `internal/api/upgrade_test.go` T37 - `require.Equal(t, before, messages(), "no message of any room")` | ✅ PASS |
| CID-08 | Offline → queued, oldest first on connect | `internal/api/agent_platform_ws_test.go:157` (TestPlatformBacklogDeliversQueuedMessagesOldestFirst) - first/second/third ids in arrival order (`:209`) | ✅ PASS |
| CID-09 | Ack deletes text at once; files 24 h later | `internal/platform/queue_test.go:358` - `require.Empty(t, got.Text, "the text is gone")`; `internal/platform/purge_test.go:127` - `require.Zero(t, res.Acked, "24 hours minus 1 ms after the ack")` then deleted at `AckedTTL` | ✅ PASS |
| CID-10 | 7 days unacked → deleted; next connection told the count | `internal/platform/purge_test.go:80,86,90` - not at 7 d − 1 ms, deleted at 7 d, `takeDropped == 1`; T39: `internal/api/agent_pending_platform_test.go:258` (poll gets count once), `:298` (poll without `platform=1` leaves it), `internal/cli/hook_platform_test.go:211`, `internal/channel/pending_platform_test.go:147` | ✅ PASS |
| CID-11 | Over 5,000 → oldest dropped and counted | `internal/platform/queue_test.go:422` - `require.Equal(t, 5000, platform.MaxQueued)`; `:433` `"old-1"` first; `:435` dropped counter reads 1 | ✅ PASS |
| CID-12 | groups_mentions_only → unmentioned group message not delivered | `internal/api/hooks_handlers_test.go:246` - `require.Empty(t, e.queued(t, e.operator), "and nothing is delivered")`; T37 group "lunch anyone?" not delivered | ✅ PASS |
| CID-13 | trusted only for sender linked to operator's owner | `internal/connectors/inbound_test.go:434` - `require.Equal(t, c.trusted, last.Trusted, c.name)` (table of linked/unlinked) | ✅ PASS |
| CID-14 | Own-account message: from_self=true, trusted=false; echo not delivered | `internal/connectors/selfmsg_test.go:50` - `[]any{"checking it now", true, false, ...}` | ✅ PASS |
| CID-15 | reply → that chat, unsigned, quoted | `internal/api/agent_reply_platform_test.go:77` - chat jid, text `"on its way"` (no signature), quote `"M1"` | ✅ PASS |
| CID-16 | send_message to connection chat, unsigned | `internal/api/agent_connection_send_test.go:42` - `"good morning, team"` sent verbatim | ✅ PASS |
| CID-17 | Chat not of the connection → 404 `unknown_chat` | `internal/api/agent_connection_send_test.go:105` - `RequireError(http.StatusNotFound, dto.ErrorCodeUnknownChat)` | ✅ PASS |
| CID-18 | Failure reason returned; outbox retries kept | `internal/api/agent_connection_send_test.go:60` - `PlatformSendResult{Status: pending, Error: "platform unavailable"}`; `internal/connectors/sends_test.go:232` backoff, `:261` failed after 5th | ✅ PASS |
| CID-19 | Chat as member → 422 `chats_not_members` | `internal/api/chats_not_members_test.go:75` - `RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeChatsNotMembers)` | ✅ PASS |
| CID-20 | Picker offers users and AIs only | `web/src/components/member-picker.test.tsx:55-63` - AI and user candidates present, `queryByTestId(/^candidate-chat-/)` null, no `/chats` call | ✅ PASS |
| CID-21 | Upgrade: chats leave rooms; history, intelligences, settings kept | `internal/store/connection_proxy_migration_test.go:111,113,123` - zero chat members, other members and every surviving table row identical; `internal/api/upgrade_test.go` T37 - members `[u-alice, a-atlas, a-pc2]`, history `{m-team1: Zé, m-team2: atlas}`; platform DM history banner `web/src/routes/_app/rooms/conversation.test.tsx:483-486`; post → 422 `history_only` `internal/api/history_only_test.go:110` | ✅ PASS |
| CID-22 | Retired features gone from API, tools, web | `internal/api/retired_routes_test.go:27-46` (each retired route 404 for every role); `internal/api/retired_fields_test.go:91` (no retired key in any response or live frame); `internal/api/dto/retired_test.go:20`; `internal/api/rooms_no_chat_delivery_test.go:121,127` (recipients ignored, agents still receive); `internal/channel/instructions_test.go:101`; web tests for composer, room settings, settings (`settings.test.tsx:136`) | ✅ PASS |
| CID-23 | Page shows operator, deliver, mentions-only, status, queued; no text | `web/src/routes/_app/connectors/connectors.test.tsx:401-418` - "Operated by Atlas", "4 queued", "Connected", Deliver value; `internal/api/connector_delivery_test.go:199` - no queued text in any view | ✅ PASS |
| CID-24 | Operator deleted → no operator, queue deleted | `internal/api/operator_removal_test.go:109` - `require.Nil(t, got.Operator)`; `:112` - `require.Zero(..., "every item of the connection is gone")` | ✅ PASS |
| CID-25 | Takeover → unacked items to the new session | `internal/api/agent_platform_ws_test.go:362` - `require.Equal(t, []string{pending, done}, platformIDs(t, holder, 2))` | ✅ PASS |
| CID-26 | Platform retry queued once | `internal/connectors/inbound_test.go:456` - `require.Len(t, got, 1)`; `internal/platform/queue_test.go:212` | ✅ PASS |
| CID-27 | New and upgraded connections: deliver=all, groups_mentions_only=on | `internal/api/connector_delivery_test.go:49-50` (new); `internal/store/connection_proxy_migration_test.go:135-136` (every old scope) | ✅ PASS |

**Status**: ✅ All 27 ACs covered with spec-matching assertions. One ⚠️ spec-precision note: CID-01 names no refusal code or status (tests pin 422 on `operator_id`).

---

## Discrimination Sensor

Run in a detached scratch worktree of `0e0e959`; each mutant applied, its tests run, the file restored. Real tree `git status --porcelain` was empty before and after.

| # | File | Mutation | Killed by |
| - | ---- | -------- | --------- |
| M1 | `internal/store/migrations/0018_connection_proxy.sql` | Chat members kept in rooms | ✅ `TestConnectionProxyUpgradeKeepsHistoryAndDropsTheRest` ("Should be zero, but was 4") |
| M2 | same | Chat-authored room history deleted | ✅ same test (messages rows differ) |
| M3 | same | `groups_mentions_only=0` for old `all` scope | ✅ same test (expected 1) |
| M4 | same | `deliver` copied from old scope | ✅ same test (expected "all") |
| M5 | same | Owner-operated connections given the owner as operator | ✅ same test (connectors rows differ) |
| M6 | `internal/connectors/manager.go:280` | Connection without operator started anyway | ✅ `TestUpgradedWhatsAppRoomLosesItsChatAndItsGroupReachesOnlyTheOperator` |
| M7 | `internal/platform/queue.go` TakeDropped | Count never reset (returned twice) | ✅ `TestTakeDroppedReadsAndResetsTheCounter`, `TestTheFiveThousandAndFirstItemDropsTheOldest` |
| M8 | `internal/api/agent_handlers.go` | Poll without `platform=1` consumes the count | ✅ `TestPendingWithoutThePlatformParameterLeavesTheDroppedCount` |
| M9 | same | Dropped count does not end a long-poll | ✅ `TestPendingDroppedCountComesWithTheMessagesAndEndsAWait` |
| M10 | `internal/channel/frame.go` FramePending | Notice dropped from wait/hook output | ✅ `TestFramePendingTellsAboutDroppedMessagesFirst`, `TestWaitForMessagesToolTellsAboutDroppedMessages` |
| M11 | `internal/cli/hook.go` | Hook frames without the notice | ✅ `TestHookTellsHowManyPlatformMessagesWereDropped` |
| M12 | `internal/api/chats_handlers.go` | `GET /me/notify-options` answers 200 | ✅ `TestRetiredChatRoutingAndNotifyRoutesAnswerNotFound` |
| M13 | same | `POST /chats/{id}/route` answers 200 | ✅ same |
| M14 | `internal/api/dto/rooms.go` | Room DTO exposes `bridge_chats` | ✅ `TestContractDeclaresNoRetiredChatField` |
| M15 | `internal/rooms/messages.go:296` | New posts store recipients | ✅ `TestPostsNamingChatsAreAcceptedAndReachNoChat` |
| M16 | `internal/store/migrations/0017_platform_queue.sql` | Default `deliver='dms'` | ✅ store upgrade test (expected "all") |
| W1 | `web/src/components/conversation-header.tsx` historyPlatform | Person DM shows composer | ✅ `conversation.test.tsx` "shows the history banner instead of the composer" |
| W2 | `web/src/routes/_app/rooms/$roomId.tsx` | `history_only` error not mapped to banner | ✅ "turns a post refused as history into the same banner" |
| W3 | `web/src/routes/_app/connectors/$connectorId.tsx` | Queued count hidden | ✅ connectors.test "…the queued count", "speaks Portuguese" |
| W4 | same | Operator select offers shared (not owned) AIs | ✅ options list mismatch |
| W5 | same | Operator pick not saved | ✅ "asks for an operator when there is none and saves the pick" |
| W6 | `web/src/components/member-picker.tsx` | Users not offered | ✅ member-picker "offers AIs and users only" |
| W7 | `web/src/components/member-list.tsx` | Leftover chat rows counted/rendered | ✅ "renders the empty state when the room only still holds a chat" |
| W8 | `web/src/components/composer.tsx` | Post carries `recipients` | ✅ "posts without recipients (CID-22)" |
| W9 | `web/src/components/room-settings-sheet.tsx` | Save sends `bridge_chats` | ✅ "offers no chat settings and saves without them" |
| W10 | `web/src/routes/_app/connectors/$connectorId.tsx` | Deliver change not saved | ✅ "saves deliver and groups_mentions_only" |

**Sensor depth**: P0 (data migration, delivery and authorization): 26 manual behavior mutants.
**Result**: 26/26 killed. ✅ PASS

---

## Upgrade Safety (real v0.4.2 database)

A database was built with the v0.4.2 binary (tag `v0.4.2`) through its HTTP API, agent WebSocket and CLI. It held 2 users, 3 AIs, 4 connectors (webhook ×2, telegram, whatsapp; scopes all/all/dms/off; two operated by AIs, two owner-operated, one catch-all room), 4 chats, 4 people, 5 rooms (Team with the owner, 2 AIs and a chat; a catch-all Inbox; a per-chat room; 2 DMs), 18 messages, 7 outbox rows (sent/pending/failed), 1 `auto_route_skips` row, and one `users.notify_chat_id`. Only `notify_chat_id` was set by hand; v0.4.2's API offers no DM chat for webhooks. The v0.5.0 binary (`v0.4.2-57-g0e0e959`) then started on it:

- Started without error; `schema_migrations` reads 0017, 0018; `integrity_check` ok; `foreign_key_check` empty.
- `messages`, `people`, `chats`, `intelligences`, `identities`, `attachments` are byte-identical before and after. `rooms` keeps every surviving column. `room_members` lost exactly the 4 chat rows.
- Through the API, Team lists Alice, Atlas and Nova and keeps 6 messages (Carol's included). Inbox and the per-chat room keep their history.
- The two owner-operated connectors read `error` / "choose the AI that operates this connection". The AI-operated ones keep their operator. All four read `deliver=all`, `groups_mentions_only=true`, `queued=0`, and no routing field.
- `outbox` and `auto_route_skips` are gone; `notify_chat_id` and `auto_route_room_id` are NULL.

---

## Code Quality

| Principle | Status |
| --------- | ------ |
| Minimum code / surgical changes | ✅ (retired code removed with its tests, as the Execution Protocol requires) |
| No scope creep | ✅ (T39 added by review, recorded in tasks.md) |
| Matches patterns | ✅ |
| Spec-anchored outcome check | ✅ (CID-01 note) |
| Per-layer coverage (domain 1:1; routes happy+edge+error, L-017/L-018 matrices) | ✅ |
| Every test maps to a spec requirement | ✅ (tests are labelled with CID ids) |
| Documented guidelines followed: `AGENTS.md`, `.specs/LESSONS.md`, web `conventions.md` | ✅ |
| Public repo hygiene (feature diff) | ✅ Synthetic names (Alice, Bob, Atlas, Pat, Rita, Zé, Maria Silva) and synthetic numbers (`55119999900xx`); André is the owner. No hostnames, group names or tokens. A nickname in older tests (outside this diff) was replaced by the neutral "Vega" after validation |

---

## Edge Cases

- [x] CID-24 operator deleted: no operator, queue deleted (`internal/api/operator_removal_test.go:109,112`)
- [x] CID-25 takeover: unacked items go to the new session (`internal/api/agent_platform_ws_test.go:362`)
- [x] CID-26 platform retry queued once (`internal/connectors/inbound_test.go:456`)
- [x] Upgrade runs once and rolls back atomically (`internal/store/connection_proxy_migration_test.go:162,184`)
- [x] Old CLI posting `recipients`/`chat_ids` is accepted and ignored (`internal/api/rooms_no_chat_delivery_test.go:121`, `internal/channel/connection_tools_test.go:199`)

---

## Gate Check

- **Gate command**: `make check SHARDS=6` (scratch worktree of `0e0e959`) → exit 0 in 2m10s: lint, vet and drift clean; Go tests all `ok` (38 packages); integrations 85 pass / 0 fail; web 51 files / 769 tests pass; web build and `go build` ok.
- **E2E**: `make e2e` with the cached headless Chromium → 20 passed (desktop-1280 and mobile-360). The known timing test did not fail, so no rerun was needed.
- **Test count**: Go top-level tests 1220 → 1347 (+127). Static web `it(` count 712 → 679 (−33). The web drop comes from retired features whose tests were deleted with them (route-chat-dialog, notify, composer recipients, catch-all, delivery badges), as the tasks' Execution Protocol requires. TS integrations 43 → 43.
- **Skipped tests**: none.
- **Failures**: none.

---

## Requirement Traceability Update

| Requirement | Previous Status | New Status |
| ----------- | --------------- | ---------- |
| CID-01..CID-27 | Implementing (CID-04 Implemented) | ✅ Verified |

`spec.md` still lists the requirements as "Implementing", and its coverage line says T1-T38 (T39 exists). The orchestrator should update both; this Verifier did not edit `spec.md`.

---

## Summary

**Overall**: ✅ Ready

**Spec-anchored check**: 27/27 ACs matched the spec outcome; 1 spec-precision note (CID-01 refusal code).
**Sensor**: 26/26 mutations killed.
**Gate**: `make check` exit 0; `make e2e` 20/20.

**What works**: one AI per connection; direct queue delivery with oldest-first order, ack, 24 h and 7 d expiry, the 5,000 bound and the dropped notice (now over the WebSocket and in polls); unsigned answers through the connection; chats never members; retired routes, fields and UI gone; an atomic upgrade that keeps history and idles owner-operated connections. The upgrade works on a real v0.4.2 database.

**Issues found** (none blocking):
1. Minor (docs): `spec.md` traceability statuses and coverage line are stale (see above).
2. Minor (spec precision): CID-01 has no refusal code or status in the spec. The tests pin 422 `operator_id: "must be an AI you own"`; add it to the spec.
3. Note (hygiene, pre-existing, outside the diff): a nickname in older tests; replaced by "Vega" after validation.

**Next steps**: Update the traceability in `spec.md`. Then release v0.5.0 with the CLI (a push or tag needs André's go-ahead).
