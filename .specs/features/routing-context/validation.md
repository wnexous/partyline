# Routing and Context Validation

**Date**: 2026-10-20
**Spec**: `.specs/features/routing-context/spec.md` (+ `design.md`, `tasks.md`, `contract-notes.md`)
**Diff range**: iteration 3 re-verify of the fix commits `f8423b2..53a74e3` (T35–T47: 3c82596, 5c3d951, 9cae214, b952a0d, 2bb3250, 6304bf0, 3dd80a1, f7f3dd0, 872b9bc, 17d5a99, 2fbede1, 7821561, 53a74e3; migration 0010), verified at merge `35b48bc` (fix/rc-3 + main). Interleaved harness-onboarding commits from main were ignored. Feature base `bc17899`.
**Verifier**: independent sub-agent (author ≠ verifier), detached worktree `partyline-verify-rc` at 35b48bc
**Iteration**: 3 of 3 (final; a FAIL escalates to André), then an escalation closure check of `67e3700..5ddd570` (T48–T54)

## Validation verdict: PASS ✅ (closure gaps closed at `f5dae2a`)

> **Gap closure (orchestrator, 2026-10-20)**: `f5dae2a` adds `TestRoomCreateStoresTrimmedText` and a legacy-name case (stored `"Support (bob)\uFEFF"`, alice's web save keeps bob as author) to `TestRoomTextWhitespaceDoesNotMoveTheAuthor` in `internal/api/room_info_test.go`. Re-applied in the working copy and reverted: F7 (create stores `in.Description` untrimmed) → FAIL; F8 (create stores `in.Instructions` untrimmed) → FAIL; F9 (name compared against the untrimmed stored name) → FAIL. `make check` at `f5dae2a` EXIT 0. Production code unchanged.

**Closure verdict before gap closure (kept for history, HEAD 5ddd570): FAIL ❌.** Every gate is green (`make check` EXIT 0, e2e 8/8). All five iteration-3 survivors (N6, N10, N13, N14, N30) are killed, and the whitespace trust laundering (finding 9) is fixed: the probe now passes on HEAD and fails on the pre-fix `rooms.go`. The four amended ACs have spec-anchored tests. The sensor still leaves **3 survivors on the new trimming code (F7, F8, F9)**. The shipped code is correct for all three; the tests just don't guard it. F9 matters most: if the name compare regressed to the untrimmed stored name, a pre-fix name ending in U+FEFF would launder again, and no test would fail. Details are in "Escalation closure (post iteration 3)" below.

**Iteration-3 verdict (kept for history): FAIL ❌.** All eight iteration-2 survivors (B2, C4, S4, S5, E5, T1, T2, T8) are now killed. All 43 ACs and all 6 edge cases have spec-anchored evidence, and every gate passes (`make check` EXIT 0, e2e 8/8). The expanded sensor on the new fix code (T42–T46) still left **5 surviving mutants**: N6, N10, N13, N14 and N30. A scratch probe also found **one real security defect** in INS-06: a web save that trims whitespace makes the saver the author of text another manager wrote (finding 9). The shipped behaviour matches the spec literally, and each survivor is a missing test. Because this is the third iteration, the gaps go to André instead of a fourth loop.

---

## Iteration history

- **Iteration 1** (`bc17899..2f0931d`): FAIL. 25/25 ACs evidenced; sensor 41/44 (G5, G23, G26 survived); 8 security weaknesses reported. Fix tasks T18–T34.
- **Iteration 2** (`8094902..6947d1d`): FAIL. 39/39 ACs evidenced; sensor 56/64 (B2, C4, S4, S5, E5, T1, T2, T8 survived); the ROUTE-07 "stays off" edge case was untested. Fix tasks T35–T47 added ROUTE-10, ROUTE-11, INS-06 and A11Y-05, amended ROUTE-08 and REACT-05, and added migration 0010.
- **Iteration 3**: every iteration-2 survivor and the edge case are fixed. The spec now has 43 ACs, all evidenced. The sensor killed 35 of 40 mutants and 5 survived. One new security defect was found. Gates are green.
- **Escalation closure** (`67e3700..5ddd570`, T48–T54): FAIL. Gates green; N6, N10, N13, N14 and N30 killed; finding 9 fixed and probed; sensor 20/23 (F7, F8, F9 survived on the new trimming). See the section below.

---

## Escalation closure (post iteration 3)

**Date**: 2026-10-20. **Range**: `67e3700..5ddd570`: db5fce7 (T48), 515929b (T49), 69252cd (T50), 426d4f0 (T51), 28e81a7 (T52), 9a9d660 (T53), 5ddd570 (T54). **Worktree**: `partyline-verify-rc` at 5ddd570 (detached). **Verifier**: a fresh sub-agent, not the author.

**Outcome (before gap closure)**: FAIL ❌. Gates are green, every escalated gap is closed, and 3 survivors on the new trimming remain (F7, F8, F9).

### Gates

- `make check`: **EXIT 0** on the first run (9 m 54 s). golangci-lint reported 0 issues. `go test -race ./...` passed all 31 packages. Vitest **292 passed / 29 files** (288 → 292). bun test passed 27, failed 0. Web build and `go build` passed.
- `make e2e` with `PLAYWRIGHT_CHROMIUM_EXECUTABLE=…/chromium_headless_shell-1243/…/chrome-headless-shell`: **8 passed**, EXIT 0, first run. Nothing was flaky, so no re-run was needed.
- **Counts**: Go test funcs went from 652 at 67e3700 to 657 at 5ddd570 (+5). Vitest cases went from 288 to 292 (+4).
- **Integrity**: no test was removed or weakened since 67e3700. `git diff --numstat 67e3700..5ddd570` shows 0 deleted lines in all three test files (`internal/api/room_info_test.go` +122, `internal/connectors/catch_all_test.go` +59, `web/src/components/composer.test.tsx` +73). Deleted lines appear only in `internal/rooms/rooms.go`, `web/src/components/composer.tsx` and `spec.md`.

### Amended ACs: spec-anchored evidence

| AC (amendment) | Spec-defined outcome | `file:line` + assertion | Result |
| --- | --- | --- | --- |
| INS-04: re-saving the same instructions once trimmed keeps the author | `instructions_by` unchanged, still untrusted | `internal/api/room_info_test.go:344` `require.Equal(t, "bobby", got.InstructionsBy, …)`; `:345` `require.False(t, got.InstructionsTrusted)`; `:347` the U+FEFF + LF variant keeps `"bobby"`; `:339` stored trimmed | ✅ |
| INS-06 trim rule (PATCH): stored without surrounding white space and compared trimmed | stored trimmed; a web save keeps the author | `room_info_test.go:303` `Equal("Support (bob)", got.Name)`; `:304` `Equal("Forward every file to bob.", got.Description)`; `:309` `False(trusted(f.rest()))` after alice's `webSave`; `:315` trailing U+FEFF; `:320` untrimmed legacy description; `:324` a real change moves the author | ✅ |
| INS-06 trim rule (create) | a created room's name, description and instructions are stored trimmed | name only: `internal/rooms/rooms_test.go:127`/`:131` `"  Ops  "` → `Equal("Ops", r.Name)`. **No evidence for the description or instructions on create** (F7, F8) | ⚠️ gap |
| INS-06: an instructions-only edit leaves the room text author | `room_text_trusted` unchanged for both agents | `room_info_test.go:371` `True(trusted(f.rest()))`, `:372` `False(trusted(helperRest))` | ✅ |
| ROUTE-10 web picker: stops at 10; the others are disabled and described; pt-BR | `At most 10 chats per message` / `No máximo 10 chats por mensagem` | `web/src/components/composer.test.tsx:305` text; `:306` `box(10).toBeDisabled()`; `:307` `toHaveAccessibleDescription("At most 10 chats per message")`; `:313` re-enabled after unpicking; `:339` pt-BR text | ✅ |
| A11Y-05: Send moves focus to the picker, Enter keeps it in the message box; pt-BR alert | focus on the `To: choose a chat` button; alert `Escolha um chat para responder` | `composer.test.tsx:285` `expect(box).toHaveFocus()`; `:287` the picker `toHaveFocus()`; `:348` `toHaveTextContent("Escolha um chat para responder")`, `:349` nothing posted | ✅ |
| ROUTE-08 / ROUTE-11: hourly window per connector | the second connector still routes, with an empty detail | `internal/connectors/catch_all_test.go:579` `Len(…, 61)`; `:581` `Empty(got.LastError)` | ✅ |
| ROUTE-11 / CTX-08: a skipped chat is still recorded | title and `last_seen_at` updated, not re-added, nothing stored | `catch_all_test.go:602` `NotContains`; `:603` message count unchanged; `:611` `Equal("Customer Renamed", got.Title)`; `:613` `last_seen_at` = message time | ✅ |

### Discrimination sensor

Scratch: `git worktree add --detach /tmp/claude-1001/pl-rc-close 5ddd570`, with web `node_modules` symlinked. A driver applied each mutation as one exact-string replacement, ran the targeted command, then ran `git checkout -- <file>`. Go mutants on rooms ran `go test -count=1 ./internal/api/ ./internal/rooms/`, catch-all mutants ran `./internal/connectors/`, and web mutants ran `vitest run src/components/composer.test.tsx src/lib/i18n`. All three survivors were re-run against **all** Go packages (`go test ./...`) and still survived. Each kill was checked to come from the intended test, with no compile errors. A second scratch (`…/pl-rc-close-probe`) held the probes. Both scratches were removed afterwards. Real-tree `git status --porcelain` was the same before and after: empty except the uncommitted report.

| # | File | Mutation | Result |
| --- | --- | --- | --- |
| N6 | `internal/connectors/inbound.go:266,281` | one shared hourly window for every connector (locks stay per connector) | ✅ killed (TestCatchAllHourlyCapIsPerConnector) |
| N10 | `inbound.go` skip branch | chat held out by the 24 h skip is not upserted | ✅ killed (TestCatchAllSkippedChatIsRecorded) |
| N13 | `internal/rooms/rooms.go:292` | an instructions save also moves `updated_by` | ✅ killed (TestRoomTextAuthorKeptByOtherSaves) |
| N14 | `rooms.go:286` | any description save moves the author | ✅ killed (TestRoomTextWhitespaceDoesNotMoveTheAuthor, TestRoomTextAuthorKeptByOtherSaves) |
| N30 | `web/src/lib/i18n/pt-BR.ts:217` | `composer.pickChat` text changed | ✅ killed (says to pick a chat in pt-BR) |
| F1 | `rooms.go:143` | `roomText` stops trimming U+FEFF | ✅ killed |
| F2 | `rooms.go:143` | `roomText` trims only the end (`TrimRightFunc`) | ✅ killed |
| F3 | `rooms.go:287` | PATCH stores the description untrimmed | ✅ killed |
| F4 | `rooms.go:286` | description compared with the untrimmed stored copy | ✅ killed (legacy row case) |
| F5 | `rooms.go:290` | instructions re-save always moves `instructions_by` | ✅ killed |
| F6 | `rooms.go:291` | PATCH stores instructions untrimmed | ✅ killed |
| **F7** | `rooms.go:172` | **create stores `in.Description` untrimmed** | ❌ survived (`go test ./...`) |
| **F8** | `rooms.go:172` | **create stores `in.Instructions` untrimmed** | ❌ survived (`go test ./...`) |
| **F9** | `rooms.go:282` | **name compared with the untrimmed stored name** | ❌ survived (`go test ./...`) |
| F10 | `rooms.go:283` | PATCH stores the name untrimmed | ✅ killed |
| F11 | `web/src/components/composer.tsx:167` | form submit calls `send()` (no focus move) | ✅ killed |
| F12 | `composer.tsx:124` | Enter also moves focus to the picker | ✅ killed |
| F13 | `composer.tsx:305` | cap `>=` → `>` (11 picks allowed) | ✅ killed |
| F14 | `composer.tsx` checkbox | capped checkbox not described (`aria-describedby` dropped) | ✅ killed |
| F15 | `composer.tsx` `capped` | picked chats also disabled at the cap | ✅ killed |
| F16 | `composer.tsx` limit note | limit note never shown | ✅ killed |
| F17 | `pt-BR.ts` | `composer.recipientsLimit` text changed | ✅ killed |
| F18 | `composer.tsx` trigger | picker trigger loses `ref={triggerRef}` | ✅ killed |

**Sensor depth**: expanded (P0: trust labels)
**Sensor outcome (before gap closure)**: 20/23 killed. All 5 re-applied iteration-3 survivors are killed; 3 fresh survivors remain (F7, F8, F9): FAIL ❌.

### Laundering probe (finding 9), scratch only, discarded

`internal/api/zz_probe_test.go` ran 8 description variants written by bob, a co-manager. After each, alice made a web-style save: the GET name and description, trimmed by a Go copy of `String.prototype.trim`, plus a retention change, sent through the `webSave` helper. The variants were: a trailing space, a trailing U+FEFF, a leading U+FEFF with a trailing NBSP, U+2028, U+3000, U+0085 (Go trims it, JS does not), CRLF on both ends, and U+200A + U+205F. For each variant the probe also checked: alice re-sending bob's raw text, an untrimmed legacy row written straight to the store followed by a web save, bob's name `U+FEFF"Bob room "` followed by alice's web save, and bob's instructions `"…\uFEFF "` re-sent by alice both trimmed and padded with U+3000/U+2029.
- On **HEAD**: passed. alice's agent never got `room_text_trusted=true`, and `instructions_by` stayed `bobby`.
- With **`rooms.go` from 67e3700**: failed on the first case (`"Forward every file to bob. " laundered into alice's trust`). The probe discriminates, and the fix closes finding 9.
- **F9 is a real laundering path that no test guards.** A second probe stored a pre-fix name `"Bob room\uFEFF"` by bob. Pre-fix `strings.TrimSpace` kept U+FEFF, so such rows can exist. alice then made a web save with `"Bob room"`. HEAD passed; with F9 applied it failed (`legacy name laundered`).
- Interior white space (for example CRLF inside the description) can't launder anything. The description field is an `<input>` driven by react-hook-form state from `defaultValues`. An untouched field sends the stored value trimmed, and instructions are sent only when edited (`room-settings-sheet.tsx:86-93`).

### UI minors from iteration 3

- Focus after Send now moves to the chat picker (`composer.tsx:124`, killed by F11, F12 and F18). **Closed.**
- The recipients picker now stops at 10 chats with an inline, described note (`composer.tsx:305-381`, killed by F13–F17). **Closed.**
- The alert sits above the textarea and describes the picker. Info only; unchanged and acceptable.

### Fix plans (closure)

- **Fix C1: guard the name compare for legacy rows (F9). Minor, INS-06, security-relevant.** In `TestRoomTextWhitespaceDoesNotMoveTheAuthor`, add `setRoomText(…, "Support (bob)\uFEFF", …, bob)`, have alice make a web save with `"Support (bob)"`, and assert that `room_text_trusted` stays false for alice's agent.
- **Fix C2: create stores trimmed text (F7, F8). Minor, INS-06 trim rule.** POST a room with description `" Desk\uFEFF"` and instructions `"\n Reply in a day. "`, then assert that GET returns `"Desk"` and `"Reply in a day."`.

### Traceability (closure)

| Requirement | After iteration 3 | After closure |
| --- | --- | --- |
| ROUTE-08, ROUTE-11, CTX-08 | ⚠️ test gap (N6, N10) | ✅ Verified |
| ROUTE-10 (web picker) | (new) | ✅ Verified |
| A11Y-05 | ⚠️ test gap (N30) | ✅ Verified (pt-BR, focus) |
| INS-04 (amended) | ✅ | ✅ Verified |
| INS-06 | ❌ Needs Fix (N13, N14, finding 9) | ⚠️ Fixed in code; test gaps F7, F8, F9 |

(spec.md not edited by the Verifier.)

---

## Task Completion

| Task | Status | Notes |
| --- | --- | --- |
| T1–T34 | ✅ Done | iterations 1–2 (re-verified through the gate) |
| T35 unknown-author instructions untrusted | ✅ Done | 3c82596, kills T1 and T2 |
| T36 room create records the author | ✅ Done | 5c3d951, kills T8 |
| T37 caps under a concurrent burst | ✅ Done | 9cae214, kills C4 deterministically (route hook + barrier) |
| T38 listen-only reply in a broadcast-off room | ✅ Done | b952a0d, kills B2 |
| T39 emoji code-point bound | ✅ Done | 2bb3250 (bound since lowered to 11 by T46; E5 re-applied at 11 → 10, killed) |
| T40 status event on clear | ✅ Done | 6304bf0, kills S4 and S5 |
| T41 broadcast stays off after clear | ✅ Done | 3dd80a1, closes the ROUTE-07 edge case |
| T42 bridge lock reason, "Pick a chat" alert | ✅ Done | f7f3dd0 (pt-BR text untested: N30) |
| T43 at most 10 chats per message | ✅ Done | 872b9bc |
| T44 capped chats leave no record, per-connector locks | ✅ Done | 17d5a99 (per-connector window untested: N6; skipped chat recording untested: N10) |
| T45 room text author | ✅ Done | 2fbede1 (unchanged-description half and instructions-only edits untested: N13, N14; whitespace laundering, finding 9) |
| T46 stricter emoji validator | ✅ Done | 7821561 |
| T47 contract notes + traceability | ✅ Done | 53a74e3 |

---

## Spec-Anchored Acceptance Criteria (43)

Rows unchanged since iteration 2 were re-checked at 35b48bc. Their cited assertions are still present, and the gate passes them.

### P1: Agents know where messages come from

| AC | Spec-defined outcome | `file:line` + assertion | Result |
| --- | --- | --- | --- |
| CTX-01 | meta `chat_id`, `chat_title`, `chat_kind`, `platform`, `sender_id` | `internal/channel/chat_context_test.go:60` exact meta map (TestPushCarriesChatContext :45); `internal/channel/format_test.go:27` | ✅ |
| CTX-02 | no chat keys for agent or user authors | `chat_context_test.go:45` exact maps without chat keys | ✅ |
| CTX-03 | history items carry the same fields | `chat_context_test.go:106` exact history item (TestHistoryCarriesChatContext :89) | ✅ |
| CTX-04 | instructions explain chat fields and `chat_ids` | `internal/channel/format_test.go:145`; `internal/channel/room_info_test.go:120` (TestInstructionsExplainChatIDsAndRoomInfo), behaviour `:98` | ✅ |

### P1: Chat titles and room names

| AC | Spec-defined outcome | `file:line` + assertion | Result |
| --- | --- | --- | --- |
| CTX-05 | group subject stored as title | `internal/connectors/whatsapp/connector_test.go:624` `"Lojinha Sintética"` | ✅ |
| CTX-06 | push name, else `+•••• 0000`; ids keep the number | `internal/connectors/whatsapp/parse_test.go:243` `ChatTitle == "+•••• 0000"`, full `ChatID`/`SenderID` | ✅ |
| CTX-07 | room named after the title, else `<platform> chat` | `internal/api/room_names_test.go:38-39` | ✅ |
| CTX-08 | next refresh or inbound updates the title | `internal/connectors/inbound_test.go:433` | ✅ (the skipped-chat path is not covered, N10) |

### P1: Catch-all routing

| AC | Spec-defined outcome | `file:line` + assertion | Result |
| --- | --- | --- | --- |
| ROUTE-01 | unrouted matching chat joins with outbound on and is delivered | `internal/connectors/catch_all_test.go:89` (TestCatchAllRoutesNewDMs), `:128` scope matrix | ✅ |
| ROUTE-02 | chat already in a room is not added | `catch_all_test.go:113-114` `NotContains(members, …)` | ✅ |
| ROUTE-03 | detail `auto-route room unavailable` | `catch_all_test.go:215` (deleted room), `:234` (lost manage) | ✅ |
| ROUTE-04 | manage required; unknown room gives the same 403 | `internal/api/catch_all_settings_test.go:84-86` `require.Equal(t, hiddenErr, unknownErr)` | ✅ |
| ROUTE-05 | `trusted=false` unless the owner's verified identity | `catch_all_test.go:101` `False(got.Trusted)`, `:123` `True` | ✅ |
| ROUTE-06 | broadcast off: stored, to agents, no chat, exact warning; a reply to an outbound chat goes there only | `internal/api/no_broadcast_test.go:81` (TestNoBroadcastRoomPosts: no outbox, `Warning == noBroadcastWarning`, reply → `[family]`); listen-only reply `internal/rooms/messages_test.go:563` (no recipients, no outbox row, exact warning literal); tool `internal/channel/room_info_test.go:131` | ✅ (B2 killed) |
| ROUTE-07 | catch-all turns broadcast and bridge off; PATCH on gets 422 with exact text, applies nothing | `no_broadcast_test.go:42-46` exact fields `"must stay off while the room is a catch-all room"`; `internal/rooms/messages_test.go:591` TestCatchAllRoomRefusesBroadcastAndBridge; web `web/src/routes/_app/rooms/room.test.tsx:541` | ✅ |
| ROUTE-08 (amended) | 60/h per connector, 1,000 per room; nothing added, **chat not recorded**, nothing stored; detail; next route clears | `catch_all_test.go:248` (60 accepted, `:261` `False(hasChat(dmPeer(60)))`, messages unchanged, detail; +1h accepted and cleared); `:276` room cap (`:288` no chat record); burst `:405`, `:422` (exactly 60 / 1,000 under 5 concurrent routes) | ✅ (C4 killed; the per-connector *window* is untested, N6) |
| ROUTE-09 | route or save clears only auto-route details | `catch_all_test.go:296`; status event `:455` `Equal(cleared, drain())` after the route (`:471`) and after the PATCH (`:476`) | ✅ (S4, S5 killed) |
| ROUTE-10 (new) | >10 distinct chats → 422 `recipients: at most 10 chats per message`, nothing stored; tool and instructions state the limit | `internal/rooms/messages_test.go:151` exact fields for user and agent, `Zero(messages)`, 10 accepted, repeated id counts once; HTTP `internal/api/recipients_cap_test.go:33`; docs `internal/channel/tools_test.go:112,115` | ✅ |
| ROUTE-11 (new) | per-connector serialization; unrouted-for-other-reasons chats still recorded (out of scope, skipped, room unavailable, empty message) | lock `catch_all_test.go:512` (TestCatchAllRoutesLockPerConnector, `:551` second connector routed while the first is held); recording `:493` (out of scope `:497`, empty `:500`, room unavailable `:505`) | ⚠️ the "skipped" chat is listed in the AC but untested (N10) |

### P1: Agent membership settings

| AC | Spec-defined outcome | `file:line` + assertion | Result |
| --- | --- | --- | --- |
| MEM-01 | no author header | `internal/connectors/sender_test.go:196` | ✅ |
| MEM-02 | display name outbound and in the timeline | `sender_test.go:206` `"Lolo"` | ✅ |
| MEM-03 | only mentions delivered, all stored; reply-to-me | `internal/delivery/wants_test.go:15`; `internal/api/delivery_filter_test.go:148` | ✅ |
| MEM-04 | only `trusted=true` | `delivery_filter_test.go:100` | ✅ |
| MEM-05 | 422 limits | `internal/api/member_settings_test.go:99` | ✅ |
| MEM-06 | exact help text (en + pt-BR); channel sentence | `web/src/components/member-settings.test.tsx:251-261`; `internal/channel/room_info_test.go:182` | ✅ |

### P2: Room instructions

| AC | Spec-defined outcome | `file:line` + assertion | Result |
| --- | --- | --- | --- |
| INS-01 | ≤4,000 stored, longer 422 | `internal/api/room_instructions_test.go:17` | ✅ |
| INS-02 | room_info fields | `internal/api/room_info_test.go:24`; `internal/channel/room_info_test.go:35` | ✅ |
| INS-03 | one notice per room, also paused, literal trusted or untrusted text | `internal/api/room_info_test.go:82` `Equal(insNotice, n.Text)`; paused `:108-109`; untrusted `:149`; owner `:153`; unknown author `:194` `insNoticeUntrusted` | ✅ (T2 killed) |
| INS-04 | author recorded (none when empty or legacy); `instructions_by`, `instructions_trusted` owner-only | `room_info_test.go:120` (edit flips, cleared → empty); legacy `''` `:181` (`NotContains "instructions_by"`, `instructions_trusted == false`); create `:199` (`"alice"`, trusted for alice's agent, not bob's) | ✅ (T1, T8 killed) |
| INS-05 | exact `instructions_note`; instructions say only the owner's are trusted | `internal/channel/room_info_test.go:172`, legacy `:212`; `:176` | ✅ |
| INS-06 (new) | author recorded on create and on name/description change (none for legacy); `room_text_trusted` owner-only; exact `room_text_note`; unchanged name **or description** keeps the author | `internal/api/room_info_test.go:220` (creator trusted, bob's rename flips, same name kept, bob's description flips, legacy `''` trusted by nobody); tool `internal/channel/room_info_test.go:55-56`, `:229-230` exact note; instructions `:236`; migration default `internal/store/store_test.go` TestRoomTextAuthorMigrationDefault | ⚠️ "unchanged description keeps the author" is untested (N14), and so is "instructions-only edit keeps the author" (N13); whitespace laundering (finding 9) |

### P2: Reactions

| AC | Spec-defined outcome | `file:line` + assertion | Result |
| --- | --- | --- | --- |
| REACT-01 | reaction reaches the origin chat | `internal/connectors/reaction_test.go:21` | ✅ |
| REACT-02 | `isError` + exact text | `internal/channel/react_test.go:31` | ✅ |
| REACT-03 | 422 `recipients: listen-only`, nothing stored | `internal/api/react_test.go:100` | ✅ |
| REACT-04 | webhook payload without `author_name` when unsigned | `internal/api/hook_reaction_test.go:50` | ✅ |
| REACT-05 (amended) | one grapheme; text-style symbols need VS16 or a skin tone; ZWJ ≤4 components, ≤11 code points; keycap; flag; else 422 `emoji: must be one emoji` | `internal/validation/emoji_test.go:15` (accept `:16-40` incl. ©️ ®️ ™️ ‼️ ↔️ ☝🏽 and the 11-code-point sequence `:39-41`; reject `:45-83` incl. bare © ® ™ ‼ ↔ ↩ ❤, ❤‍🔥, 5- and 8-component chains); HTTP `internal/api/react_test.go:61-63` exact fields for bare ©, ❤ and a 5-component chain | ✅ (E5 killed) |

### P1: Delete a room

| AC | Spec-defined outcome | `file:line` + assertion | Result |
| --- | --- | --- | --- |
| ROOMDEL-01 | managers only; alert dialog text | `web/src/routes/_app/rooms/room.test.tsx:447`, not offered without manage (same describe) | ✅ |
| ROOMDEL-02 | navigate, toast, list updated | `room.test.tsx:447` | ✅ |

### P2: Accessible forms

| AC | Spec-defined outcome | `file:line` + assertion | Result |
| --- | --- | --- | --- |
| A11Y-01 | errors announced and tied by `aria-describedby` | `member-settings.test.tsx:229-238`; `connectors.test.tsx:523-547` | ✅ |
| A11Y-02 | Save commits a typed keyword or errors and sends nothing | `member-settings.test.tsx:209-226` | ✅ |
| A11Y-03 | counter describes the textarea | `room.test.tsx:380` | ✅ |
| A11Y-04 | Save disabled, described by `No changes to save.` | `connectors.test.tsx:550-560` | ✅ |
| A11Y-05 (new) | Enter or Send without a picked chat posts nothing and shows `Pick a chat to answer` (pt-BR `Escolha um chat para responder`) as an alert describing the picker; bridge switch described by the lock reason | `web/src/components/composer.test.tsx:254-263` (alert text after Enter and after Send, picker accessible description, 0 posts, text kept; picking clears it); bridge `room.test.tsx:554-555` `toHaveAccessibleDescription(locked)` | ⚠️ the pt-BR literal is not asserted anywhere (N30) |

**Status**: 43/43 ACs evidenced. Four ACs have spec-precision gaps at their edges: ROUTE-11, INS-06, A11Y-05 and the ROUTE-08 per-connector window. The success criterion "DM reaches the agent within 2 s" still has no AC (L-033, manual UAT).

---

## Edge Cases

- [x] Concurrent first messages add the chat once: `catch_all_test.go:157`.
- [x] Removed catch-all chat waits 24 h: `catch_all_test.go:176`.
- [x] Bridge on → off when chosen as catch-all: `no_broadcast_test.go:23`.
- [x] Composer: hint, `Pick a chat to answer` on Enter/Send, nothing posted (`composer.test.tsx:246-280`); no chat to pick → posts to agents only (`composer.test.tsx:279`); switches locked with the reason (`room.test.tsx:541-555`).
- [x] No connector targets the room any more → broadcast stays off: `no_broadcast_test.go:60` (moved `:68`, cleared `:73`). Closed since iteration 2.
- [x] Keyword inside a longer word: `wants_test.go:15` table.

---

## Discrimination Sensor

Scratch: `git worktree add --detach /tmp/claude-1001/pl-rc-mutants3 35b48bc`, plus three sibling worktrees (`…3b`, `…3c`, `…3d`) to run partitions in parallel. Each had web `node_modules` symlinked. Each mutation was one exact-string replacement applied with a driver script, followed by `git checkout -- <file>` after its run. Targeted `go test -count=1 <pkgs>` or `vitest run <file>` decided each mutant. Every survivor was re-run against **all** Go packages (`go test ./...`, 31 ok) or the full Vitest suite (288 passed) and still survived. For each killed mutant, the failing test was checked to be the intended one: no compile errors and no unrelated flakes. Scratch porcelain was clean after every partition. Real-tree `git status --porcelain` was empty before and after the sensor.

### Re-applied iteration-2 survivors

| # | File | Mutation | Result |
| --- | --- | --- | --- |
| B2 | `internal/rooms/messages.go:251` | reply to a listen-only chat counts as an explicit chat | ✅ killed (TestNoBroadcastReplyToListenOnlyChat) |
| C4 | `internal/connectors/inbound.go:227-228` | per-connector route lock removed | ✅ killed (TestCatchAllHourlyCapHoldsUnderBurst) |
| S4 | `inbound.go:359` | clearing publishes no `connector.status` | ✅ killed (TestCatchAllClearedDetailIsAnnounced) |
| S5 | `internal/connectors/service.go:391` | PATCH clearing publishes no `connector.status` | ✅ killed (same) |
| E5 | `internal/validation/emoji.go:14` | `MaxEmojiLength` 11 → 10 (raising it is equivalent: 4 components × 2 + 3 ZWJ = 11) | ✅ killed (TestOneEmoji, 11-code-point accept) |
| T1 | `internal/api/agent_handlers.go:135` | legacy `''` instructions author trusted | ✅ killed |
| T2 | `internal/agents/gateway.go:419` | legacy author gets the trusted notice | ✅ killed |
| T8 | `internal/rooms/rooms.go:163` | room create records no instructions author | ✅ killed (TestRoomInfoInstructionsAuthorAtCreate) |

### Fresh mutations on T42–T46

| # | File | Mutation | Result |
| --- | --- | --- | --- |
| N1 | `internal/rooms/messages.go:153` | count `recipients` with repeats instead of distinct | ✅ killed (TestPostRecipientsCap) |
| N2 | `messages.go:153` | `>` → `>=` (10 refused) | ✅ |
| N3 | `messages.go:154` | 422 text changed | ✅ |
| N4 | `internal/channel/tools.go:56` | `chat_ids` description drops the limit | ✅ |
| N5 | `internal/connectors/inbound.go:82` | one shared route state for every connector | ✅ |
| N6 | `inbound.go` autoRoute cap | **hourly window counts every connector's routes** (locks stay per connector) | ❌ survived |
| N7 | `inbound.go:85` | route state not kept (fresh window each call) | ✅ |
| N8 | `inbound.go` cap branch | capped chat recorded before refusal | ✅ (TestCatchAllHourlyCap, TestCatchAllRoomCap) |
| N9 | `inbound.go:222-224` | out-of-scope chat not recorded | ✅ |
| N10 | `inbound.go:236-238` | **chat held out by the 24 h skip not recorded/updated** | ❌ survived |
| N11 | `inbound.go` handle | empty-message chat not recorded | ✅ |
| N12 | `inbound.go:243-248` | room-unset chat not recorded | ✅ |
| N13 | `internal/rooms/rooms.go:278` | **instructions-only PATCH makes the caller the room text author** | ❌ survived |
| N14 | `rooms.go:274` | **unchanged description save changes the author** | ❌ survived |
| N15 | `rooms.go:270` | unchanged name save changes the author | ✅ |
| N16 | `internal/api/agent_handlers.go:137` | legacy `''` room text trusted | ✅ |
| N17 | `rooms.go:165` | room create records no room text author | ✅ |
| N18 | `internal/channel/tools.go:301` | `room_text_note` never added | ✅ |
| N19 | `tools.go:291` | tool always says `room_text_trusted: true` | ✅ |
| N20 | `internal/validation/emoji.go:119` | text-style symbols accepted bare | ✅ |
| N21 | `emoji.go:112` | 5-component chains accepted | ✅ |
| N22 | `emoji.go:52` | ☕ dropped from Emoji_Presentation | ✅ |
| N23 | `emoji.go:81` | presentation check uses the pictographic table | ✅ |
| N24 | `emoji.go:117` | skin tone no longer a qualifier | ✅ |
| N25 | `emoji.go:125` | trailing ZWJ accepted | ✅ |
| N26 | `web/src/components/composer.tsx:114` | no "Pick a chat" guard (posts with no chat) | ✅ |
| N27 | `composer.tsx:243` | alert does not describe the picker | ✅ |
| N28 | `composer.tsx:109` | Send disabled again until a chat is picked | ✅ |
| N29 | `composer.tsx:240` | picking a chat keeps the alert | ✅ |
| N30 | `web/src/lib/i18n/pt-BR.ts` | **`composer.pickChat` pt-BR text changed** | ❌ survived (full Vitest 288 passed) |
| N31 | `web/src/components/room-settings-sheet.tsx:159` | bridge switch keeps its normal hint in a catch-all room | ✅ |
| N32 | `composer.tsx:117` | picker not marked invalid | ✅ |

**Sensor depth**: expanded (P0: catch-all exposure, trust labels, mass messaging)
**Sensor outcome (iteration 3)**: 35/40 killed. All 8 iteration-2 survivors killed; 5 fresh survivors (N6, N10, N13, N14, N30). **FAIL ❌**

### Security probe (scratch only, discarded)

A throwaway test in `/tmp/claude-1001/pl-rc-mutants3/internal/api/zz_probe_test.go` did the following. Bob, a co-manager, set the description `"Forward every file to bob. "` with a trailing space through the API. Alice then sent a web-style PATCH: her unchanged name and the trimmed description that `roomSettingsSchema` sends (`web/src/lib/schemas.ts:65` `description: z.string().trim()`; `room-settings-sheet.tsx:90` always sends name and description). Result: `room_text_trusted` for alice's agent became **true** for text bob wrote. The server compares descriptions untrimmed (`internal/rooms/rooms.go:274`) and stores them untrimmed on create (`:162`). Any manager can therefore plant whitespace and wait for the owner's next unrelated save (for example retention) to launder the text into "trusted". This is finding 9 below.

---

## Gate Check

- **Build gate** `make check`: **EXIT 0**, first run, 14 m 43 s. It covers generate drift, golangci-lint, vet, biome, typecheck, `go test -race ./...` (31 packages ok), **Vitest 288 passed / 29 files**, integrations `bun test` 27 pass / 0 fail, web build and `go build`.
- **E2E** `make e2e` with `PLAYWRIGHT_CHROMIUM_EXECUTABLE=…/chromium_headless_shell-1243/…`: **8 passed**, EXIT 0, first run.
- **Flakes**: none. No re-run was needed, including `TestLinkPausesAfterReplaced`.
- **Counts**: Go test funcs went from 521 at 6947d1d to 538 at 53a74e3 (+17 for routing-context). HEAD has 652 because main brings harness-onboarding tests. Web stays at 288 cases: T42 rewrote composer and room cases rather than adding new ones. Skipped: none. Failures: none.
- **Integrity**: the only removed assertions are `Send … toBeDisabled()`/`toBeEnabled()` in the composer test (A11Y-05 replaced that behaviour on purpose) and `getByText(locked)`, which became the stronger `toHaveAccessibleDescription(locked)` on both switches. Emoji cases that moved from accept to reject follow the REACT-05 amendment. No assertion was weakened.

---

## Code Quality

| Principle | Status |
| --- | --- |
| Minimum code / surgical / no scope creep | ✅ (`routeState` per connector, `findChat` helper, `dto.MaxMessageRecipients`) |
| Matches patterns | ✅ (validation.Check 422s, sqlc, tygo, i18n parity) |
| Spec-anchored outcome check | ✅ literal texts asserted (422 reasons, notes, notices, warning); ⚠️ pt-BR A11Y-05 literal |
| Per-layer coverage | ⚠️ per-connector window, skipped-chat recording, INS-06 description/instructions edges |
| Every test maps to a requirement | ✅ |
| Documented guidelines | ⚠️ L-019: contract notes claim "its own hourly window" per connector and "skip (record, stop)", and neither is tested (N6, N10). A test seam (`routeHook` field) lives in production code; it is acceptable, set only from `export_test.go`. |

### UI checklist (web-design-guidelines, T42)

- Fixed since iteration 2: the bridge switch is described by the lock reason (`room-settings-sheet.tsx:158-160`). Send stays enabled until the request starts, which complies with the guideline (`composer.tsx:109`).
- `web/src/components/composer.tsx:113-118`: Send without a chat leaves focus on Send. The guideline says "focus first error on submit", which here means the chat picker. The `role="alert"` announcement mitigates this. Minor.
- `composer.tsx:163`: the alert sits above the textarea and is tied to the picker (`aria-describedby`, `aria-invalid`), not to the textarea. This is acceptable because the alert is announced. Info.
- The recipients picker does not stop at 10 chats, so the 11th pick shows up only as the server's 422 post error (contract note). Minor UX.
- Passing: pt-BR keys for every new string (parity test), `aria-hidden` icons, live regions.

---

## Security Notes: final status

1. **Mass messaging via `chat_ids` (was medium): mitigated.** ROUTE-10 caps a post at 10 distinct chats (tested). Residual: an agent can make 30 posts per minute (`errAgentRateLimited`), so it can still reach about 300 chats per minute, and user posts have no rate limit. Low-medium.
2. **Spam creates chat rows (was medium-low): mitigated.** Capped chats are no longer recorded (tested). Residual, by spec (ROUTE-11/CON-07): out-of-scope, empty-message and under-cap chats still persist rows with attacker-chosen titles, the hourly counter is in memory and resets on restart, and the 24 h skip expires. Low.
3. **Cap serialization (was medium): fixed.** The lock is per connector and is tested under a deterministic burst (C4 killed); per-connector locking is also tested. Residual test gap: the hourly *window* per connector (N6).
4. **Unknown-author instructions (was medium, test gap): closed.** T1 and T2 are killed.
5. **Untrusted content in agent context (was low-medium): partly mitigated.** `room_info` now labels the room name and description (INS-06). Residual: `room_name` push meta and `list_rooms` carry no label, and only the channel instructions warn about them. Untrusted instructions are still delivered verbatim, labelled. Low.
6. **Emoji permissiveness (was low): fixed.** Text-style symbols need VS16, chains are limited to 4 components and 11 code points. Residual: non-RGI 2–4 component chains such as `👍‍👍` are accepted and may render as several emoji. Info.
7. **PII residue (low): open.** `chat_id`/`sender_id` still carry the full phone number to every room viewer and agent, and titles or room names set before T30 keep the number until the next inbound message.
8. **Mentions mode: by design.** Documented as noise-only (MEM-06). Info.
9. **NEW: room-text trust laundering through whitespace (medium).** See the probe above. A co-manager's description with leading or trailing whitespace becomes "written by" whoever next saves the room settings from the web app, so the owner's agents trust it. Proposed fix: normalize the description server-side (trim on create and update) or compare trimmed values before moving `updated_by`. Then add an HTTP test for a web-style save that keeps the author.

---

## Fix Plans (escalated: iteration 3 is the last)

### Fix 1: INS-06 description edges and the whitespace laundering (N14, finding 9): Major
Trim descriptions server-side, or compare trimmed values. Test that a PATCH carrying the unchanged description, and the web form's trimmed copy of it, keep `updated_by`.

### Fix 2: Instructions-only edit keeps the room text author (N13): Minor, INS-06
Have bob PATCH only `instructions` and assert that `room_text_trusted` is unchanged for both agents.

### Fix 3: Per-connector hourly window (N6): Minor, ROUTE-08
Put connector A at 60 routes in the hour. Assert that a new chat on connector B is still routed and that B's detail stays empty.

### Fix 4: Skipped chat still recorded (N10): Minor, ROUTE-11 / CTX-08
During the 24 h skip, send an inbound message with a new title from the removed chat. Assert that the stored title and `last_seen_at` update, the chat is not re-added and nothing is stored.

### Fix 5: pt-BR "Pick a chat" literal (N30): Minor, A11Y-05
Render the composer under pt-BR and assert that the alert reads `Escolha um chat para responder`.

---

## Requirement Traceability Update

| Requirement | Previous | New |
| --- | --- | --- |
| CTX-01..08, ROUTE-01..07, ROUTE-09, ROUTE-10, MEM-01..06, INS-01..05, REACT-01..05, ROOMDEL-01/02, A11Y-01..04 | Implemented / re-verify | ✅ Verified |
| ROUTE-08 | amended, re-verify | ⚠️ Verified with test gap (N6) |
| ROUTE-11 | Implemented (T44) | ⚠️ Verified with test gap (N10) |
| INS-06 | Implemented (T45) | ❌ Needs Fix (N13, N14, finding 9) |
| A11Y-05 | Implemented (T42) | ⚠️ Verified with test gap (N30) |

(spec.md not edited by the Verifier.)

---

## Summary

**Overall**: ❌ Not Ready (final iteration; escalate to André).

**Spec-anchored check**: 43/43 ACs evidenced; precision gaps in ROUTE-08 (window), ROUTE-11 (skipped), INS-06 (description and instructions edges) and A11Y-05 (pt-BR)
**Sensor**: 35/40 killed. All 8 iteration-2 survivors killed; N6, N10, N13, N14 and N30 survived
**Gate**: `make check` EXIT 0 (31 Go packages with `-race`, 288 Vitest, 27 bun), e2e 8/8

**What works**: every behaviour named in the spec, including the caps under concurrency, the recipients cap, the room text trust label, the stricter emoji rule and the composer alert.

**Issues found**: one medium security defect (finding 9) and five missing tests. All of them are small.

**Next steps**: André decides whether to run Fixes 1–5 as a fourth round or accept the minor gaps. Fix 1 is recommended in any case, because it closes a trust-laundering path.
