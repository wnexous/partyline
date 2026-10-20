# Routing and Context Validation

**Date**: 2026-10-20
**Spec**: `.specs/features/routing-context/spec.md` (+ `design.md`, `tasks.md`, `contract-notes.md`)
**Diff range**: iteration 2 re-verify of `8094902..6947d1d` (T18–T34: b798941 … 6947d1d, migration 0008); feature base `bc17899`
**Verifier**: independent sub-agent (author ≠ verifier), detached worktree `partyline-verify-rc` at 6947d1d
**Iteration**: 2 of 3 (fix → re-verify loop)

**Verdict: FAIL ❌.** The three iteration-1 survivors (G5, G23, G26) are now killed, all 39 ACs have spec-anchored evidence, and every gate passes (`make check`, e2e 8/8). The expanded sensor still left **8 surviving mutants** in the fix code, so the feature is not done under validate.md. Two of them touch security decisions: instructions with an unknown author (saved before migration 0008) could become trusted without a failing test (T1, T2), and the catch-all caps have no concurrency test (C4). No wrong behaviour was found in the shipped code; every gap below is a missing or weak test.

---

## Iteration history

- **Iteration 1** (`bc17899..2f0931d`, verifier at 2f0931d): FAIL. 25/25 ACs evidenced; sensor 41/44 killed, with G5 (reply-to-me identity), G23 (notice wording) and G26 (paused-room notice) surviving. Eight security weaknesses were reported for triage. Fix tasks T18–T34 added ROUTE-06..09, REACT-03..05, INS-04/05, MEM-06 and A11Y-01..04, and amended INS-03, ROUTE-04 and CTX-06.
- **Iteration 2** (this report): G5, G23 and G26 are killed. The spec now has 39 ACs, all evidenced. The sensor killed 56 of 64 valid mutants and 8 survived. Gates are green.

---

## Task Completion

| Task | Status | Notes |
| --- | --- | --- |
| T1–T17 | ✅ Done | iteration 1 (unchanged) |
| T18 reply-to-me two agents | ✅ Done | b798941, kills G5 |
| T19 notice wording + paused room | ✅ Done | 64fa6c4, kills G23 and G26 |
| T20 react listen-only | ✅ Done | 5fe4fa6 |
| T21 unsigned webhook reaction | ✅ Done | 33a8744 |
| T22 migration 0008 + DTO | ✅ Done | 7e69fba |
| T23 no broadcast in catch-all rooms | ✅ Done | 98a37a4 |
| T24 tool warning | ✅ Done | 0e6aa34 |
| T25 caps | ✅ Done | b9d5995 (it also carries unrelated `harness-onboarding` spec files, which were ignored) |
| T26 clear sticky status | ✅ Done | 7aceba1 |
| T27 no room-existence oracle | ✅ Done | 5b7ea78 |
| T28 one emoji grapheme | ✅ Done | 75f73a2 |
| T29 instructions trust | ✅ Done | 76c745e |
| T30 mask DM numbers | ✅ Done | 1c187b2 |
| T31 web broadcast-off | ✅ Done | b9118fb |
| T32 mentions not a security filter | ✅ Done | 23795a2 |
| T33 form a11y | ✅ Done | 4944e4a |
| T34 notes + traceability | ✅ Done | 6947d1d |

---

## Spec-Anchored Acceptance Criteria (39)

### P1: Agents know where messages come from

| AC | Spec-defined outcome | `file:line` + assertion | Result |
| --- | --- | --- | --- |
| CTX-01 | meta `chat_id`, `chat_title`, `chat_kind`, `platform`, `sender_id` | `internal/channel/chat_context_test.go:60`: exact meta map `{"chat_id": group, "chat_title": "Lollerie", "chat_kind": "group", "platform": "whatsapp", …}` (TestPushCarriesChatContext :45); `internal/channel/format_test.go:27` | ✅ |
| CTX-02 | no chat keys for agent or user authors | `chat_context_test.go:45` (TestPushCarriesChatContext): exact maps for user and agent without chat keys | ✅ |
| CTX-03 | history items carry the same fields | `chat_context_test.go:106`: exact history item incl. `"chat_kind": "channel", "platform": "discord"` (TestHistoryCarriesChatContext :89) | ✅ |
| CTX-04 | instructions explain chat fields and `chat_ids` | `internal/channel/format_test.go:145`; `internal/channel/room_info_test.go:117-119` Contains `send_message`, `chat_ids`, `room_info`; behaviour `room_info_test.go:94` | ✅ |

