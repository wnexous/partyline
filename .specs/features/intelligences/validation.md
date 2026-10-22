# Intelligences Validation

## Validation (iteration 2): intelligences - FAIL ❌

**Result**: FAIL - every iteration-1 gap that had a fix task is closed, and all 8 non-equivalent iteration-1 survivors are now killed. M30 is equivalent; its double-guard variant is killed. All gates pass: `make check-race`, and the formerly flaky takeover test passed 30 of 30 under `-race`. 83 of 84 ACs match their spec outcome. Two new mutants survive, both test-only gaps with no product defect. N18: the amended SES-06 promises that a silent connection goes offline within 35 s, and no test drives the ping-failure path. W8: the AI badge added to the route-chat dialog in fed9454 has no test. This is iteration 2 of 3, and fixing both takes two tests.

**Date**: 2026-10-22
**Spec**: `.specs/features/intelligences/spec.md` (84 requirements; amended by T58, T66 and T68; AD-013, AD-014)
**Diff range**: `cf5440f^..fed9454`: the Phase 10 fix round T56-T69 (`cf5440f..771de75`, merged as `b76dcf5`) plus `fed9454` (AI badge in the command palette and the route-chat dialog). 53 files, +1 893 / −136.
**Verifier**: independent sub-agent (author ≠ verifier), worktree `partyline-verify-intel` on `verify/intelligences-2` at `fed9454`.

### Task Completion

| Task | Status | Notes |
| --- | --- | --- |
| T1-T55 | ✅ Done | As in iteration 1. |
| T54 | ✅ Done | `uat.md`: real Claude Code UAT PASS with Finding 1. Its follow-up (T69) is a text change. Only a new real session can show its effect, and none was run here (hard rule). |
| T56-T69 | ✅ Done | Every `Done when` is checked in `tasks.md`. The commits are `cf5440f..771de75`. |

### Iteration-1 gaps re-checked

| Gap | Fix | Evidence (asserts the spec outcome) | Closed? |
| --- | --- | --- | --- |
| 1 MRG-01 merge skips undelivered/unread | `internal/conversations/merge.go:244-266,315-336` (up-to-date check before append; `cursorHolder`) | `internal/api/merge_test.go:426-430` survivor cursor `0`, pending `{"to atlas 1","to atlas 2","History merged from @old","to old"}`, alice read cursor `0`, `Unread == 3`; `:470-478` up-to-date parties move (4/5, 5/4, `Unread` 0); `:501-504` each AI's cursor credited to itself (6 vs 5) | ✅ (M19, N01-N03 killed) |
| 2 check-race flake / duplicate after takeover | `internal/agents/gateway.go:474` `sent[st.ID] = st.Cursor` | `internal/agents/gateway_live_test.go:131-134` late push of an acked message → `taker.ExpectNone`, next message arrives; `go test -race -count=30 -run TestTakenOverChannelNotifiesOnceAndDeliversNothingMore` 30/30 | ✅ (N04, N05, N17 killed) |
| 3 PEOP-04 vs DM-08 | AD-013 (André), spec PEOP-04/DM-08 amended | `internal/api/people_test.go:182-243`: alice and dave (`use` on Atlas) get `LevelView` on the platform DM and 200 on the avatar (`:214-220`); dave's profile `{Dana, platform, CanEdit false}`, `Person == nil`, patch 403 (`:223-227`); bob (connector) sees the person but gets 404 on the DM (`:229-231`); carol and the admin get 404 on the DM, profile and avatar (`:234-240`) | ✅ (N14, N15 killed) |
| 4 M17/M18 participant order | tests only | `internal/connectors/notify_test.go:307,312` AI sorts first → `{dmB, "Atlas: your report is ready"}`; `internal/api/direct_messages_test.go:366,375` AI sorts first → `ErrCounterpartDisabled` | ✅ (M17, M18, N10 killed) |
| 5 M10b no process id | tests only | `internal/agents/presence_test.go:85-108` second attach without id `ErrBusy`, `Busy` true; `internal/api/agent_live_test.go:113-119` 409 `identity_busy`, first stays live | ✅ (M10b + Attach-only variant killed) |
| 6 ACC-05 trust source | AD-014 (André), `internal/connectors/selfmsg.go:52-55` | `internal/connectors/selfmsg_test.go:89-102` owner operates → `Trusted` true, `AuthorUserID` = alice; AI operates → `Trusted` false, `AuthorUserID` empty, author = AI; back to owner → trusted | ✅ (N06, N07 killed) |
| 7 PROF-07 height / person avatar | tests only | `internal/avatars/avatars_test.go:169-171` 10×5000, 4097×1 and 1×4097 are `ErrInvalid`; `:216-222` 1×4096 accepted; `internal/api/people_test.go:291-314` person avatar `image/png` 256×256 (`requireGreenSquare`) for owner, connector grantee, room viewer and chat viewer; carol and the admin get 404 | ✅ (M23 killed) |
| 8 PROF-09 badges | header, new-group, files (+ DTO `author`) | `web/src/routes/_app/rooms/conversation.test.tsx:211` both participants with their badges ("AI", "WhatsApp"); `web/src/components/new-group-dialog.test.tsx:294,306`; `web/src/components/room-files.test.tsx:159`; `internal/api/files_handlers_test.go:34-64`; `internal/api/people_test.go:273-288` | ✅ (W3-W7, N13 killed). The route-chat dialog badge from `fed9454` has no test (W8 survived, see below). |
| 9 CLI-04 / M15 / M16 | `internal/cli/setup_harness.go:259-262`, `internal/cli/menu.go:107-124`; CLI-03/04 amended | `internal/cli/setup_menu_test.go:66-71` one line per harness (`already registered`, `- … not registered`); `:92-104` registered-not-installed starts checked, Enter keeps it; `:135-139` failure → exit 1, `✗ Claude Code:` on stderr, Gemini still registered; `internal/cli/menu_test.go:78-84` `a` touches installed items only | ✅ (M15, M16, N11, N12 killed) |
| 10 M30 senderless platform DM | test only | `internal/connectors/platform_dm_test.go:278-300` no DM, no person, no message, nothing delivered, no failure logged | ✅ The outcome is pinned. M30 itself is **equivalent**, see the sensor. |
| 11 W1 clamp | test only | `web/src/lib/api/queries.test.ts:70-74` 1 unread, read to seq 5 of 10 → stays 1 | ✅ (W1 killed) |
| 12 notify outbox visibility | `internal/api/convert/convert.go:38-67`, `internal/live/hub.go:378-383`, `internal/rooms/messages.go:495-531` | `internal/connectors/notify_test.go:343` AI's own answer has no notify row; `:347-356` bob sees row + live status; `:359-373` owner and `use` grantee: timeline `Outbox` empty, live frame empty, no `outbox.status`; `:379-399` AI acting for its owner's DM sees none, alice does | ✅ (N08, N09, N10 killed) |
| 13 spec text | T68 | SES-06, TREE-02, DM-02, DM-08, PEOP-01 amended; each now matches the code and the tests (table below) | ✅ except SES-06's new 35-s clause, which has no test (N18) |
| 14 other minor findings | not tasked | Still open: late-member unread baseline, operator-validation race (500 not 422), `settle` blocking the inbound loop up to 10 s, people squatting AI handles, tools-half of a refused pi/OpenCode session | ⏭️ Carried over (Minor, not AC failures) |
| UAT Finding 1 | T69 | `internal/channel/instructions_test.go:75-77` literal `terminalSentence` once, after the tool paragraph; `:99` in the omp text; tools-only text unchanged | ✅ for the text (N16 killed). Behaviour still needs a real session. |

### Gate Check

| Gate | Command | Result |
| --- | --- | --- |
| Build | `make check` | ✅ exit 0 in 127 s: 0 lint issues, no generated-code drift. Go: every package `ok` (api 338, channel 112, cli 137, connectors 128, agents 32, avatars 8 tests …). Web: 48 files / 778 tests. Integrations: 70 pass / 0 fail. Build OK. |
| Race | `make check-race` | ✅ exit 0 in 493 s (web 778/778). |
| Race, isolated | `go test -race -count=30 -run 'TestTakenOverChannelNotifiesOnceAndDeliversNothingMore$' ./internal/channel/` | ✅ `ok` in 84 s: 30/30, run with the sensor loading the machine. Iteration 1: 1 in 8 failed. |
| E2E | `make e2e` (Chromium headless shell 1248) | ✅ 20 passed (10 × desktop-1280 + mobile-360) |

