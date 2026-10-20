# Routing and Context Validation

**Date**: 2026-10-20
**Spec**: `.specs/features/routing-context/spec.md` (+ `design.md`, `tasks.md`, `contract-notes.md`)
**Diff range**: `bc17899..2f0931d` (T1 d7ae343 … T17 c024a07, baa125a, notes 047d8c6 / ca9d526, merge 2f0931d)
**Verifier**: independent sub-agent (author ≠ verifier), detached worktree `partyline-verify-rc` at 2f0931d

**Verdict: FAIL ❌.** The implementation meets every acceptance criterion and all gates pass. The discrimination sensor left 3 surviving mutants, all of them weak tests rather than wrong behaviour (reply-to-me identity, paused-room instructions notice, notice wording). Under validate.md, a surviving mutant is a fix task, so the feature is not done until those three tests are strengthened.

---

## Task Completion

| Task | Status | Notes |
| --- | --- | --- |
| T1–T4 (data, member settings, instructions, catch-all settings) | ✅ Done | d7ae343, e7f038a, 9267428, eab23de |
| T5–T8 (delivery filter, signing, catch-all routing, titles) | ✅ Done | 638bc55, 2acf743, b85cd41, aa0f79a |
| T9–T12 (chat context, room_info, react, platform reactions) | ✅ Done | c14b942, 511ac0d, 5d8ff49, 2ea5779 |
| T13–T17 (web settings, instructions, catch-all card, titles, delete room) | ✅ Done | e59dfc0, 584dd66, 97f8464, cadb7e1, c024a07 (+ baa125a) |

All "Done when" boxes in tasks.md are checked; no blocked or partial task.

---

## Spec-Anchored Acceptance Criteria

### P1: Agents know where messages come from

| Criterion | Spec-defined outcome | `file:line` + assertion | Result |
| --- | --- | --- | --- |
| CTX-01 chat meta keys | `chat_id`, `chat_title`, `chat_kind` (group/dm/channel), `platform`, `sender_id` | `internal/channel/chat_context_test.go:57` exact `map[string]string{… "chat_id": group, "chat_title": "Lollerie", "chat_kind": "group", "platform": "whatsapp", "sender_id": …}` (backlog); `:66` live Telegram DM; `internal/channel/format_test.go:43` unit | ✅ PASS |
| CTX-02 none for agent/user | keys absent | `internal/channel/chat_context_test.go:74` (user) and `:82` (agent): exact meta maps without chat keys | ✅ PASS |
| CTX-03 history items | same chat fields as push | `internal/channel/chat_context_test.go:103` exact history item map incl. `chat_kind: "channel"`, `platform: "discord"`; `:116` timeline `ChatRef` | ✅ PASS |
| CTX-04 instructions text | explains chat fields and `chat_ids` | `internal/channel/format_test.go:145` (`chat_kind="group\|dm\|channel"`, `platform=…`, `sender_id`); `internal/channel/room_info_test.go:114` (`chat_ids`, `room_info`); behaviour `room_info_test.go:92` (`Recipients == [family]`, union with `recipients`) | ✅ PASS |

### P1: Chat titles and room names

| Criterion | Spec-defined outcome | `file:line` + assertion | Result |
| --- | --- | --- | --- |
| CTX-05 group subject | `POST /group/info {"groupJid": jid}` → `data.Name` stored as title | `internal/connectors/whatsapp/connector_test.go:612` — `ChatTitle == "Lojinha Sintética"`, body `== {"groupJid": groupJID}`, instance token; fixture is the captured 0.7.2 shape (`connector_test.go:71-80`) | ✅ PASS |
| CTX-06 DM title | push name, else phone number | `internal/connectors/whatsapp/parse_test.go:242` — `ChatTitle == "5511999990000"` for empty and blank push name, incl. LID chat; `connector_test.go:641` push name "Test Person" | ✅ PASS |
| CTX-07 routed room name | title, else `<Platform> chat` | `internal/api/room_names_test.go:19` — `"Lojinha Sintética"`, `"WhatsApp chat"`, `"Telegram chat"`, `"Discord chat"`, `"Webhook chat"`, 100-char cut; web `web/src/components/message-timeline.test.tsx:106`, `web/src/routes/_app/rooms/room.test.tsx:189`, `connectors.test.tsx:259` | ✅ PASS |
| CTX-08 title update | next refresh / inbound updates stored title | `internal/connectors/inbound_test.go:433` — title becomes `"Support group (renamed)"`, empty title keeps it; `whatsapp/connector_test.go:612` cache expiry after 10 min shows `"Renamed Group"`; `:648` list refresh fills cache | ✅ PASS |