### P1: Chat titles and room names

| AC | Spec-defined outcome | `file:line` + assertion | Result |
| --- | --- | --- | --- |
| CTX-05 | group subject stored as title | `internal/connectors/whatsapp/connector_test.go:624` `in.ChatTitle == "Lojinha Sintética"` | ✅ |
| CTX-06 (amended) | push name, else `+•••• 0000`; chat id and sender id keep the number | `internal/connectors/whatsapp/parse_test.go:243` (TestParseDirectMessageTitleFallsBackToNumber): `ChatTitle == "+•••• 0000"`, `ChatID == "acme\|5511999990000@s.whatsapp.net"`, `SenderID == "5511999990000@s.whatsapp.net"`, LID case too; push name `connector_test.go:642` `"Test Person"`; P1/P2 killed | ✅ |
| CTX-07 | room named after the title, else `<platform> chat` | `internal/api/room_names_test.go:38-39` `"Lojinha Sintética"`, `"WhatsApp chat"` | ✅ |
| CTX-08 | next refresh or inbound updates the title | `internal/connectors/inbound_test.go:433` (title becomes `"Support group (renamed)"` :439) | ✅ |

### P1: Catch-all routing

| AC | Spec-defined outcome | `file:line` + assertion | Result |
| --- | --- | --- | --- |
| ROUTE-01 | unrouted matching chat joins with outbound on and is delivered | `internal/connectors/catch_all_test.go:88` (TestCatchAllRoutesNewDMs), `:127` scope matrix | ✅ |
| ROUTE-02 | chat already in a room is not added | `catch_all_test.go:112-113` `NotContains(members, …)` | ✅ |
| ROUTE-03 | detail `auto-route room unavailable` | `catch_all_test.go:214` (deleted room), `:221` (lost manage) | ✅ |
| ROUTE-04 (amended) | manage required, else 403; unknown room gives the same status, code and message | `internal/api/catch_all_settings_test.go:84-86` `RequireError(403, forbidden)` for unknown and hidden rooms, `require.Equal(t, hiddenErr, unknownErr)`; `:77` post-only 403; O1 killed | ✅ |
| ROUTE-05 | `trusted=false` unless the owner's verified identity | `catch_all_test.go:100` `False(got.Trusted)`, `:122` `True(got.Trusted)` | ✅ |
| ROUTE-06 | broadcast off: stored, delivered to agents, no chat, exact warning; a reply to a chat with outbound on goes only to that chat | `internal/api/no_broadcast_test.go:71-73` (no outbox, `Warning == noBroadcastWarning`, pushed to agent), `:82,:85` (reply goes to `[family]`), `:91-95` (history has no warning); `internal/rooms/messages_test.go:477` (user and agent, exact text, reply to own message → warning); tool `internal/channel/room_info_test.go:127` exact `"sent (message_id …); not sent to any chat: …"`; Plan `internal/delivery/rules_test.go:233`; web `web/src/components/composer.test.tsx:246-269` and `:272` | ✅ (reply to a **listen-only** chat untested, B2) |
| ROUTE-07 | catch-all turns broadcast and bridge off; PATCH turning either on gets 422 with exact text and applies nothing | `no_broadcast_test.go:35-48` (after.Broadcast/BridgeChats false, exact fields map for alice and bob); `messages_test.go:530` (combined name+broadcast refused, `"inbox"` unchanged); migration `internal/store/store_test.go:324`; web `web/src/routes/_app/rooms/room.test.tsx:541,558` | ✅ |
| ROUTE-08 | 60/h per connector, 1,000 per room; nothing added or stored; detail `auto-route limit reached`; next route clears it | `catch_all_test.go:246` (60 accepted, 61st refused, `messageCount` unchanged, detail; after 1h+1s accepted and cleared), `:272` (999→1000 accepted, then refused) | ✅ (no concurrent-burst test, C4) |
| ROUTE-09 | route or settings save clears only auto-route details | `catch_all_test.go:304` (route clears, PATCH clears, `"connection lost"` kept) | ✅ (status event not asserted, S4/S5) |