- **Test count**: Go +23 top-level tests since iteration 1 (api 329→338, channel 111→112, cli 133→137, connectors 123→128, …). Web went from 770 to 778. No test was deleted. Two assertions were changed on purpose to follow the decisions: `selfmsg_test.go` "trusted" became `False` (AD-014), and `notify_test.go:407` became `Empty(msg.Outbox)` (T67). Both are spec changes, not weakening.
- **Skipped tests**: none. `git status --porcelain` was empty before and after every gate.

### Spec-Anchored Acceptance Criteria (recount over all 84)

The 74 ACs that iteration 1 marked ✅ were re-checked against the amended spec. Their outcome text is unchanged. Their evidence still holds, but line numbers moved in the test files that Phase 10 touched (current lines below). The rows below are those that changed status, changed outcome text, or gained evidence.

| AC | Spec-defined outcome (current text) | Evidence | Result |
| --- | --- | --- | --- |
| SES-04 | 409 `identity_busy`, busy text on every tool | + `internal/agents/presence_test.go:85`, `internal/api/agent_live_test.go:113-119` | ✅ |
| SES-05 | one notice, then nothing | `internal/channel/session_lost_test.go:151,159`; `internal/agents/gateway_live_test.go:131-134` | ✅ stable (30/30 under `-race`) |
| SES-06 | offline ≤5 s on close or drop; a silent connection is dropped by the ping (25 s + 10 s write timeout), offline ≤35 s | 5 s: `internal/api/agent_live_test.go:245`. 35 s: **no test**. The interval is injectable (`gateway.go:73`), and ignoring a failed ping (N18) survives every package. | ❌ Partial |
| TREE-02 | path label before the name; home node `~`, folder column `~/…` | `web/src/routes/_app/contacts.test.tsx:181-184` `getByText("c/d ›")`, `/c\/d ›\s*E/`, `getByText("~")`, `getByText("~/c/d/e")`; `internal/intelligences/tree_test.go:31,47` | ✅ |
| DM-02 | 422 `dm_members_fixed` for a viewer; 404 for a caller who cannot view | `internal/api/direct_messages_test.go:296-298` 422, `:304-306` 404; `:375` AI-first order | ✅ |
| DM-03 | live session receives the DM; reply in the DM (+ instruction text) | iteration-1 evidence + `internal/channel/instructions_test.go:75` | ✅ |
| DM-08 | two participants; AI owner and named `use` grantees view (also DMs with people, AD-013); `*` grant does not; grants 422 | `internal/api/people_test.go:214-216` `{dm, LevelView}`; `internal/access/access_test.go:577` public AI's DM not visible to everyone; `internal/api/room_membership_access_test.go:85,100` | ✅ |
| PEOP-01 | person only for routed messages; unrouted chat records no person | `internal/connectors/people_test.go:89` `Empty(e.people(t), "an unrouted chat records no person")`; `internal/connectors/platform_dm_test.go:290` | ✅ |
| PEOP-04 | connector users see + edit + account; conversation viewers and AI `use` grantees read only; nobody else, admins included | `internal/api/people_test.go:141,147,153-158,223-241` | ✅ |
| PROF-07 | 256×256 PNG; served to who can see; size bounds | `internal/avatars/avatars_test.go:169-171,216`; `internal/api/people_test.go:291-314` | ✅ |
| PROF-09 | badge + @handle next to every name | gap-8 evidence above + `web/src/components/command-menu.test.tsx:35` | ✅ (W8: route-chat dialog untested) |
| ACC-05 | authored by operator; trusted as owner only when the owner operates | `internal/connectors/selfmsg_test.go:89-102` | ✅ |
| CLI-03 | installed or registered pre-selected; `a` = installed only | `internal/cli/menu_test.go:51,78-84`; `internal/cli/setup_menu_test.go:93` `"> [x] Claude Code  not installed · registered"` | ✅ |
| CLI-04 | one result line per harness; exit non-zero on failure; nothing else changed | `internal/cli/setup_menu_test.go:66-71,94-97,135-139` | ✅ |
| MRG-01 | groups, DMs merged, cursors (now: only for up-to-date parties), grants, messages, delete, audit | `internal/api/merge_test.go:426-430,470-478,501-504` + iteration-1 lines | ✅ |
| NOTI-02 | "<AI name>: <text>", links, outbox marked; delivery visible to the DM's user only | `internal/connectors/notify_test.go:312,411`, `:343-373` | ✅ |
| NOTI-03 | retries; DM readable | `internal/connectors/notify_test.go:565-582` (moved from `:470,474`) | ✅ |
| DM-05 | only via the account the sender operates | `internal/connectors/platform_dm_test.go:378-384` (moved from `:322,331`) | ✅ |

**Status**: 83 ✅ / 0 ⚠️ / 1 ❌ (SES-06: the 35-s clause added by T68 has no evidence).

### Edge Cases (changed since iteration 1)

- [x] Disabled counterpart is refused in both pair orders: `internal/api/direct_messages_test.go:347,375`.
- [x] Merge with a counterpart AI: `internal/api/merge_test.go:483-505`.
- [x] Senderless platform DM: `internal/connectors/platform_dm_test.go:278`.
- [~] Width < 40 and hostname truncation: unchanged from iteration 1 (Minor notes).

### Discrimination Sensor

The sensor ran in the detached scratch worktree `/home/dev/orca/projects/partyline-verify-intel-mut` at `fed9454`. One mutant was applied at a time, followed by `go test -count=1 -p 4` on the covering packages (filtered with `-run`; the survivors were run against whole packages) or `bun x vitest run` for W*. After each mutant the file was restored with `git checkout` inside the scratch, and the scratch was checked to be clean before the next one. At the end the scratch was removed with `git worktree remove --force` and pruned. `git status --porcelain` of the verify worktree was empty before and after.

**Iteration-1 survivors, re-run**

| # | File:line | Mutation | Killed? |
| --- | --- | --- | --- |
| M10b | `internal/agents/presence.go:93,124` | empty live id counts as the holder's process | ✅ `TestPresenceTreatsTwoConnectionsWithoutAProcessIDAsTwoProcesses`, `TestTwoConnectionsWithoutAProcessIDAreTwoProcesses` |
| M10b′ | `internal/agents/presence.go:93` | Attach-only variant | ✅ `TestPresenceTreatsTwoConnectionsWithoutAProcessIDAsTwoProcesses` |
| M15 | `internal/cli/setup_harness.go:144` | `Selected: s.installed` | ✅ `TestSetupMenuKeepsARegisteredHarnessThatIsNotInstalled` |
| M16 | `internal/cli/setup_harness.go:162` | failed apply exits 0 | ✅ `TestSetupMenuExitsNonZeroWhenAHarnessFails` |
| M17 | `internal/rooms/notify.go:23-26` | notify target = `dm_a` always | ✅ `TestAIDMIsCopiedToTheAppOfAUserThatSortsAfterTheAI`, `TestTheCopysDeliveryIsVisibleOnlyToTheUserOfTheDM` |
| M18 | `internal/rooms/messages.go:116-119` | counterpart check uses the owner's id for AI posts | ✅ `TestPostByAnAIThatSortsBeforeTheDisabledUserIsRefused` |
| M19 | `internal/conversations/merge.go:341-346` | every appended AI cursor credited to the survivor | ✅ `TestMergeCreditsEachAICursorToItself` |
| M23 | `internal/avatars/avatars.go:52` | height bound removed | ✅ `TestProcessRejectsInvalidInputs/too_tall`, `/one_pixel_too_tall` |
| M30 | `internal/connectors/autoroute.go:210` | senderless guard removed | ➖ **Equivalent.** `EnsurePlatformDM` refuses an empty author first (`internal/conversations/platform.go:58`, `ErrDMSelf` → not routed), so no outcome changes, the log line included. Removing both guards (M30b) is ✅ killed by `TestPlatformDMWithoutASenderIsNotRouted`, and removing only the conversations guard (M30c) also survives. The two guards are redundant and the behaviour is pinned. The test comment ("refused by the database and logged") names the wrong guard; that is cosmetic. |
| W1 | `web/src/lib/api/queries.ts:540` | clamp removed | ✅ `queries.test.ts` "never raises the count … (W1)" |