### P1: Catch-all routing

| Criterion | Spec-defined outcome | `file:line` + assertion | Result |
| --- | --- | --- | --- |
| ROUTE-01 scope matrix | unrouted chat of matching kind joins the room with outbound on and is delivered | `internal/connectors/catch_all_test.go:85` (dms: DM delivered, `members[chatA].Outbound` true, group not added); `:124` groups → group+channel yes, DM no; off → none; all → DM+group | ✅ PASS |
| ROUTE-02 already routed | not added to catch-all room | `catch_all_test.go:110` `NotContains(members, dmB)` and `:111` message stored only in the other room | ✅ PASS |
| ROUTE-03 room unavailable | stop routing, status detail `auto-route room unavailable` | `catch_all_test.go:211` deleted room → `LastError == "auto-route room unavailable"`, nothing stored (`:212`); `:230` lost manage → same detail, `:232-234` nothing stored/added | ✅ PASS |
| ROUTE-04 manage required | 403 without manage | `internal/api/catch_all_settings_test.go:229` post-only and hidden rooms → 403 and unchanged; `:261` second owner 403 until a manage grant; unknown room 422 `:235`, bad scope 422 `:238` | ✅ PASS |
| ROUTE-05 trust | auto-routed senders `trusted=false` unless owner's verified identity | `catch_all_test.go:97` `False(got.Trusted)`; `:119` verified owner identity `True(got.Trusted)` | ✅ PASS |

### P1: Agent membership settings