### P1: Agent membership settings

| AC | Spec-defined outcome | `file:line` + assertion | Result |
| --- | --- | --- | --- |
| MEM-01 | no author header | `internal/connectors/sender_test.go:196` (TestOutboundAuthorFollowsMemberSettings) | ✅ |
| MEM-02 | display name outbound and in timeline | `sender_test.go:206` `"Lolo"` | ✅ |
| MEM-03 (fixed) | only mentions delivered, all stored; reply-to-**me** | `internal/delivery/wants_test.go:15` table; `internal/api/delivery_filter_test.go:148` two agents: reply reaches builder only, live and backlog; G5 killed | ✅ |
| MEM-04 | only `trusted=true` | `delivery_filter_test.go:100` | ✅ |
| MEM-05 | 422 limits | `internal/api/member_settings_test.go:99` | ✅ |
| MEM-06 | exact help text (en + pt-BR); channel says not a security filter | `web/src/components/member-settings.test.tsx:256-261` radiogroup description equals the en text; `:251` pt-BR text; `internal/channel/room_info_test.go:179` Contains the sentence; W15 killed | ✅ |

### P2: Room instructions

| AC | Spec-defined outcome | `file:line` + assertion | Result |
| --- | --- | --- | --- |
| INS-01 | ≤4,000 stored, longer 422 | `internal/api/room_instructions_test.go:17` | ✅ |
| INS-02 | room_info fields | `internal/api/room_info_test.go:24`; tool `internal/channel/room_info_test.go:33` | ✅ |
| INS-03 (amended) | one notice per room, also paused, literal trusted or untrusted text | `internal/api/room_info_test.go:82` `Equal(insNotice, n.Text)` (spec literal `:92`); paused `:96-110` (`:108` notice + `ExpectNone`); untrusted literal `:114`, asserted `:149`, owner's plain notice `:153`; channel `internal/channel/room_info_test.go:159`; G23, G26, T3 killed | ✅ (unknown-author case untested, T2) |
| INS-04 | author recorded (none when empty or legacy); `instructions_by`, `instructions_trusted` only for the owner | `internal/api/room_info_test.go:120` (alice trusted for her agent, not bob's; bob's edit flips it; cleared → empty); migration default `''` `store_test.go:324` | ⚠️ partial: legacy `''` author and create-time author are not asserted (T1, T8 survive) |
| INS-05 | `instructions_note` exact; instructions say only owner's are trusted | `internal/channel/room_info_test.go:152-169` exact note (`:168`); `:173-174` | ✅ |

### P2: Reactions

| AC | Spec-defined outcome | `file:line` + assertion | Result |
| --- | --- | --- | --- |
| REACT-01 | reaction reaches the origin chat | `internal/connectors/reaction_test.go:21`; per-platform tests (iteration 1) | ✅ |
| REACT-02 | `isError` + exact text | `internal/channel/react_test.go:31` | ✅ |
| REACT-03 | 422 `recipients: listen-only`, nothing stored | `internal/api/react_test.go:99-114` exact fields map, one stored message; R1 killed | ✅ |
| REACT-04 | webhook payload has no `author_name` when unsigned | `internal/api/hook_reaction_test.go:50-71` exact payload map without the key; R2 killed | ✅ |
| REACT-05 | one emoji grapheme, ≤16 code points, else 422 `emoji: must be one emoji` | `internal/validation/emoji_test.go:14` accept and reject table (letters, digits, multi-grapheme, ZWJ, flags, keycaps, skin tones); HTTP `react_test.go:56` | ⚠️ the 16-code-point bound is not tested at its edge (E5 survives) |

### P1: Delete a room

| AC | Spec-defined outcome | `file:line` + assertion | Result |
| --- | --- | --- | --- |
| ROOMDEL-01 | managers only; alert dialog text | `web/src/routes/_app/rooms/room.test.tsx:447` (dialog text, confirm), `:491` not offered without manage | ✅ |
| ROOMDEL-02 | navigate, toast, list updated | `room.test.tsx:447` pathname `/rooms`, toast, list cache (iteration 1, unchanged) | ✅ |