**New mutations on the fixed code**

| # | File:line | Mutation | Killed? |
| --- | --- | --- | --- |
| N01 | `internal/conversations/merge.go:356` | `agentUpToDate` always true | ✅ `TestMergeKeepsWhatTheSurvivorHasNotReceivedOrRead` |
| N02 | `internal/conversations/merge.go:363` | `userUpToDate` always true | ✅ `TestMergeKeepsWhatTheSurvivorHasNotReceivedOrRead` |
| N03 | `internal/conversations/merge.go:363` | `userUpToDate` always false | ✅ `TestMergeCarriesDirectMessages`, `TestMergeMovesTheCursorsOfPartiesThatWereUpToDate` |
| N04 | `internal/agents/gateway.go:474` | `sent` seeding removed (pre-fix) | ✅ `TestTakeoverDropsALatePushOfAnAcknowledgedMessage` |
| N05 | `internal/agents/gateway.go:474` | seeded with `cursor + 1` (off by one) | ✅ 5 tests, among them `TestTakenOverChannelNotifiesOnceAndDeliversNothingMore`, `TestTakeoverMakesTheNewSessionLive` |
| N06 | `internal/connectors/selfmsg.go:53` | AD-014 condition inverted | ✅ `TestOwnAccountMessageIsTrustedOnlyWhenTheOwnerOperatesTheAccount` (+1) |
| N07 | `internal/connectors/selfmsg.go:54` | AI-operated account keeps the owner's user (pre-AD-014) | ✅ same 2 tests |
| N08 | `internal/api/convert/convert.go:38-43` | `Viewer` returns the user id for agents too | ✅ `TestTheCopysDeliveryIsNotShownToTheAIEvenForItsOwnersDM` |
| N09 | `internal/live/hub.go:380` | `outbox.status` of a notify copy sent to every viewer | ✅ `TestTheCopysDeliveryIsVisibleOnlyToTheUserOfTheDM` |
| N10 | `internal/rooms/messages.go:514-517` | `notifiedUser` = `dm_a` always | ✅ `TestAIDMIsCopiedToTheAppOfAUserThatSortsAfterTheAI`, `TestTheCopysDeliveryIsVisibleOnlyToTheUserOfTheDM` |
| N11 | `internal/cli/setup_harness.go:260` | no "already registered" line | ✅ `TestSetupMenuAppliesTheChecklist`, `TestSetupMenuKeepsARegisteredHarnessThatIsNotInstalled` |
| N12 | `internal/cli/menu.go:120-124` | `a` toggles items that are not installed | ✅ `TestMenuKeys`, `TestMenuAllLeavesItemsThatAreNotInstalledAsTheyWere`, `TestSetupMenuAllChecksOnlyInstalledHarnesses` |
| N13 | `internal/api/convert/convert.go:140` | file author loses its platform | ✅ `TestRoomFilesNameAPersonWithItsPlatform` |
| N14 | `internal/access/access.go:173` | named `use` grant no longer gives view on the AI's DMs (AD-013) | ✅ `TestUseGranteeOfAnAIReadsItsPlatformDMsAndPeople`, `TestDMRoutesAuthMatrix` |
| N15 | `internal/intelligences/service.go:400-401` | person not readable through a viewable conversation | ✅ 6 tests incl. `TestUseGranteeOfAnAIReadsItsPlatformDMsAndPeople`, `TestPersonAvatarIsServedToWhoCanSeeThePerson` |
| N16 | `internal/channel/instructions.go:43` | `terminalNote` dropped from the omp text | ✅ `TestOmpInstructions` |
| N17 | `internal/agents/gateway.go:442` | drop check `<=` → `<` | ✅ `TestTakeoverDropsALatePushOfAnAcknowledgedMessage`, `TestAgentLivePushSkipsBacklogOverlap` |
| N18 | `internal/agents/gateway.go:436-439` | failed ping ignored: a silent connection is never dropped (SES-06, 35 s) | ❌ **Survived** (`./internal/agents/ ./internal/api/ ./internal/channel/` whole packages) |
| W3 | `web/src/components/conversation-header.tsx:46` | both-participants header disabled | ✅ `conversation.test.tsx` (2 tests) |
| W4 | `web/src/components/room-files.tsx:285` | file author always a plain name | ✅ `room-files.test.tsx` (2 tests) |
| W5 | `web/src/components/command-menu.tsx:124` | palette badge removed (`fed9454`) | ✅ `command-menu.test.tsx` "marks an AI with a human-looking name as an AI (PROF-09)" |
| W6 | `web/src/components/new-group-dialog.tsx:185` | picked member without badge | ✅ `new-group-dialog.test.tsx:294` |
| W7 | `web/src/components/new-group-dialog.tsx:154` | refused member without badge | ✅ `new-group-dialog.test.tsx:306` (+1) |
| W8 | `web/src/components/route-chat-dialog.tsx:134` | route-chat badge removed (`fed9454`) | ❌ **Survived** (all of `src/components` and `src/routes`; the dialog has no test file) |

**Sensor depth**: P0-full. There were 36 runs: 10 re-runs of iteration-1 mutants (the 9 survivors plus the Attach-only variant), 24 new mutants and 2 equivalence probes (M30b, M30c). Four further attempts did not compile on the first try (`declared and not used`); they were rewritten and run as M18, N01-N03 and N09 above.
**Sensor outcome**: 32/36 killed. Of the 4 survivors, M30 and M30c are equivalent (redundant guards, pinned together), and N18 and W8 are genuine. FAIL ❌: 2 genuine survivors → fix tasks.

### Code Quality

| Principle | Status |
| --- | --- |
| Minimum code / surgical | ✅ The fixes are small and local: merge pre-check, one seeding line, a trust condition, a viewer filter, CLI result lines. |
| No scope creep | ✅ The palette and route-chat badges (`fed9454`) implement PROF-09's "every name". |
| Matches patterns | ✅ sqlc query added for files (`internal/store/queries/files.sql`), DTO + tygo regenerated, no drift. |
| Spec-anchored outcome check | ⚠️ SES-06's 35-s clause has no test (T68 called it untestable, but the ping interval is injectable). |
| Per-layer coverage | ⚠️ The route-chat dialog has no component test (W8). |
| Every test maps to a requirement | ✅ The new tests cite AC ids and lessons (L-042, L-044, L-045, L-046, L-050). |
| Documented guidelines (`AGENTS.md`, `contract-notes.md`, `.specs/LESSONS.md`) | ✅ L-018 note: M30's test cannot isolate the autoroute guard because `platform.go:58` rejects first. The outcome is still pinned, so L-018 is not penalized. |

### Gaps and Fix Plans (ranked)

1. **Minor - SES-06 / N18: the 35-s bound for a silent connection has no test.** `internal/agents/gateway.go:434-440`. Fix task: in `internal/agents/gateway_live_test.go` (or `internal/api/agent_live_test.go`), build the app with a short `PingInterval` (`gateway.go:73`), dial an agent connection and stop reading it, so pongs are never answered. Assert that the AI goes offline (presence event / `Presence.Online` false) within ping + write timeout. If 10 s is too slow for the suite, make `writeTimeout` a `Deps` field as `PingInterval` is. The test must fail with the ping error ignored (N18). Also correct T68's commit claim that no test can reach the bound.
2. **Minor - PROF-09 / W8: the route-chat dialog's AI badge is untested.** `web/src/components/route-chat-dialog.tsx:134` (added in `fed9454`). Fix task: add `web/src/components/route-chat-dialog.test.tsx`, render the dialog with an AI whose display name looks human, and assert `getByTestId("kind-badge")` has text "AI" next to it. The test must fail without the badge.
3. **Cosmetic - M30 test comment.** `internal/connectors/platform_dm_test.go:274-277` says the database would refuse an empty participant. In fact `EnsurePlatformDM` (`internal/conversations/platform.go:58`) refuses it first. Optionally reword the comment; no behaviour change.
4. **Carried over (Minor, not AC failures, iteration-1 gap 14)**: unread baseline for late group members; operator validation racing deletion (500); `settle` holding the inbound loop up to 10 s; people squatting AI handles; tools-half of a refused pi/OpenCode session; attachment blobs of a deleted AI's DMs; hostname truncation/`unknown`.
5. **Open UAT follow-up**: repeat UAT step 4 with fresh Claude Code sessions to confirm that the T69 text makes models answer greetings with `reply` (`uat.md` Finding 1 stays open until then).