| Criterion | Spec-defined outcome | `file:line` + assertion | Result |
| --- | --- | --- | --- |
| MEM-01 signature off | no author header outbound | `internal/connectors/sender_test.go:217` `sent.AuthorName == ""`; adapters drop the header for an empty name: `connectors/text_test.go:36`, `whatsapp/connector_test.go:330` (`text == "hi"`), Telegram/Discord split tests join to the raw text | ✅ PASS |
| MEM-02 display name | outbound header and timeline use it | `sender_test.go:209` `sent.AuthorName == "Lolo"`; `:212` timeline `author_name == "Lolo"`; `:230` other room keeps `"helper"` | ✅ PASS |
| MEM-03 mentions | only mentions (DM, reply-to-me, `@name` ci+boundary, keyword boundary) delivered; all stored | `internal/delivery/wants_test.go:15` table (DM `:36`, reply `:39`, case `:41`, `@builders` `:45`, keyword in word `:51`); live `internal/api/delivery_filter_test.go:65` exact frame order, `:95` 13 stored; backlog `:117` skip + cursor | ✅ PASS (reply-to-me identity weak, see sensor G5) |
| MEM-04 trusted | only `trusted=true` | `wants_test.go:30`; live `delivery_filter_test.go:100` (bob's message only, `Trusted` true) | ✅ PASS |
| MEM-05 limits | 65-char name / 21 keywords / 65-char keyword → 422 | `internal/api/member_settings_test.go:108,116,120` 422 on field; boundaries 64 code points `:110`, 20 keywords `:118`; rejected PATCH applies nothing `:128-133`; non-agent → 422 `:138`; viewer 403 / stranger 404 `:91-92` | ✅ PASS |

### P2: Room instructions

| Criterion | Spec-defined outcome | `file:line` + assertion | Result |
| --- | --- | --- | --- |
| INS-01 | ≤4,000 stored, 4,001 → 422 | `internal/api/room_instructions_test.go:17` (4,000 multibyte code points stored, 4,001 → 422 field `instructions`, viewer 403, stranger 404); web `room.test.tsx:391,402,413` | ✅ PASS |
| INS-02 room_info | name, instructions, members (kind, name, platform, chat kind), own settings | `internal/api/room_info_test.go:23` (`Room`, `Instructions`, `Me.*`, chat member `ConnectorKind`/`ChatKind`); tool `internal/channel/room_info_test.go:34` exact maps; non-member 403 `api/room_info_test.go:55` | ✅ PASS |
| INS-03 notice | one notice per room with instructions, stating they exist and to call `room_info` | `api/room_info_test.go:67` one notice per room before its messages, repeated per connection; `channel/room_info_test.go:71` | ⚠️ Spec-precision gap: the text is asserted against the production constant, never against the spec wording (mutant G23 survives); paused rooms (contract notes) untested (G26) |

### P2: Reactions

| Criterion | Spec-defined outcome | `file:line` + assertion | Result |
| --- | --- | --- | --- |
| REACT-01 | reaction sent to origin chat via its connector | `internal/connectors/reaction_test.go:21` exact `connectors.Reaction{ChatID, TargetID: "M1", TargetSenderID, Emoji …}`; WhatsApp `whatsapp/react_test.go:15` (participant in groups) / `:32` (no participant in DMs); Telegram `telegram/react_test.go:16` integer `chat_id`/`message_id`, FE0F dropped, `:32` emoji outside set refused before any call; Discord `discord/react_test.go:14`; webhook `internal/api/hook_reaction_test.go:18` exact signed payload | ✅ PASS |
| REACT-02 | `isError` with `can only react to chat messages` | `internal/channel/react_test.go:31` `isErr` and text equal; API 422 `internal/api/react_test.go:41` | ✅ PASS |

### P1: Delete a room

| Criterion | Spec-defined outcome | `file:line` + assertion | Result |
| --- | --- | --- | --- |
| ROOMDEL-01 | managers only; alert dialog states messages, attachments, pending deliveries removed | `web/src/routes/_app/rooms/room.test.tsx:452` exact dialog text; `:489` not offered without manage | ✅ PASS |
| ROOMDEL-02 | navigate `/rooms`, toast, list updated without reload | `room.test.tsx:457` pathname `/rooms`; `:459` toast "deploys deleted"; `:460` cache `["r2"]` with a refetch that never answers | ✅ PASS |

**Status**: 25/25 ACs have spec-anchored evidence. Spec-precision gaps: INS-03 notice wording; Success Criterion "DM reaches the agent within 2 seconds" has no AC and no test (non-functional, manual UAT only).

---

## Edge Cases

- [x] Two first messages at once add the chat once: `catch_all_test.go:153` (4 concurrent emits → 1 member, 4 messages); mutant G9 killed.
- [x] Removed catch-all chat not re-added for 24 h unless a manager adds it: `catch_all_test.go:172` (fake clock 24h−1m out, 24h+1m in; manual add at once); G10, G24 killed.
- [x] Keyword inside a longer word does not match: `wants_test.go:51-55` (`redeploy`, `deployment`, accents, underscore); G3b killed.

---

## Discrimination Sensor

Scratch: `git worktree add --detach /tmp/claude-1001/pl-rc-mutants 2f0931d`, one exact-string mutation at a time, targeted `go test -count=1` / `bun x vitest run`, file restored after each, worktree removed. Real-tree `git status --porcelain` empty before and after (match).

| # | File:line | Mutation | Killed? |
| --- | --- | --- | --- |
| G1 | `internal/delivery/wants.go:53` | DM chats no longer always delivered in mentions mode | ✅ |
| G2 | `wants.go:75` | mention match case-sensitive | ✅ |
| G3b | `wants.go:84` | right word boundary ignored | ✅ |
| G4 | `wants.go:62` | keywords ignored | ✅ |
| G5 | `wants.go:129` | reply-to-me true for a reply to **any** agent | ❌ Survived |
| G6 | `internal/delivery/dispatcher.go:145` | live push ignores the delivery filter | ✅ |
| G7 | `internal/agents/gateway.go:430` | backlog of only skipped messages no longer advances the cursor | ✅ |
| G8 | `internal/connectors/inbound.go:198` | scope `groups` excludes channel chats | ✅ |
| G9 | `inbound.go:181` | concurrent `ErrAlreadyMember` treated as failure | ✅ |
| G10 | `inbound.go:169` | 24 h skip comparison inverted | ✅ |
| G11 | `inbound.go:184` | lost manage does not set the status detail | ✅ |
| G12 | `internal/connectors/service.go:411` | `auto_route_room_id` needs only view | ✅ |
| G13 | `internal/rooms/members.go:176` | agent-only settings accepted for chats | ✅ |
| G14 | `members.go:216` | keyword count limit 21 | ✅ |
| G15 | `internal/connectors/sender.go:69` | `sign_outbound=false` ignored | ✅ |
| G16b | `internal/connectors/whatsapp/react.go:23` | participant sent in DMs too | ✅ |
| G17 | `internal/connectors/telegram/react.go:51` | `chat_id` sent as string | ✅ |
| G18 | `internal/connectors/whatsapp/parse.go:132` | DM title fallback to number removed | ✅ |
| G19 | `whatsapp/connector.go:223` | group subject cached forever | ✅ |
| G20 | `internal/connectors/chats.go:143` | untitled room named after the raw id | ✅ |
| G21 | `internal/channel/format.go:56` | `sender_id` meta dropped | ✅ |
| G22 | `internal/api/agent_handlers.go:262` | react accepts non-chat targets | ✅ |
| G23 | `gateway.go:49` | `InstructionsNotice` text replaced by "hello" | ❌ Survived |
| G24 | `members.go:296` | removal writes no skip row | ✅ |
| G25 | `internal/rooms/rooms.go:123` | instructions limit 4,001 | ✅ |
| G26 | `gateway.go:358` | no instructions notice for paused rooms | ❌ Survived |
| G27 | `inbound.go:175` | deleted room does not set the status detail | ✅ |
| G28 | `agent_handlers.go:272` | reaction without the target chat as recipient | ✅ |
| G29 | `inbound.go:105` | auto-route also chats already in a room | ✅ |
| W1 | `web/src/components/member-settings.tsx:113` | display name always sent | ✅ |
| W2 | `web/src/components/member-list.tsx:189` | settings shown without manage | ✅ |
| W3 | `member-list.tsx:189` | settings shown for chat members | ✅ |
| W4 | `web/src/components/connector-catch-all.tsx:52` | picker offers rooms without manage | ✅ |
| W5 | `connector-catch-all.tsx:66` | scope off keeps the room id | ✅ |
| W6 | `connector-catch-all.tsx:186` | exposure warning shown while off | ✅ |
| W7 | `web/src/components/room-settings-sheet.tsx:88` | instructions always sent | ✅ |
| W8 | `web/src/lib/api/queries.ts:297` | delete invalidates instead of updating the list cache | ✅ |
| W9 | `room-settings-sheet.tsx:264` | no navigation after delete | ✅ |
| W10 | `web/src/components/message-item.tsx:80` | untitled chat fallback lost | ✅ |
| W11 | `web/src/lib/schemas.ts:74` | instructions counted in UTF-16 units | ✅ |
| W12 | `schemas.ts:145` | keyword length limit removed | ✅ |
| W13 | `room-settings-sheet.tsx:263` | no toast after delete | ✅ |
| W14 | `message-item.tsx:245` | platform badge shows the raw kind | ✅ |

Invalid attempts discarded (did not compile): G3 (unused variable) and G16 (unused import). They were replaced by G3b and G16b.

**Sensor depth**: expanded (P0 paths: catch-all exposure, trust, delivery filter)
**Result**: 41/44 killed. **FAIL ❌** (3 survivors become fix tasks)

---

## Gate Check

- **Build gate**: `make check` → EXIT 0. It covers generate drift, golangci-lint, vet, biome, typecheck, `go test -race ./...` (27 packages ok), Vitest **276 passed / 29 files**, web build and `go build`.
- **E2E**: `make e2e` (Playwright, pinned chromium-headless-shell 1243) → **8 passed**, EXIT 0, first run.
- **Test count before feature** (`bc17899`): 443 Go test funcs, 216 web cases. **After** (`2f0931d`): 501 Go, 258 web. **Delta**: +58 Go, +42 web.
- **Skipped**: none. **Failures**: none. No flaky re-run was needed.
- **Integrity**: the removed test lines are signature adaptations only (6→`len(required)` tools, `UpdateMember` call shape, fake send-error routing). No assertion was weakened.

---

## Code Quality

| Principle | Status |
| --- | --- |
| Minimum code / surgical changes / no scope creep | ✅ (reaction storage reuses outbox; `PlatformLabel` mirrored in web) |
| Matches patterns | ✅ (validation.Check, sentinel errors, tygo DTOs) |
| Spec-anchored outcome check | ⚠️ INS-03 wording asserted via constant |
| Per-layer coverage (domain 1:1; routes happy+edge+error) | ⚠️ react to a listen-only chat (422 `recipients`) and webhook with signature off are untested |
| Every test maps to a requirement | ✅ |
| Documented guidelines followed | ✅ AGENTS.md, contract notes, LESSONS.md (L-030 captured fixtures applied) |

### UI checklist (web-design-guidelines, T13–T17)

- `web/src/components/member-settings.tsx:400`: the keyword draft and limit message is a plain `<p>`, with no `aria-live` and not linked to the input by `aria-describedby`.
- `member-settings.tsx:143`: a keyword typed but not committed (no Enter or +) is silently dropped on Save.
- `web/src/components/connector-catch-all.tsx:125`: the scope error is not linked to the radio group and has no live region. The room error is linked (`:157`) but is not announced.
- `connector-catch-all.tsx:202`: Save stays disabled until something changes. The guideline says submit stays enabled until the request starts, so this is a minor deviation.
- `web/src/components/room-settings-sheet.tsx:162`: the instructions counter is not tied to the textarea, so screen readers get no remaining-characters cue.
- Passing: icon buttons are labelled; decorative icons are `aria-hidden`; the destructive action is behind an AlertDialog; radios share one hit target with their labels; `spellCheck={false}` and `autoComplete="off"` are set on keywords; the pt-BR copy is present.

---

## Security Weaknesses (not fixed; for triage)

1. **Cross-customer broadcast in catch-all rooms (high).** A catch-all room fills with one chat per customer, each with outbound on. Any `send_message` without `chat_ids`, or any web post, goes to **every** customer, and `bridge_chats` on that room would relay customers to each other. Scoping a message to one chat is only advice in the instructions. Consider defaulting catch-all rooms to reply-only or forcing `chat_ids` there.
2. **Unbounded catch-all exposure (medium).** Anyone who DMs the number, or adds the bot to a group under `groups`/`all`, creates a chat and a room member. There is no cap or rate limit per connector or room, and after the 24 h skip the same spammer is re-added. Room members, agent context and outbox can be flooded. Only `delivery=trusted` protects the agent.
3. **Mentions mode is not a security filter (medium).** Any chat author can force delivery with `@<agent name>` (the name shows in every signed message), a guessed keyword, a quote of the agent's message, or simply a DM, because every DM is always delivered. Catch-all DMs therefore always reach mention-mode agents.
4. **Reaction abuse (low).** `oneEmoji` (`internal/api/agent_handlers.go:283`) accepts any 1–16 code points without letters or spaces, such as digits, punctuation or `<>`. A prompt-injected agent can send arbitrary symbol "reactions" to WhatsApp, Discord and webhooks. Telegram limits this to its own set. The shared 30/min budget bounds volume, and failed reactions retry until `MaxAttempts`.
5. **Instructions as an injection channel (medium).** Any user with manage on a shared room can write instructions that other owners' agents read through `room_info` (a tool result, not a `trusted=false` frame). The instructions text says they never override trust, but tool output is not marked untrusted.
6. **PII exposure (low).** A DM title falls back to the phone number (CTX-06) and shows in member lists, `room_info` and `list_rooms` for every room viewer and every agent (including other owners'). Attacker-controlled push names and group subjects flow into `chat_title` meta and room names.
7. **Room-existence oracle (low).** `PATCH /connectors/{id}` answers 422 "unknown room" for a missing id but 403 for an existing room the caller cannot see (`internal/connectors/service.go:403-411`).
8. **Sticky status detail (info).** `unavailable()` does not re-publish once `last_error` already equals the detail, and the detail clears only on the next status report, so a repaired catch-all can show "unavailable" until a reconnect.

---

## Fix Plans

### Fix 1: Reply-to-me must name this agent (G5)
- **Root cause**: `internal/api/delivery_filter_test.go:84` uses a room with one agent, so a check on author kind alone passes.
- **Fix task**: in a room with two agents in mentions mode, a reply to agent A's message is pushed to A and not to B (live and backlog).
- **Priority**: Major (MEM-03)

### Fix 2: Instructions notice for paused rooms (G26)
- **Root cause**: contract notes promise the notice for paused rooms, and no test pauses a room.
- **Fix task**: pause a room with instructions (loop guard), connect, and expect the notice and no messages.
- **Priority**: Minor (INS-03)

### Fix 3: Assert the notice wording (G23)
- **Root cause**: tests compare against `agents.InstructionsNotice` itself.
- **Fix task**: assert that the notice text mentions instructions and `room_info` (literal or `Contains`), in both the api and channel tests.
- **Priority**: Minor (INS-03)

---

## Requirement Traceability Update

| Requirement | Previous | New |
| --- | --- | --- |
| CTX-01..08, ROUTE-01..05, MEM-01, MEM-02, MEM-04, MEM-05, INS-01, INS-02, REACT-01, REACT-02, ROOMDEL-01, ROOMDEL-02 | Implementing | ✅ Verified |
| MEM-03 | Implementing | ❌ Needs Fix (test G5) |
| INS-03 | Implementing | ❌ Needs Fix (tests G23, G26) |

(spec.md not edited by the Verifier.)

---

## Summary

**Overall**: ❌ Not Ready (test-strength gaps only; behaviour correct)

**Spec-anchored check**: 25/25 ACs evidenced; 1 spec-precision gap (INS-03 wording) plus the untested 2 s success criterion
**Sensor**: 41/44 mutations killed (G5, G23, G26 survived)
**Gate**: `make check` green (Go race suite, 276 web tests), e2e 8/8

**Next steps**: Fixes 1–3 (tests only), then re-verify (iteration 1 of 3). Triage security items 1–3 with André.