### P2: Accessible forms

| AC | Spec-defined outcome | `file:line` + assertion | Result |
| --- | --- | --- | --- |
| A11Y-01 | errors announced and tied with `aria-describedby` | keyword `member-settings.test.tsx:229-238` (`aria-live="polite"`, accessible description); room `connectors.test.tsx:523-527` (`role=alert`, description); scope `:530-547`; W7, W9–W12 killed | ✅ |
| A11Y-02 | Save commits a typed keyword or shows its error and sends nothing | `member-settings.test.tsx:209-215` body `{keywords: ["deploy","pedido"]}`; `:218-226` error and 0 patches; W8 killed | ✅ |
| A11Y-03 | counter describes the textarea | `room.test.tsx:380` `toHaveAccessibleDescription(/21 \/ 4000/)`; W14 killed | ✅ |
| A11Y-04 | Save disabled and described by `No changes to save.` | `connectors.test.tsx:550-560`; W13 killed | ✅ |

**Status**: 39/39 ACs evidenced. INS-04 and REACT-05 have gaps at the edges (see sensor). Success criterion "DM reaches the agent within 2 s" still has no AC (iteration-1 note, manual UAT).

---

## Edge Cases

- [x] Concurrent first messages add the chat once: `catch_all_test.go:156`.
- [x] Removed catch-all chat waits 24 h: `catch_all_test.go:175`.
- [x] Bridge on → off when chosen as catch-all: `no_broadcast_test.go:26,37`.
- [x] Composer hint and disabled Send until a chat is picked; posts to agents only with no chat to pick: `composer.test.tsx:246,272`; switches disabled with an explanation: `room.test.tsx:541`.
- [ ] **No connector targets the room any more → broadcast stays off until a manager turns it on**: no assertion. `no_broadcast_test.go:50-53` clears the connector and turns broadcast on at once, without first checking that it stayed off.
- [x] Keyword inside a longer word: `wants_test.go:51-52`.

---

## Discrimination Sensor

Scratch: `git worktree add --detach /tmp/claude-1001/pl-rc-mutants2 6947d1d` (web `node_modules` symlinked). One exact-string mutation at a time, `git checkout` of the file after each run, then targeted `go test -count=1` (C4 with `-race`) or `vitest run`. Every survivor was re-run against its full packages with no `-run` filter and still survived. Real-tree `git status --porcelain` was empty before and after the sensor.