### Requirement Traceability Update

The verdict is FAIL, so `spec.md` stays at `Implementing`. After the two fix tasks pass re-verification (iteration 3), set all 84 to `Verified`.

| Requirement | Previous Status | Status this iteration would give |
| --- | --- | --- |
| 83 ACs (all but SES-06) | Implementing | ✅ Verified at the next PASS |
| SES-06 | Implementing | ❌ Needs Fix (gap 1) |
| PROF-09 | Implementing | ✅ outcome verified; W8 test gap (gap 2) |

### Summary

**Overall**: ❌ Not Ready by the gate (two test-only gaps). The product behaviour checked here has no known defect.

**Spec-anchored check**: 83/84 matched; 0 spec-precision gaps; 1 partial (SES-06, 35-s clause untested).
**Sensor**: 32/36 killed; 2 equivalent (M30, M30c), 2 genuine survivors (N18, W8).
**Gate**: `make check` ✅, `make check-race` ✅, takeover test `-race -count=30` ✅ 30/30, `make e2e` ✅ 20/20.
**Completion gate**: `validate_state.py --root . intelligences` exits 1 with "validation.md verdict is FAIL - route the ranked gaps to fix tasks, then re-verify". This is expected for a FAIL.

**What works**: merge now keeps undelivered and unread messages and credits each cursor to its own party. Takeover no longer replays acknowledged messages, and the race gate is stable. AD-013 and AD-014 are implemented exactly as decided and pinned by tests. A notify copy's delivery is visible only to the DM's user, in every renderer: timeline, post answer, activity, live frame and status event. Setup prints one result line per harness and exits 1 on failure, and `a` selects installed harnesses only. Kind badges appear in the header, new-group dialog, files list and palette. The channel instructions now say that terminal text never reaches the sender.

**Production-release blockers**: none in product behaviour. The release gate is FAIL only because of gaps 1 and 2 (each one test). Fixing them and re-verifying (iteration 3 of 3) clears it. UAT Finding 1 should be repeated with a real session before announcing DM support for Claude Code.

### Lessons distilled (iteration 2)

- **Added (candidates)**: L-051 from N18 (SES-06) and L-052 from W8 (PROF-09). They are listed in `.specs/LESSONS.md`.
- **Not penalized**: L-018. M30 is an equivalent mutant whose behaviour is pinned by the double-guard probe M30b, not a missed outcome.
- **Not added**: none for M30. A redundant guard is not a test failure.

---

# Iteration 1 (2026-10-22, diff `be30f10^..d52458b`)

**Date**: 2026-10-22
**Spec**: `.specs/features/intelligences/spec.md` (84 requirements)
**Diff range**: `be30f10^..d52458b` (96 commits, 390 files, +51 673 / −10 367)
**Verifier**: independent sub-agent (author ≠ verifier; the code was written by Sonnet 5.5, DeepSeek and Claude Opus workers)

## Iteration 1 report: intelligences - FAIL ❌

**Iteration 1 verdict** - FAIL - 74/84 ACs matched their spec outcome; 4 are partial (one is a confirmed delivery-loss defect in merge, MRG-01) and 6 have spec-precision gaps. 9 of 32 sensor mutants survived. `make check-race` is flaky: run 1 failed on one load-sensitive takeover test (it fails 1 in 8 runs alone under `-race`), run 2 passed. `make check` and `make e2e` (20/20) pass. T54 (real Claude Code UAT) is still pending.

The core of the release is sound. The v0.3 → 0015/0016 migration preserves every row of every table and rolls back as one transaction. I found no cross-owner leak in DMs, profiles, avatars, folder identity or adoption. The failures below are bounded and each has a one-line fix.

---

## Task Completion

| Task | Status | Notes |
| --- | --- | --- |
| T1–T53 | ✅ Done | Every `Done when` is checked in `tasks.md`. |
| T54 | ⏳ Pending | The real Claude Code UAT is running in another worktree (`wnexous/pl-t54-sonnet`). It will add `uat.md`. Not verified here. |
| T55 | ✅ Done | Merge from the profile page. |

---

## Gate Check

| Gate | Command | Result |
| --- | --- | --- |
| Build | `make check` | ✅ exit 0 in 2m03s: 0 lint issues, vet clean, no generated-code drift. Go: 23 sharded packages + 14 others all `ok` (e.g. api 329, channel 111, cli 133, connectors 123, store 37 tests). Web: 48 files / 770 tests. Integrations: 70 tests. Build OK. |
| Race | `make check-race` | ⚠️ flaky. Run 1: exit 2 (9m36s). One failure: `TestTakenOverChannelNotifiesOnceAndDeliversNothingMore` (`internal/channel/session_lost_test.go:158`, expected the "after" message, got the earlier "before" message). Run alone: `go test -race -count=8 -run …` fails 1 of 8; without `-race`, 10 of 10 pass. **Re-run** of the full `make check-race` on a quieter machine: ✅ exit 0 (7m18s). The gate is flaky, not deterministically red. |
| E2E | `make e2e` (Chromium headless shell 1248) | ✅ 20 passed (10 tests × desktop-1280 + mobile-360) |

- **Test count**: Go top-level tests went from 802 to 1 154 (+352). Web `it`/`test` calls went from 318 to 679, e2e tests from 7 to 10 (×2 projects). No tests were deleted. The suite grew, and the tests replaced along with v0.3 code (projects, session keys) were replaced with intelligences equivalents.
- **Skipped tests**: none.
- **Root cause of the race failure** (read from the code, reproduced in isolation): `Gateway.run` only drops a live push whose seq is at or below `sent[room]` (`internal/agents/gateway.go:442`). `sent` is filled only by messages the backlog actually sent (`gateway.go:528`). If the old session already acked a message, the new session's backlog sends nothing for that room, so `sent[room]` stays 0. A late dispatcher push of the old message then reaches the taker a second time. The product result is a duplicate delivery after a takeover (at-least-once, Minor); the test then fails under load. Fix: seed `sent[room]` with the agent's cursor in `backlog`.

---

## Spec-Anchored Acceptance Criteria

Evidence is `file:line` plus the assertion. Every line cited was checked with `grep -n`/`sed -n`. ✅ = the asserted value is the spec outcome; ⚠️ = spec-precision gap; ❌ = partial or failed.

### AID - folder identity

| AC | Spec-defined outcome | Evidence | Result |
| --- | --- | --- | --- |
| AID-01 | (O,M,F) resolved; created with default name/handle | `internal/agents/folder_test.go:105-106` `Equal("Acme", i.DisplayName)`, `Equal("acme", i.Handle)` | ✅ |
| AID-02 | same id; `app` = new harness | `internal/agents/folder_test.go:112-114` `Equal(first.ID, again.ID)`; `internal/api/agent_folder_test.go:85,88` | ✅ |
| AID-03 | `name`, `name-2`, same display name | `internal/agents/folder_test.go:250,253` `Equal([]string{"notes","notes-2","notes-3"}, got)` | ✅ |
| AID-04 | tools-only call acts as (O,M,F) | `internal/api/agent_folder_test.go:57` author `[agent, ai.ID, …]` | ✅ |
| AID-05 | 422 `folder_required` | `internal/api/agent_folder_test.go:405` `RequireError(422, ErrorCodeFolderRequired)` on every route, `:410` WebSocket | ✅ |
| AID-06 | rename visible in timelines, members, mentions, signatures; 1–64 / 1–40 | `internal/api/message_authors_test.go:83,92`; `internal/api/mentions_test.go:42-45`; `internal/connectors/agent_rename_test.go:35`; `internal/intelligences/service_test.go:294-301` | ✅ |
| AID-07 | canonical folder + hostname on every call | `internal/channel/identity_test.go:51` `Equal(c.want, got)` (symlink, trailing slash, dot segments) | ✅ |

### SES - one live session

| AC | Spec-defined outcome | Evidence | Result |
| --- | --- | --- | --- |
| SES-01 | exact prompt text | `internal/cli/launcher_test.go:390` `Equal("Atlas is open in another session (Claude Code, since 09:30). Take over? [y/N] ", r.stderr)` | ✅ |
| SES-02 | takeover; old closed `taken_over` | `internal/cli/launcher_test.go:392` `PARTYLINE_TAKEOVER=1`; `internal/api/agent_live_test.go:135` `requireClosedWith(t, first, 4002, "taken_over")` | ✅ |
| SES-03 | no/enter → exit 1, harness not started | `internal/cli/launcher_test.go:416-417` `Equal(1, r.code)`, `False(r.execed)` | ✅ |
| SES-04 | 409 `identity_busy`; every tool answers the busy text | `internal/api/agent_live_test.go:88` `RequireError(409, ErrorCodeIdentityBusy)`; `internal/channel/session_lost_test.go:89` `Equal(wantBusy, text)` | ✅ (mutant M10b survived: the empty live-id case is untested) |
| SES-05 | one exact notice, then no deliveries | `internal/channel/session_lost_test.go:151` `Equal(wantTakenOver, n.Content)`, `:159` `expectNoNotification` | ✅ (flaky under `-race`, see Gate) |
| SES-06 | offline ≤5 s; next session needs no prompt | `internal/api/agent_live_test.go:224` 5 s deadline, `:241` | ⚠️ Only proven for a clean close. A half-open TCP connection takes ping 25 s + write 10 s (`internal/agents/gateway.go:36-37`). |
| SES-07 | no TTY → no prompt, no takeover unless flag/env | `internal/cli/launcher_test.go:441-444`, `:490` | ✅ |
| SES-08 | backlog, 50 cap + notice | `internal/api/agent_live_test.go:266` `Equal("5 older messages skipped; use the history tool", n.Text)` | ✅ |

### TREE - machine › folder tree

| AC | Spec-defined outcome | Evidence | Result |
| --- | --- | --- | --- |
| TREE-01 | machine groups; nested child | `internal/api/contacts_test.go:125` `shape(devbox.Nodes,0)`; `web/src/routes/_app/contacts.test.tsx:171`; e2e `web/e2e/smoke.spec.ts:476` | ✅ |
| TREE-02 | `c/d › e` collapsed; `~` for home | `internal/intelligences/tree_test.go:31` `{Path:"c/d",Name:"e"}`, `:47` `Name:"~"`; `contacts.test.tsx:181,183` | ⚠️ The server computes `TreeNode.Name`, but the web (`web/src/components/machine-group.tsx:159-166`) and CLI (`internal/cli/agents.go:103-106`) render `Path › DisplayName`. `~` appears only in the folder column. |
| TREE-03 | row fields; online machines first, then name | `internal/intelligences/tree_test.go:128`; `internal/api/contacts_test.go:244`; golden `internal/cli/agents_test.go:74` | ✅ (M13 killed) |
| TREE-04 | label replaces hostname | `internal/api/contacts_test.go:257-296` (64 ok, 65 → 422, '' resets, other owner 404) | ✅ |
| TREE-05 | filter keeps ancestors | `web/src/lib/contacts.test.ts:154,176-179` | ✅ |
| TREE-06 | Unlinked with merge/delete | `internal/api/contacts_test.go:141,235`; `contacts.test.tsx:580-592,718-737` | ✅ |
| TREE-07 | `partyline agents` tree | `internal/cli/agents_test.go:74-82` exact golden, `:90` empty text | ✅ |

### CONT / DM - contacts and private conversations

| AC | Spec-defined outcome | Evidence | Result |
| --- | --- | --- | --- |
| CONT-01 | own tree, shared AIs, users, people per connector | `internal/api/contacts_test.go:144,149,155`; `contacts.test.tsx:136` | ✅ |
| DM-01 | exactly one DM per pair | `internal/api/direct_messages_test.go:83`, `:152` `Equal(1, created)` (12 concurrent) | ✅ |
| DM-02 | 422 `dm_members_fixed` | `internal/api/direct_messages_test.go:296-298` `RequireError(422, ErrorCodeDMMembersFixed)` | ⚠️ A stranger gets 404 (`:304-306`), not 422 as "anyone" says. 404 is the right privacy choice; amend the spec. Disabled-counterpart edge case: mutant M18 survived. |
| DM-03 | `conversation="dm"`; reply in DM | `internal/api/dm_agent_test.go:62,69` | ✅ |
| DM-04 | `to:` creates DM if the owner sees the target, else `contact_not_found` | `internal/api/agent_conversations_test.go:70`, `:121` `RequireError(404, ErrorCodeContactNotFound)` | ✅ (M05 killed) |
| DM-05 | person only via the account the sender operates, else `no_route_to_person` | `internal/connectors/platform_dm_test.go:322`, `:331` `RequireError(422, ErrorCodeNoRouteToPerson)` | ✅ |
| DM-06 | together, newest first, unread per user | `internal/api/conversations_list_test.go:96,175,193` | ✅ (M14 killed) |
| DM-07 | mark read to newest | `web/src/routes/_app/rooms/conversation.test.tsx:322` `toEqual([{ seq: 5 }])`; `conversations_list_test.go:249` | ✅ |
| DM-08 | two participants post; AI owner and `use` grantees view; grants 422 | `internal/api/room_membership_access_test.go:85` `statuses(403,403,201,404,404)`, `:100` `statuses(422,422,422,404,404)`; `internal/access/access_test.go` matrix | ⚠️ A `*` (public) use grant does not give view (`internal/access/access.go:172`). That is safer than the spec text; amend it. DM-08 also conflicts with PEOP-04 (Gap 3). |

### GRP / AGRP - groups

| AC | Spec-defined outcome | Evidence | Result |
| --- | --- | --- | --- |
| GRP-01 | rooms are groups, routing unchanged | `internal/store/intelligences_migration_test.go:209`; `internal/api/upgrade_test.go:68` | ✅ |
| GRP-02 | people per bridged chat with platform | `internal/api/people_test.go:328-329` | ✅ |
| GRP-03 | authored by the person | `internal/connectors/people_test.go:111,117` | ✅ |
| GRP-04 | delivery modes, keywords, signatures, broadcast | `internal/api/member_settings_test.go:77`; `internal/api/no_broadcast_test.go:81`; `dm_agent_test.go:108` | ✅ |
| AGRP-01 | owned by the AI's owner, owner's add-member rules | `internal/api/agent_conversations_test.go:230,265` | ✅ (M29 killed) |
| AGRP-02 | nothing created; refused list | `internal/api/agent_conversations_test.go:264,269` | ✅ |

### PEOP - people

| AC | Spec-defined outcome | Evidence | Result |
| --- | --- | --- | --- |
| PEOP-01 | person per (connector, external id) | `internal/connectors/people_test.go:96,111,140,188-212` | ⚠️ Created only for routed messages (CON-07). This is written in contract-notes T26 but not in the spec. |
| PEOP-02 | linked account authored by the user | `internal/connectors/people_test.go:267`; `internal/api/people_test.go:304` | ✅ |
| PEOP-03 | name, apps, visible conversations | `internal/api/people_test.go:212,218,221` | ✅ |
| PEOP-04 | only connector owner/use see people; nobody else | `internal/api/people_test.go:153-158` (404 for carol/admin) | ⚠️ Built wider than the spec. Anyone who can view a conversation where the person was seen can read the profile (`internal/intelligences/service.go:396-401`). The design's security review allows this, the spec does not. |
| PEOP-05 | platform rename unless user-renamed | `internal/connectors/people_test.go:340,348,381` | ✅ (M24 killed) |
| PEOP-06 | trusted unchanged | `internal/connectors/people_test.go:303,305`; `internal/delivery/rules.go:96` | ✅ |

### ACC - accounts and operators