| # | File | Mutation | Result |
| --- | --- | --- | --- |
| G5 | `internal/delivery/wants.go:129` | reply-to-me ignores the agent id | ✅ killed (was survivor) |
| G23 | `internal/agents/gateway.go:49` | notice text → "hello" | ✅ killed (was survivor) |
| G26 | `gateway.go:362` | no notice in paused rooms | ✅ killed (was survivor) |
| B1 | `internal/delivery/rules.go:86` | broadcast off ignored by Plan | ✅ |
| B2 | `internal/rooms/messages.go:244` | reply to a **listen-only** chat counts as an explicit chat (no warning, recipients stored) | ❌ survived |
| B3 | `messages.go:218` | warning not set | ✅ |
| B4 | `internal/channel/tools.go:128` | tool result drops the warning | ✅ |
| B5 | `messages.go:238` | replies never routed to the chat | ✅ |
| B6 | `messages.go:227` | agent posts skip the broadcast check | ✅ |
| F1 | `internal/connectors/service.go:345` | choosing a catch-all does not force broadcast/bridge off | ✅ |
| F3 | `internal/store/db/rooms.sql.go:382` | bridge not turned off | ✅ |
| F4 | `internal/rooms/rooms.go:290` | 422 only with 2+ connectors | ✅ |
| F5 | `rooms.go:285` | bridge_chats=true not checked | ✅ |
| F6 | `internal/store/migrations/0008_room_safety.sql:6` | migration keeps bridging | ✅ |
| F7 | `rooms.go:282` | PATCH broadcast ignored | ✅ |
| C1 | `internal/connectors/inbound.go:203` | hourly cap `>=` → `>` | ✅ |
| C2 | `inbound.go:203` | room cap `>=` → `>` | ✅ |
| C3 | `inbound.go:227` | window 2 h | ✅ |
| C4 | `inbound.go:191-192` | **mutex removed** (caps unserialized; run with `-race`) | ❌ survived |
| C5 | `inbound.go:211` | successful routes not counted | ✅ |
| C6 | `inbound.go:204` | limit sets no detail | ✅ |
| S1 | `inbound.go:212` | success does not clear the detail | ✅ |
| S2 | `service.go:328` | PATCH does not clear | ✅ |
| S3 | `service.go:328` | PATCH clears unrelated details | ✅ |
| S4 | `inbound.go:277` | clearing publishes no `connector.status` event | ❌ survived |
| S5 | `service.go:391` | PATCH clearing publishes no `connector.status` event | ❌ survived |
| S6 | `inbound.go:283` | limit detail not treated as auto-route detail | ✅ |
| O1 | `service.go:423` | unknown room → 404 (oracle back) | ✅ |
| E1 | `internal/validation/emoji.go:78` | skin tones refused | ✅ |
| E2 | `emoji.go:84` | non-ZWJ joiner accepted (multi-grapheme) | ✅ |
| E3 | `emoji.go:71` | 3+ regional indicators accepted | ✅ |
| E4 | `emoji.go:69` | keycap mark not required | ✅ |
| E5 | `emoji.go:10` | `MaxEmojiLength` 16 → 10 | ❌ survived |
| E6 | `emoji.go:49` | every code point above © counts as pictographic | ✅ |
| E7 | `emoji.go:70-71` | flags refused | ✅ |
| T1 | `internal/api/agent_handlers.go:116` | instructions with an unknown (legacy `''`) author become trusted | ❌ survived |
| T2 | `internal/agents/gateway.go:364` | unknown author gets the trusted notice | ❌ survived |
| T3 | `gateway.go:364` | always the trusted notice | ✅ |
| T4 | `rooms.go:277` | PATCH does not record the author | ✅ |
| T5 | `rooms.go:139` | cleared instructions keep an author | ✅ |
| T6 | `tools.go:286` | note added to empty instructions | ✅ |
| T7 | `tools.go:286` | note never added | ✅ |
| T8 | `rooms.go:163` | **room create** does not record the author | ❌ survived |
| T9 | `agent_handlers.go:114` | user id instead of username | ✅ |
| P1 | `internal/connectors/whatsapp/parse.go:340` | 6 digits shown | ✅ |
| P2 | `parse.go:132` | number not masked | ✅ |
| R1 | `messages.go:268` | reactions skip the listen-only check | ✅ |
| R2 | `internal/connectors/webhook/connector.go:361` | `author_name` always present | ✅ |
| W1 | `web/src/components/composer.tsx:105` | hint/required chat even with no pickable chat | ✅ |
| W2 | `composer.tsx:112` | Send enabled without a chat | ✅ |
| W3 | `composer.tsx:208` | hint not in the textarea's description | ✅ |
| W4 | `web/src/routes/_app/rooms/$roomId.tsx:122` | page does not pass `broadcast` | ✅ |
| W5 | `web/src/components/room-settings-sheet.tsx:93` | broadcast always sent | ✅ |
| W6 | `room-settings-sheet.tsx:164` | bridge switch not locked | ✅ |
| W7 | `web/src/components/member-settings.tsx:458` | keyword message not live | ✅ |
| W8 | `member-settings.tsx:199` | Save drops the typed keyword | ✅ |
| W9 | `member-settings.tsx:403` | message not in the input's description | ✅ |
| W10 | `web/src/components/connector-catch-all.tsx:129` | scope error not `role=alert` | ✅ |
| W11 | `connector-catch-all.tsx:110` | scope error not described | ✅ |
| W12 | `connector-catch-all.tsx:178` | room error not `role=alert` | ✅ |
| W13 | `connector-catch-all.tsx:217` | Save not described | ✅ |
| W14 | `room-settings-sheet.tsx:309` | counter not in the description | ✅ |
| W15 | `member-settings.tsx:290` | MEM-06 hint removed | ✅ |
| W16 | `room-settings-sheet.tsx:180` | no catch-all explanation | ✅ |

Excluded as equivalent: E8 (`pictoLowCut` '©' → '!'). It is only a fast-path cut before the table lookup and changes no result.