| AC | Spec-defined outcome | Evidence | Result |
| --- | --- | --- | --- |
| ACC-01 | owner or owner's AI; audit | `internal/api/connector_operator_test.go:99` `Len(audits, 1)`, `:136` 403, `:141-159` 422, `:170` fallback | ✅ |
| ACC-02 | platform DM → DM person↔operator | `internal/connectors/platform_dm_test.go:95,105,112`; catch-all `:197-198` | ✅ (M21 killed) |
| ACC-03 | operator AI unsigned | `internal/connectors/operator_signature_test.go:67` `Empty(…, "its own account: no author line")` | ✅ (M20 killed) |
| ACC-04 | AI signs per setting when a user operates | `internal/connectors/operator_signature_test.go:83,88,106` | ✅ |
| ACC-05 | own-account message authored by the operator | `internal/connectors/selfmsg_test.go:51-54,69`; echo `:109,164,174` | ✅ (M22 killed). The new trust source is not recorded in AD-006 (Gap 6). |
| ACC-06 | new group chat → group with operator | `internal/connectors/auto_groups_test.go:42,51,95,122` | ✅ |
| ACC-07 | apps on operator's page | `internal/api/connector_operator_test.go:218` `{claude, "Fake: @atlas_bot"}` | ✅ |

### PROF - profiles

| AC | Spec-defined outcome | Evidence | Result |
| --- | --- | --- | --- |
| PROF-01 | one intelligence per user/AI/person | `internal/intelligences/service_test.go:148,211`; `internal/connectors/people_test.go:113` | ✅ |
| PROF-02 | unique case-insensitive, 1–40 `a-z0-9-`, letter first | `internal/handles/handles_test.go:53,67`; `internal/store/intelligences_schema_test.go:59` | ✅ |
| PROF-03 | own edit shows everywhere | `internal/api/intelligences_handlers_test.go:52-91`; `message_authors_test.go:83`; `web/src/routes/_app/settings.test.tsx:125`; e2e `smoke.spec.ts:503` | ✅ |
| PROF-04 | `update_profile` on itself only | `internal/api/avatar_handlers_test.go:266,299`; `internal/intelligences/service_test.go:399,405` | ✅ |
| PROF-05 | owner edits AI; grantee 403 | `internal/api/intelligences_handlers_test.go:276` `statuses(200,403,404,404,404)` | ✅ (M03 killed) |
| PROF-06 | 422 `avatar_invalid`, old kept | `internal/api/avatar_handlers_test.go:159-190` | ✅ |
| PROF-07 | 256×256 PNG; served to who can see | `internal/api/avatar_handlers_test.go:115` `Equal(image.Rect(0,0,256,256), img.Bounds())`, `:141` 404 | ❌ Partial. The height bound is not tested (M23 survived: a 10×5000 image passes `DecodeConfig`). No positive test serves a person's avatar. |
| PROF-08 | initials on colour from id | `web/src/components/intelligence/intelligence.test.tsx:87-89` | ✅ |
| PROF-09 | badge + @handle next to every name | `web/src/components/intelligence/intelligence.test.tsx:160` `toHaveTextContent(/^AI$/)`, `:197` | ❌ Partial. Bare names without a badge in `web/src/components/conversation-header.tsx:15-19,58-60` (an owner viewing an AI's DM), `web/src/components/new-group-dialog.tsx:180` and `web/src/components/room-files.tsx:260`. |
| PROF-10 | 409 `handle_taken` | `internal/api/intelligences_handlers_test.go:242-247` | ✅ |
| PROF-11 | user rename beats platform | `internal/api/people_test.go:258,262` | ✅ |

### LAY - messenger layout

| AC | Spec-defined outcome | Evidence | Result |
| --- | --- | --- | --- |
| LAY-01 | avatar, name, 80-char preview, sender prefix, time, unread, newest first | `internal/api/conversations_list_test.go:358,375`; `web/src/components/conversation-list.test.tsx:143` `"Bob Lima: rolled back"` | ✅ |
| LAY-02 | live message to top; unread +1 unless open | `web/src/lib/live/handlers.test.ts:410,436,439,453` | ✅ (W2 killed; W1 on the read clamp survived) |
| LAY-03 | search names + members; new conversation/group | `conversation-list.test.tsx:314-357`; `web/src/components/new-conversation-dialog.test.tsx:81-151` | ✅ (member search covers the first 20 members only, `internal/conversations/list.go:59`) |
| LAY-04 | user owner + members, add-member rules | `internal/api/groups_test.go:28,83`; e2e `smoke.spec.ts:440` | ✅ |
| LAY-05 | <768 px sidebar first, full-screen conversation with back | `web/src/routes/messenger-shell.test.tsx:89,103`; e2e `smoke.spec.ts:541` (mobile-360) | ✅ |
| LAY-06 | header menu reaches the pages | `web/src/routes/messenger-shell.test.tsx:133-163` | ✅ |

### CLI - login and setup

| AC | Spec-defined outcome | Evidence | Result |
| --- | --- | --- | --- |
| CLI-01 | device flow; exact "Logged in as …" | `internal/cli/login_test.go:42` `Contains(…"Logged in as @alice on "+env.URL+" (machine devbox-7)\n")` | ✅ |
| CLI-02 | login opens menu when nothing registered | `internal/cli/setup_menu_test.go:163-165,181-182` | ✅ |
| CLI-03 | checklist keys, state, pre-selection | `internal/cli/menu_test.go:43-56,72-73,129-130`; width 30 `:183-189` | ✅ |
| CLI-04 | register/remove selected; one line per harness; nothing else | `internal/cli/setup_menu_test.go:49,57,61,64` | ❌ Partial. Lines are printed only for changed harnesses (`internal/cli/setup_harness.go:233-256`). Mutants M15 (registered-but-not-installed starts unchecked) and M16 (apply failure exits 0) survived. |
| CLI-05 | no TTY → flags; exit 2 + hint | `internal/cli/setup_menu_test.go:97-98,108,118,132` | ✅ |
| CLI-06 | "Run partyline login first", exit 1 | `internal/cli/setup_menu_test.go:143-144` | ✅ |
| CLI-07 | `setup --browser` | `internal/cli/setup_test.go:361-362` | ✅ |

### MIG - migration

| AC | Spec-defined outcome | Evidence | Result |
| --- | --- | --- | --- |
| MIG-01 | groups, no DMs, every message/member/grant/bridge/cursor kept | `internal/store/intelligences_migration_test.go:166` `Equal(before[table], e.rowsOf(table, cols))` for every table, discovered from `sqlite_master`; `:187` `pragma_foreign_key_check` empty; `:209-210` | ✅ All 23 pre-0015 tables are seeded and compared. Project grants and members of agentless projects are dropped by design (`0015_intelligences.sql:127-128`). |
| MIG-02 | adopt the owner's unlinked identity once | `internal/agents/folder_test.go:351,355,396,420,522-525` | ✅ (M26 killed: another owner's identity is never adopted) |
| MIG-03 | project member → most recently connected agent; projects removed | `internal/store/projects_conversion_test.go:77-92` | ✅ (M07 killed) |
| MIG-04 | chat senders → persons, alt ids folded | `internal/store/intelligences_backfill_test.go:140,169`; `intelligences_migration_test.go:236,252` | ✅ (M08 killed) |
| MIG-05 | one transaction; unchanged on failure | `internal/store/intelligences_migration_test.go:287` (hook fails), `:453` (0016 fails → 0015 rolled back); `internal/store/migrate_test.go:139` | ✅ (M09 killed) |

### MRG / NOTI

| AC | Spec-defined outcome | Evidence | Result |
| --- | --- | --- | --- |
| MRG-01 | groups (B wins), DMs merged, cursors max, grants, messages, delete A, audit | `internal/api/merge_test.go:211,218,249,275,316,345` | ❌ **Defect, reproduced.** When A's DM is appended to B's DM with the same counterpart, A's renumbered cursor is MAXed into B's (`internal/conversations/merge.go:288-301`). B's undelivered messages are then skipped for good. Repro: B has 2 unreceived messages, A has 1 received; after the merge B's cursor is 4 (expected 0). The counterpart's read cursor jumps the same way (`merge.go:303-312`). M19 survived. |
| MRG-02 | 409 `identity_online` | `internal/api/merge_test.go:423` `RequireError(409, ErrorCodeIdentityOnline)` | ✅ |
| NOTI-01 | choose one own linked account or none | `internal/connectors/notify_test.go:147,254` | ✅ |
| NOTI-02 | "<AI name>: <text>", links, outbox marked | `internal/connectors/notify_test.go:304` `Equal("Atlas: the build is green", sent.Text)`, `:358,417` | ✅ (M17 survived: every fixture has the user as `dm_a`) |
| NOTI-03 | retries; DM readable | `internal/connectors/notify_test.go:470,474` | ✅ |

**Status**: ❌ 74 ✅ / 6 ⚠️ (SES-06, TREE-02, DM-02, DM-08, PEOP-01, PEOP-04) / 4 ❌ (PROF-07, PROF-09, CLI-04, MRG-01).

---

## Edge Cases

- [x] Simultaneous takeover → exactly one live: `internal/api/agent_live_test.go:196-210`, `internal/agents/presence_test.go:163`
- [x] Folder >1024 bytes / NUL → 422 `folder_invalid`: `internal/agents/folder_test.go:303-304,321,330`; `internal/api/agent_folder_test.go:273,275,291,296` (M12 killed)
- [x] Same folder on two machines → two identities: `internal/agents/folder_test.go:230`. Caveat: hostnames are truncated to 64 runes, and an unreadable hostname becomes `"unknown"` (`internal/channel/identity.go:14`), so distinct machines can merge.
- [x] AI deleted → DMs deleted, memberships removed: `internal/api/direct_messages_test.go:386,402`. The DMs' attachment blobs are not swept (`internal/agents/registry.go:331-341`, Minor).
- [~] Disabled counterpart → readable, post 403: `internal/api/direct_messages_test.go:337,339,347`. Only proven when the user is `dm_a` (M18).
- [x] Operator AI deleted → DM with owner: `internal/connectors/platform_dm_test.go:245-249`
- [x] Unknown menu key ignored: `internal/cli/menu_test.go:53-56`
- [~] Width < 40: `internal/cli/menu_test.go:183-189` (width 30 only). Widths under 20 are clamped to 20 and wrap (`internal/cli/menu.go:159-161`).

---

## Discrimination Sensor

Every mutant ran in the detached scratch worktree `/home/dev/orca/projects/partyline-verify-intel-mut` (`git worktree add --detach … d52458b`), one at a time, followed by `go test -count=1` on the covering packages, or `bun x vitest run` for W*. The scratch was removed with `git worktree remove --force`. `git status --porcelain` of the verify worktree was empty before and after.

| # | File:line | Mutation | Killed? |
| --- | --- | --- | --- |
| M01 | `internal/access/access.go:149` | AI owner/grantee gets `post` instead of `view` on the AI's DM | ✅ `TestDMRoomLevelMatrix` |
| M02 | `internal/access/access.go:169` | any user "owns" the AI participant (cross-owner DM read) | ✅ `TestDMRoomLevelMatrix`, `TestDMOfAPublicAIIsNotVisibleToEveryone` |
| M03 | `internal/intelligences/service.go:389` | `use` grantee may edit the AI | ✅ `TestCanSeeAndCanEdit`, `TestGetProfile` |
| M04 | `internal/intelligences/service.go:401` | every user sees every person (PEOP-04 leak) | ✅ `TestPersonProfileRoutesAuthMatrix`, `TestOpenDMWithAPersonHasNoRoute` |
| M05 | `internal/conversations/dm.go:100` | DM target visibility check removed (DM-04) | ✅ `TestAIDMTargetsAreThoseItsOwnerSees` |
| M06 | `internal/conversations/dm.go:78` | AI new-conversation rate limit removed | ✅ `TestOnlyNewDMsOfAnAICountAgainstTheLimit` |
| M07 | `internal/store/migrations/0015_intelligences.sql:111` | MIG-03 picks the least recently connected agent | ✅ `TestUpgradeV03KeepsEveryRow`, `TestUpgradeV03Identities` |
| M08 | `internal/store/intelligences_backfill.go:333` | linked chat senders not attributed to the user (MIG-04/PEOP-02) | ✅ `TestBackfillMessageAuthors` |
| M09 | `internal/store/store.go:190` | hook error ignored (MIG-05) | ✅ `TestMigrateFailingHookRollsBackEverything`, `TestUpgradeV03RollsBackOnHookFailure` |
| M10b | `internal/agents/presence.go:93,124` | a connection with no live id counts as the holder's process (silent replace) | ❌ **Survived** |
| M11 | `internal/agents/presence.go:94` | busy check removed (SES-04) | ✅ `TestPresenceRefusesASecondProcess` |
| M12 | `internal/agents/folder.go:91` | folder length bound off by one | ✅ `TestResolveFolderValidation` |
| M13 | `internal/intelligences/tree.go:202` | offline machines sort first (TREE-03) | ✅ `TestBuildMachinesOrder` |
| M14 | `internal/store/db/conversations.sql.go:20` | unread ignores the upgrade baseline (DM-06) | ✅ `TestUnreadCountsPerUser` |
| M15 | `internal/cli/setup_harness.go:144` | registered-but-not-installed harness starts unchecked (Enter removes it) | ❌ **Survived** |
| M16 | `internal/cli/setup_harness.go:162` | menu apply failure exits 0 | ❌ **Survived** |
| M17 | `internal/rooms/notify.go:23-26` | notify target = `dm_a` always | ❌ **Survived** |
| M18 | `internal/rooms/messages.go:111-114` | counterpart check uses the owner's id for AI posts | ❌ **Survived** |
| M19 | `internal/conversations/merge.go:293-296` | every appended agent cursor credited to the survivor | ❌ **Survived** |
| M20 | `internal/connectors/sender.go:93` | every author unsigned (ACC-03/04) | ✅ `TestAIThroughSomeoneElsesAccountSignsPerItsSetting` |
| M21 | `internal/connectors/autoroute.go:41` | catch-all connectors use ACC-06 routing (Spec issue 2) | ✅ `TestCatchAllRoutesNewDMs`, `TestCatchAllScopes` |
| M22 | `internal/connectors/selfmsg.go:73` | Partyline's own echo not dropped (ACC-05) | ✅ `TestEchoOfAPartylineSendIsDropped`, `TestEchoBeforeTheSendReturnsIsDropped` |
| M23 | `internal/avatars/avatars.go:52` | height bound removed (decompression bound) | ❌ **Survived** |
| M24 | `internal/intelligences/service.go:535` | any person edit locks the name (PROF-11) | ✅ `TestInboundUserRenameSurvivesPlatformRename` |
| M25 | `internal/agents/gateway.go:308` | takeover header inverted | ✅ `TestASecondSessionIsRefusedWhileTheFirstIsLive` |
| M26 | `internal/store/db/agents.sql.go:276` | adoption ignores the owner (cross-owner MIG-02) | ✅ `TestResolveFolderIgnoresAStaleAdopt` |
| M27 | `internal/store/db/agents.sql.go:169` | folder resolution ignores the owner (cross-owner AID) | ✅ `TestResolveFolderIdentityIsOwnerMachineAndFolder` |
| M28 | `internal/access/access.go:131` | group user members lose `post` (AD-011) | ✅ `TestGroupUserMemberPosts` |
| M29 | `internal/conversations/groups.go:133` | owner's add-member rules ignored (AGRP-01) | ✅ `TestAIGroupWithARefusedMemberCreatesNothing` |
| M30 | `internal/connectors/autoroute.go:210` | senderless platform DM is routed (contract-notes T30 "not routed") | ❌ **Survived** |
| W1 | `web/src/lib/api/queries.ts:540` | `room.read` clamp removed (`unread = after`) | ❌ **Survived** |
| W2 | `web/src/lib/live/handlers.ts:174` | own live messages count as unread | ✅ `handlers.test.ts` |

The Attach-only variant of M10b (`presence.go:93` alone) also survived. It is unreachable through the gateway, because `Presence.Busy` runs first (`internal/agents/gateway.go:189`).

**Sensor depth**: P0-full (critical feature): 32 behaviour-level mutants across migration, authorization, live session, folder identity, operators/signatures, conversations, CLI and web.
**Iteration 1 sensor outcome** - 23/32 killed - FAIL ❌ (9 survivors → fix tasks).

---

## Interactive UAT Results

T54 (real Claude Code session in a folder; resume; takeover prompt; web DM; `send_message to:`; `update_profile`; `create_group`; `partyline agents`; login/setup) is being run by another worker and will be recorded in `uat.md`. Not performed here.

---

## Code Quality