**Sensor depth**: expanded (P0: catch-all exposure, trust, broadcast)
**Result**: 56/64 killed. 8 survived (B2, C4, S4, S5, E5, T1, T2, T8). **FAIL ❌**

---

## Gate Check

- **Build gate** `make check`: EXIT 0 (14 min wall time on a loaded host). It covers generate drift, golangci-lint, vet, biome, typecheck, `go test -race ./...` (28 packages ok), **Vitest 288 passed / 29 files**, web build and `go build`. The first attempt was terminated externally (SIGTERM, no test failure). The re-run passed.
- **E2E** `make e2e` with `PLAYWRIGHT_CHROMIUM_EXECUTABLE=…/chromium_headless_shell-1243/…`: **8 passed**, EXIT 0, first run.
- **Counts**: 501 Go test funcs and 258 web cases at 2f0931d, versus 521 Go and 270 web at 6947d1d (+20 Go, +12 web). Skipped: none. Failures: none.
- **Integrity**: the removed assertions were replaced by stronger ones: the constant became the spec literal, 422 "unknown room" became an identical 403, and the raw number became the masked title. No assertion was weakened.

---

## Code Quality

| Principle | Status |
| --- | --- |
| Minimum code / surgical / no scope creep | ✅ (`validation.OneEmoji` moved out of api; `instructionsBy` helper) |
| Matches patterns | ✅ (validation.Check 422s, sentinel errors, sqlc queries, tygo DTOs) |
| Spec-anchored outcome check | ✅ literal texts asserted (warning, notices, note, MEM-06, 422 reasons) |
| Per-layer coverage | ⚠️ concurrency of caps, legacy trust, create-time author, 16-cp edge and status events untested |
| Every test maps to a requirement | ✅ |
| Documented guidelines | ⚠️ L-019 (contract-note behaviours need tests): "Additions are serialized by a pipeline mutex" and "connector.status event published" are untested; L-002 (rate limiter concurrent burst) is not applied to the caps |

### UI checklist (web-design-guidelines, T31–T33)

- `web/src/components/room-settings-sheet.tsx:155-166`: in a catch-all room the bridge switch is disabled but still shows its normal hint. The lock reason is shown only under the broadcast switch, so a screen reader on the bridge switch hears no reason.
- `web/src/components/composer.tsx:258-262`: Send is disabled until a chat is picked, and Enter does nothing silently. The hint describes the textarea, not the Send button. This deviates slightly from the guideline "submit stays enabled until the request starts". The hint mitigates it.
- `web/src/components/connector-catch-all.tsx:206-217`: Save is disabled while nothing has changed, and A11Y-04 now explains why. This is an accepted deviation.
- The server's `warning` is not shown in the web UI. It can only arise when there is no chat to pick, and the composer then deliberately posts to agents only. Info.
- Passing: live keyword region (`aria-live="polite"`, always mounted, `empty:hidden`); `role="alert"` + `aria-describedby` on catch-all errors; counter and hint in the textarea description; pt-BR copy for every new key.

---

## Remaining Security Weaknesses (not fixed; for triage)

1. **Mass messaging with explicit `chat_ids` (medium).** Broadcast-off stops accidental fan-out only. A prompt-injected agent, or any user with post, can still list every customer chat of a catch-all room in `chat_ids`/`recipients`, and nothing caps the number of recipients per post.
2. **Spam still creates chat rows (medium-low).** `UpsertChat` runs before the scope, skip and cap checks (`internal/connectors/inbound.go:99` before `:111`). Every sender therefore persists a chat (with an attacker-chosen title), even when capped or out of scope. The hourly counter is in memory and resets on restart (contract notes accept this; see candidate L-025). After the 24 h skip, a removed spammer can be re-added.
3. **Cap serialization is untested (medium).** Surviving mutant C4: the mutex that keeps the caps exact under concurrent first messages from distinct chats can be removed with no failing test. One global mutex also serializes catch-all additions for all connectors, so a flood on one connector slows routing for the others.
4. **Unknown-author instructions (medium, test gap).** Code treats `instructions_by = ''` as untrusted, but no test pins it (T1, T2). A regression would let pre-0008 instructions written by any manager pass as trusted.
5. **Untrusted content still flows into agent context (low-medium).** Untrusted instructions are labelled but delivered verbatim. Room name and description, which any manager can edit, reach `room_name` meta and `room_info` with no trust label.
6. **Emoji validator permissiveness (low).** It accepts text-style Extended_Pictographic symbols (`©`, `®`, `™`, `‼`, arrows) and any ZWJ chain of up to 8 pictographs. Such a chain is one grapheme cluster but renders as several emoji on platforms without that RGI sequence.
7. **PII residue (low).** `chat_id` and `sender_id` still carry the full phone number to every room viewer and agent, including other owners' agents. Titles stored before T30 change only on the next inbound message, and rooms already named after a number keep that name.
8. **Mentions mode** is documented as noise-only (MEM-06). The behaviour is unchanged by design. Info.

Iteration-1 items 7 (room-existence oracle) and 8 (sticky status in DB) are fixed. The status *event* on clearing is untested (S4, S5).

---

## Fix Plans

### Fix 1: Unknown-author instructions stay untrusted (T1, T2): Major, INS-04/INS-03
Seed a room with non-empty instructions and `instructions_by = ''` (as migration 0008 leaves them). Assert `room_info.instructions_trusted == false`, `instructions_by` omitted, the tool's `instructions_note`, and the untrusted notice literal on connect.

### Fix 2: Room create records the author (T8): Major, INS-04
Create a room with instructions through `POST /rooms`. Assert `instructions_by == "<creator>"` and `instructions_trusted == true` for the creator's agent and false for another owner's agent.

### Fix 3: Caps hold under a concurrent burst (C4): Major, ROUTE-08
With 59 chats routed in the hour, emit first messages from 5 distinct new chats concurrently. Expect exactly 60 routed chats and the `auto-route limit reached` detail. Do the same with 999 room chats against the room cap.

### Fix 4: Reply to a listen-only chat in a broadcast-off room (B2): Minor, ROUTE-06
Expect no outbox, empty recipients and the exact warning.

### Fix 5: 16-code-point bound (E5): Minor, REACT-05
Accept a valid 16-code-point ZWJ sequence, for example `👩🏻‍❤️‍💋‍👨🏼` padded to 16 with a longer RGI sequence or a synthetic 8-pictograph chain of 15–16 code points. The existing 17-code-point case stays rejected.

### Fix 6: Status event on clear (S4, S5): Minor, ROUTE-09 / contract notes
Subscribe to the bus or the live WS and assert a `connector.status` event with an empty detail after a successful route and after the PATCH.

### Fix 7: Broadcast stays off after the catch-all is cleared: Minor, ROUTE-07 edge
After clearing the connector's catch-all, `GET /rooms/{id}` must still return `broadcast: false` before anyone turns it on.

---

## Requirement Traceability Update

| Requirement | Previous | New |
| --- | --- | --- |
| CTX-01..08, ROUTE-01..07, ROUTE-09, MEM-01..06, INS-01, INS-02, INS-05, REACT-01..04, ROOMDEL-01/02, A11Y-01..04 | Implementing / Implemented / re-verify | ✅ Verified (ROUTE-06, ROUTE-09 with minor test gaps B2, S4/S5) |
| ROUTE-08 | Implemented (T25) | ❌ Needs Fix (test C4) |
| INS-03, INS-04 | Implemented / re-verify | ❌ Needs Fix (tests T1, T2, T8) |
| REACT-05 | Implemented (T28) | ❌ Needs Fix (test E5) |

(spec.md not edited by the Verifier.)

---

## Summary

**Overall**: ❌ Not Ready. The behaviour is correct; 8 tests are weak or missing.

**Spec-anchored check**: 39/39 ACs evidenced; edge gaps in INS-04, REACT-05 and the ROUTE-07 "stays off" edge case
**Sensor**: 56/64 killed (B2, C4, S4, S5, E5, T1, T2, T8 survived); iteration-1 survivors G5, G23, G26 killed
**Gate**: `make check` green (28 Go packages with `-race`, 288 Vitest), e2e 8/8

**Next steps**: Fixes 1–7 are all test-only. Then run iteration 3, the last before escalating to André. Triage security items 1–3 with André.