| Principle | Status |
| --- | --- |
| Minimum code | ✅ No speculative abstractions found in the reviewed paths. |
| Surgical changes | ✅ v0.3 project/session-key code removed in T16/T50, with tests replaced. |
| No scope creep | ⚠️ PEOP-04 (conversation-shared people) and DM-02 (404 for strangers) differ from the spec text without a spec amendment. |
| Matches patterns | ✅ sqlc + raw-SQL hooks (AD-012), DTO/tygo contract (AD-003), one authorization package. |
| Spec-anchored outcome check | ⚠️ 4 partial, 6 spec-precision gaps (above). |
| Per-layer coverage (domain 1:1; routes happy+edge+error) | ⚠️ The participant-order branches (`dm_a`/`dm_b`) are untested (M17, M18, M19). Routes have auth matrices (`internal/api/authmatrix_test.go` and per-route matrices). |
| Every test maps to a requirement | ✅ Test names and comments cite AC ids throughout. |
| Documented guidelines followed: `AGENTS.md`, `contract-notes.md`, `.specs/LESSONS.md` (L-017/018/019/029) | ⚠️ L-019 recurred: contract-notes T30 "a sender without any id … not routed" has no test (M30). |

---

## Gaps and Fix Plans (ranked)

1. **Major - MRG-01 merge drops undelivered messages.** `internal/conversations/merge.go:288-313`. The fix task: when appending a DM, advance a party's cursor in the target only if that party's existing target cursor already reached the target's pre-merge last seq; otherwise keep its cursor. Apply the same rule to read cursors. Add a test where B has an unreceived message in its DM with X and still receives it after the merge, and one where the counterpart is an AI with a cursor (kills M19).
2. **Major (gate) - `make check-race` intermittently fails on `TestTakenOverChannelNotifiesOnceAndDeliversNothingMore`.** Root cause: `internal/agents/gateway.go:442` with `:528`. Fix: seed `sent[room]` with the agent's cursor in `backlog`, so live pushes at or below the acked cursor are dropped. That also removes the duplicate delivery after takeover.
3. **Major (decision) - PEOP-04 vs DM-08.** `internal/access/access.go:138-176` gives an AI's `use` grantees `view` on the AI's platform DMs with external people. Through `internal/intelligences/people.go:73` they then see those people's profiles and avatars without any connector access. The spec's "nobody else" (PEOP-04) and "use grantees view" (DM-08) contradict each other here. Fix: André decides. Either require `CanUseConnector` for DMs that have a chat member, or amend PEOP-04.
4. **Minor - surviving mutants M17, M18 (participant order).** Add NOTI-02 and disabled-counterpart tests where the AI is `dm_a` (create the AI before the user). Files: `internal/connectors/notify_test.go`, `internal/api/direct_messages_test.go`.
5. **Minor - M10b.** Add a presence/gateway test: two live connections without `X-Partyline-Live` → the second gets 409 `identity_busy` (`internal/agents/presence_test.go`, `internal/api/agent_live_test.go`).
6. **Minor (decision record) - ACC-05 trust.** Own-account platform messages are stored with `author_user_id` = connector owner (`internal/connectors/selfmsg.go:22,50`), so the owner's AIs trust them. AD-006 does not list this source, and catch-all connectors now post these messages where CON-12 used to drop them. Fix: record an AD-006 amendment in `.specs/STATE.md`.
7. **Minor - PROF-07 / M23.** Add a 10×5000 avatar case (`internal/avatars/avatars_test.go`) and a positive person-avatar serving test (`internal/api/people_test.go`).
8. **Minor - PROF-09 badges.** Render `IntelligenceName` in `web/src/components/conversation-header.tsx:15-19,58-60` (both participants when there is no counterpart), `web/src/components/new-group-dialog.tsx:180` and `web/src/components/room-files.tsx:260`.
9. **Minor - CLI-04 / M15 / M16.** Test a registered-but-not-installed harness and a failing apply in the menu path (`internal/cli/setup_menu_test.go`). Amend the spec to "one line per changed harness", or print unchanged lines. Also, `a` selects harnesses that are not installed (`internal/cli/menu.go:107-117`), which then fail to apply. Limit `a` to installed harnesses.
10. **Minor - M30 / L-019.** Add a test that a senderless platform DM is not routed (`internal/connectors/platform_dm_test.go`).
11. **Minor - W1.** Add a `markConversationRead` test with `after > unread` (`web/src/lib/api/queries.test.ts` or `handlers.test.ts`).
12. **Minor - notify outbox visibility.** `purpose=notify` outbox items, with their chat id and errors, are rendered to every DM viewer (`internal/api/convert/convert.go:113`, `internal/live/hub.go:370-379`). Show them to the DM's user only.
13. **Minor - spec text.** Amend SES-06 (bound for unclean disconnects), TREE-02 (what renders `~`), DM-02 (404 for callers who cannot view), DM-08 (`*` grants excluded) and PEOP-01 (routed messages only).
14. **Minor - other findings.** Unread for a user added to a group later counts all history since the upgrade baseline (`internal/store/queries/conversations.sql:57-64`). Operator validation races deletion (`internal/connectors/service.go:374,401`, 500 instead of 422). `settle` blocks the adapter's inbound loop up to 10 s (`internal/connectors/selfmsg.go:56-78`). People can squat AI handles (`internal/connectors/people.go`). For pi/OpenCode, the tools half of a session refused `identity_busy` still acts as the AI (`internal/channel/run.go:105`; allowed by the "Tools-only processes" assumption, but worth a note in `docs/agents.md`).

---

## Requirement Traceability Update

| Requirement | Previous Status | New Status |
| --- | --- | --- |
| 74 ACs marked ✅ above | Implementing | ✅ Verified |
| SES-06, TREE-02, DM-02, DM-08, PEOP-01, PEOP-04 | Implementing | ⚠️ Verified with spec-precision gap (spec amendment) |
| MRG-01 | Implementing | ❌ Needs Fix (Gap 1) |
| PROF-07, PROF-09, CLI-04 | Implementing | ❌ Needs Fix (Gaps 7, 8, 9) |
| AID-01/02, SES-01..03/05, DM-03/04, PROF-04, AGRP-01, TREE-07, CLI-01/04 | - | also pending T54 UAT |

---

## Summary

**Overall**: ❌ Not Ready. The core is ready; the fix list is small.

**Spec-anchored check**: 74/84 matched; 6 spec-precision gaps; 4 partial (1 confirmed defect).
**Sensor**: 23/32 mutations killed.
**Gate**: `make check` ✅, `make e2e` ✅ 20/20, `make check-race` ⚠️ flaky: run 1 failed on one test, run 2 passed; the test fails 1 in 8 runs alone under `-race`.

**What works**:
- The migration preserves every row of all 23 v0.3 tables and is atomic across 0015 + 0016.
- DM privacy holds (participants post, owner and grantees view, admins none, grants refused).
- Folder identity and adoption are owner-scoped.
- Busy/takeover protocol, operators and signatures, catch-all compatibility, people per connector, profiles and avatars, the messenger web shell and the CLI login/setup/agents all behave as specified.

**Production-release blockers**: none in the migration or the authorization core. Fix before release: Gap 1 (merge loses deliveries), Gap 2 (race-gate flake / duplicate after takeover), Gap 3 (privacy decision), and complete T54.

**Next steps**: route Gaps 1–11 to fix tasks (one phase), get André's decision on Gap 3 and Gap 6, then re-verify (iteration 1 of 3).

---

## Lessons distilled

Lessons were recorded with `scripts/lessons.py`; `.specs/LESSONS.md` is re-rendered from them.

- **Added (candidates)**:
  - L-043: merge cursor (MRG-01)
  - L-044: test both orderings of a pair (M17–M19)
  - L-045: CLI failure paths (M15, M16)
  - L-046: clamps (W1)
  - L-047: visibility conflicts between ACs (PEOP-04/DM-08)
  - L-048: amend the spec when the design changes an outcome
  - L-049: timing on unclean disconnects (SES-06)
  - L-050: drop pushes at or below the acked cursor (check-race failure)
- **Promoted**: L-042 (every condition of a match predicate gets a test where only it excludes) recurred in a second feature (M10b, M23) and is now confirmed.
- **Penalized**: L-019 ("every behaviour written into contract notes needs a test") was loaded for this feature. It recurred: contract-notes T30's "a sender without any id … not routed" is untested (M30). This is its second penalty, so the script quarantined it.
