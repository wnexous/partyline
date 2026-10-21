# Agent Identity Verification Report

**Date**: 2026-10-21
**Spec**: `.specs/features/agent-identity/spec.md`
**Diff range**: feature commits 6ec1a99, 583e3bc, 4e5e7a7, 1897c40, 0cd16d2, 641cd5c, 8eb6761, 9026095, 16a0e95, 61e975f, aee919a, 558eb7f, da4db01, cd33de9 (interleaved with other features; verified on `main` at 4f8ea13, which includes the harness-onboarding merge 30a168f)
**Verifier**: independent sub-agent (author ≠ verifier), worktree `partyline-verify-ai`, branch `verify/agent-identity`

## Validation: agent-identity - FAIL

**Result**: FAIL. The code meets 15 of the 17 ACs and both gates pass. The verdict is FAIL for two reasons:
1. Two sensor mutants survived (M7, M16). Both trace to one vacuous assertion pattern: a reconnect or promotion that should unarchive an agent is checked by reading a field from a list that hides archived agents.
2. PROJ-09 ships narrower than the spec text (SPEC_DEVIATION), and nobody ratified the change.

---

## Task Completion

| Task | Status | Notes |
| ---- | ------ | ----- |
| T1 schema + migration 0011 | ✅ Done | `internal/store/agent_identity_migration_test.go:67`, `:83` |
| T2 identity on connect | ✅ Done | `internal/agents/identity.go:191` |
| T3 rename/promote/archive/list | ✅ Done | Unarchive assertions are vacuous (Fix 1) |
| T4 project room members | ✅ Done | `internal/rooms/members.go:134` |
| T5 plan + dispatcher | ✅ Done | PROJ-09 narrowed (Fix 2) |
| T6 project cursors + backlog | ✅ Done | `internal/agents/gateway.go` ack/advanceProject |
| T7 channel identity | ✅ Done | Derived-name tests replaced (4 removed, see Gate) |
| T8 `partyline agents` | ✅ Done | `internal/cli/agents_test.go:64` |
| T9 agents page | ✅ Done | `web/src/routes/_app/agents.test.tsx:355` |
| T10 projects in rooms UI | ✅ Done | `web/src/routes/_app/rooms/room.test.tsx:336` |
| T11 real UAT | ✅ Done | `uat.md` (one finding fixed in 558eb7f) |

---

## Spec-Anchored Acceptance Criteria

| AC | Spec-defined outcome | `file:line` + assertion | Result |
| -- | -------------------- | ----------------------- | ------ |
| ID-01 | Temporary agent `<Project> · <Harness label>`, ` #N` (N ≥ 2) while another exists | `internal/api/agent_identity_test.go:131` - `Equal([]string{"proj-claude","Proj · Claude Code"}, …)`; `:133` - `"proj-claude-2","Proj · Claude Code #2"`; `internal/cli/channel_names_test.go:33` (git root project, end to end) | ✅ (⚠️ tools-only edge, see Edge Cases) |
| ID-01 edge (concurrent) | Distinct numbers | `internal/api/agent_identity_test.go:202` - `Equal(want, got)` over 6 concurrent dials; killed M8b (lock removed) | ✅ for WebSocket sessions |
| ID-02 | Fixed agent `<n>` of the owner, created, project/harness/machine recorded | `internal/api/agent_identity_test.go:220` - `Equal([]any{AgentKindFixed,"proj","pi","box"}, …)`; `:224` newest wins (close 4000 "replaced") | ✅ |
| ID-03 | Project `home` + exactly one warning line (home or `/`) | `internal/cli/channel_names_test.go:93` - exact stderr equality (one line); `:98` `/` for listen; `:105` no warning with `--name`; `internal/cli/launcher_test.go:302`; `internal/channel/name_test.go:49` | ✅ |
| ID-04 | Rename 1–64 shows in timeline, members, outbound signature, mentions | `internal/api/agent_manage_test.go:55` members; `:56`/`:58` timeline; `:62` mention push; `:79` 422 `"must have 1 to 64 characters"`; `:85` 64 code points accepted; `internal/connectors/agent_rename_test.go:24` - `Equal("Ruthe bot", sent.AuthorName)` | ✅ |
| ID-05 | Promoted agent no longer archived | `internal/api/agent_manage_test.go:141` kind fixed; `:145` - `Zero(n)` after 48 h | ✅ (unarchive half vacuous at `:166`, M16 survived) |
| PROJ-01 | One project per (owner, slug); agent carries `project{id,slug,name}`, harness, machine, kind, archived | `internal/api/agent_identity_test.go:58` - `Equal(&ProjectRef{ID:"p1",Slug:"proj",Name:"Proj"}, a.Project)`; `:59`; `:145` one project per slug | ✅ |
| PROJ-02 | Archive after 24 h offline (temporary only); reconnect unarchives | Archive: `internal/api/agent_manage_test.go:115` - `Equal(1, n)` after 24 h + 1 min, zero at 23 h 59 (killed M6/M6b). Unarchive: `:126` - `False(listAgents(…,"")[gone.Name].Archived)` is **vacuous** (the default list hides archived agents, so a missing key reads `false`); M7 survived | ❌ GAP (unarchive half not covered) |
| PROJ-03 | Archived omitted without `include_archived=true` | `internal/api/agent_manage_test.go:117` - `NotContains(visible, gone.Name)`; `:121` - `True(all[gone.Name].Archived)` | ✅ |
| PROJ-04 | Grouped by project (online first), harness/machine/online, filters, archived toggle | `web/src/routes/_app/agents.test.tsx:355` - `toEqual(["Ruthe","Alpha","Web","No project"])`; `:385` toggle; `:412`/`:423` filters; e2e `web/e2e/smoke.spec.ts:249` | ✅ |
| PROJ-05 | Name changes everywhere, slug stable | `internal/api/agent_manage_test.go:190` - `Equal([]string{p.ID,"proj","Ruthe"}, …)`; `:192` readable names; `:201` 422 | ✅ (no live refresh for project-member rooms; Fix 4) |
| PROJ-06 | Manager must own or hold `use` on the project, else 403 | `internal/api/project_members_test.go:36` owner add; `:45` grantee add; `:48` stranger 403; `:49` unknown 403; `:62` grantee-less manager 403; killed M5 | ✅ |
| PROJ-07 | Only the active session (latest connected online); none → wait | `internal/api/project_delivery_test.go:79` active gets it, `:81` older `ExpectNone`; `:86` switch on disconnect; killed M1, M2 | ✅ |
| PROJ-08 | New session gets project backlog per room, 50 cap + notice | `internal/api/project_backlog_test.go:47` order; `:52` cursor; `:56` no repeat; `:111` - `Equal("5 older messages skipped; use the history tool", n.Text)`; `:116` cursor past skipped; killed M9, M9b, M9c | ✅ |
| PROJ-09 | Reply authored by that agent; **not delivered back to any session of the same project** | `internal/api/project_delivery_test.go:117` authorship; `:124` active `ExpectNone`; but `:119` asserts that a same-project session which is a direct room member **does** receive the reply | ❌ SPEC_DEVIATION (Fix 2) |
| PROJ-10 | `trusted` computed for the project owner | `internal/api/project_delivery_test.go:147`/`:150`/`:155`/`:158` - true/false per owner; killed M10 | ✅ |
| CLI-01 | `--name`/`--project` reach the session | `internal/cli/channel_names_test.go:65`; `internal/cli/launcher_test.go:283` env equality | ✅ |
| CLI-02 | `partyline agents` grouped by project with online state | `internal/cli/agents_test.go:64` exact output; `:90`/`:92` archived; `internal/api/agent_register_test.go:98` endpoint | ✅ |

**Status**: ❌ gaps in PROJ-02 and PROJ-09. ⚠️ spec-precision gaps on ID-01 and PROJ-07 for tools-only sessions.

**Payload/conjunction rule**: these payload fields are asserted on their values: the welcome `AgentInfo.display_name` (`agent_identity_test.go:131`), the full `dto.Agent` identity fields (`:59`, `:145`), `RoomMember.active_session` as a whole struct (`project_delivery_test.go:243`), `online` (`:242`, `:260`), `AgentMessage.room` and `trusted` (`project_backlog_test.go:46`, `:48`) and the `access.changed` payload (`project_delivery_test.go:277`).

---

## Edge Cases

- [x] Concurrent connects get distinct #N for WebSocket sessions: `internal/api/agent_identity_test.go:202`. M8b (allocation lock removed) was killed.
- [ ] Concurrent connects for **tools-only** sessions (Codex, Gemini): a scratch probe registered two Codex sessions of one project through `POST /agent/register`, and both got `proj-codex` / "Proj · Codex" (same agent id). The cause is `freeName`, which reuses any offline temporary agent (`internal/agents/identity.go:333`), and tools-only sessions are never online. The design text matches this behavior, but the spec edge case does not. ⚠️ Spec-precision gap (Fix 3).
- [x] The active session switches on disconnect: `internal/api/project_delivery_test.go:86`, `:254`. M2 was killed.
- [x] Project member + direct agent member → one delivery: `internal/api/project_delivery_test.go:97-98`. M4 was killed.

---

## Author-reported deviations

| # | Deviation | Verdict |
| - | --------- | ------- |
| 1 | PROJ-09 narrower: a same-project session that is a direct member still receives the active session's posts | **SPEC_DEVIATION, unratified.** The spec says "any session of the same project". `internal/delivery/rules.go:76` excludes only the project target, and `project_delivery_test.go:119` asserts the opposite of the spec text. The narrowing is defensible: direct membership is an explicit subscription, and agents migrated into one project keep talking to each other. Still, there is no `// SPEC_DEVIATION` marker and no STATE decision, so either amend PROJ-09 (and add an AD) or suppress the delivery. |
| 2 | The active session of a member project uses the room's REST tools; other sessions get non-member answers | **Acceptable (orchestrator decision); enforced and tested.** `internal/access/access.go:137` (`ActiveInProjectRoom`) and `project_delivery_test.go:201-212` (active) / `:215-220` (non-active: empty list_rooms, 403 "not a member of room", 404 reply/attachment). M13 (any project session allowed) was killed. One side effect: a session loses its tools in the room as soon as a newer session of the project connects. This is consistent with PROJ-07. |
| 3 | The project cursor advances on an ack from ANY session of the project | **Acceptable.** The dedupe case requires it: the active session that is also a direct member must move the project cursor (`project_backlog_test.go:85`). Only the same owner's agents can ack, and an acked message was in fact delivered to a session of the project, which matches PROJ-08's "undelivered". |
| 4 | Tools-only sessions never become active sessions | **Acceptable under the literal PROJ-07 ("online"), but a spec-precision gap.** A project whose only sessions are Codex/Gemini (or the tools half of pi/OpenCode without listen) never receives project messages. They wait indefinitely, and the hook's `Pending` covers member rooms only. `docs/agents.md:81` does not mention this (Fix 3). |
| 5 | The derived unique-name mode was removed and the harness-onboarding tests rewritten | **Acceptable.** The removed tests are `TestDeriveName`, `TestLinkDerivedNameAsksForFreeNameThenKeepsIt`, `TestDerivedSessionsGetDistinctNamesAndToolsUseThem` and `TestChannelDerivedNamesInSameProject`, replaced 1:1 by project-based equivalents (`internal/channel/name_test.go:26`, `:135`, `:168`; `internal/cli/channel_names_test.go:33`). The server keeps unique mode for old clients (`agent_identity_test.go:279`). Spec hygiene: harness-onboarding NAME-01 (`.specs/features/harness-onboarding/spec.md:95`) is superseded by ID-01 but not annotated. |
| 6 | Additions: `GET /api/v1/agent/agents`, `active_session`, live member refresh, `access.changed` on project share, env vars, migration 0012 | **Acceptable; authz verified.** `/agent/agents` filters on `a.OwnerID == p.UserID` (`internal/api/agent_handlers.go:74`), and `agent_register_test.go:98` asserts that bob's shared agent is left out, that user tokens get 403 and anonymous callers 401. M14 (filter removed) was killed. `access.changed` reaches only the grantee (`project_delivery_test.go:277-278`). Migration 0012 author naming is tested by `internal/connectors/sender_test.go:280` and `project_delivery_test.go:180`. Nothing leaks across owners: projects are keyed by (owner, slug) (`internal/agents/identity.go:311`), and the project routes return 404 to strangers (`agent_manage_test.go:224`). |

---

## Discrimination Sensor

The sensor ran in a detached scratch worktree `partyline-verify-ai-mut`, which was removed afterwards. The verify worktree's porcelain matched the empty baseline after cleanup. Each mutant ran `go test -count=1 -run <pattern>` on the covering packages.

| Mutation | File:line | Description | Killed? |
| -------- | --------- | ----------- | ------- |
| M1 | `internal/store/db/agents.sql.go:885` | Active session ordered `connected_at ASC` | ✅ Killed (`TestProjectMessageGoesToTheActiveSession` +4) |
| M2 | `internal/delivery/active.go:21` | Only the newest agent considered (no switch on disconnect) | ✅ Killed |
| M3 | `internal/delivery/rules.go:76` | Plan keeps the author's project | ✅ Killed (`TestPlanProjectMembers`, `TestProjectRepliesDoNotEchoToTheProject`) |
| M3b | `internal/delivery/dispatcher.go:121` | Dispatcher stops setting `AuthorProjectID` | ✅ Killed |
| M4 | `internal/delivery/dispatcher.go:140` | Dedupe with the direct member removed | ✅ Killed (`TestProjectActiveSessionThatIsAMemberGetsEachMessageOnce`) |
| M5 | `internal/rooms/members.go:140` | PROJ-06 use/owner check skipped | ✅ Killed (`TestAddProjectMember`, `…NeedsUseOnTheProject`) |
| M6 | `internal/agents/archive.go:13` | `ArchiveAfter` 24 h → 23 h | ✅ Killed |
| M6b | `internal/agents/archive.go:13` | `ArchiveAfter` 24 h → 25 h | ✅ Killed |
| M7 | `internal/store/db/agents.sql.go:1018`, `:1098` | Reconnect no longer clears `archived_at` | ❌ **Survived**. Fix 1 |
| M8 | `internal/agents/identity.go:333` | Free-name choice ignores presence | ✅ Killed |
| M8b | `internal/agents/gateway.go:174` | Allocation lock removed | ✅ Killed (`TestAgentWSConcurrentTemporarySessionsGetDistinctNumbers`) |
| M8c | `internal/agents/identity.go:68` | ` #N` starts at 3 | ✅ Killed |
| M9 | `internal/agents/gateway.go` (`ack`) | Ack no longer advances the project cursor | ✅ Killed |
| M9b | `internal/store/db/agents.sql.go:92`, `:762` | Project backlog includes the project's own posts | ✅ Killed |
| M9c | `internal/agents/gateway.go` (`backlog`) | Cap skip does not move the cursor | ✅ Killed |
| M10 | `internal/delivery/dispatcher.go:177` | Project trust computed for the room owner | ✅ Killed (`TestProjectDeliveryTrustFollowsTheProjectOwner`) |
| M11 | `internal/store/db/agents.sql.go:671` | The list no longer hides archived agents | ✅ Killed |
| M12 | `internal/channel/name.go:41` | `/` no longer counts as home | ✅ Killed |
| M12b | `internal/channel/name.go:40` | Home compared without `Clean` | ✅ Killed |
| M13 | `internal/access/access.go:151` | Any session of the project gets post | ✅ Killed (`TestProjectActiveSessionUsesTheRoomTools`) |
| M14 | `internal/api/agent_handlers.go:74` | `/agent/agents` leaks shared agents | ✅ Killed |
| M15 | `internal/agents/registry.go:321` | Rename limit 64 → 65 | ✅ Killed |
| M16 | `internal/store/db/agents.sql.go:1032` | Promotion no longer clears `archived_at` | ❌ **Survived**. Fix 1 |

**Sensor depth**: expanded (critical path: delivery routing + authorization), 23 behavior-level mutations.
**Sensor outcome**: 21 of 23 killed, 2 survived. FAIL.

M7 was confirmed with a scratch probe that asserts the agent is listed again after reconnect. The probe passed on the real code and failed under M7.

---

## Gate Check

- **Build gate**: `make check` in the verify worktree exited 0. Details: check-generate clean, `golangci-lint` 0 issues, `go vet`, all 31 Go packages `ok` with `-race`, web Vitest 338/338, integrations `bun test` 53 pass / 0 fail, web build, `go build ./...`. `TestLinkPausesAfterReplaced` did not fail, so no rerun was needed.
- **E2E**: `make e2e` exited 0 with 13 passed and 1 skipped. The skip is the mobile-360 project-picker smoke (`web/e2e/smoke.spec.ts:277`), which is expected.
- **Test delta (feature commits)**: +74 test functions / `it` cases and −4. The 4 removed tests belong to the derived-name mode, replaced 1:1 (deviation 5). No assertion weakening was found beyond Fix 1.

---

## Code Quality

| Principle | Status |
| --------- | ------ |
| Minimum code / surgical changes | ✅ |
| No scope creep | ✅ (additions in deviation 6 serve ACs or the decided REST rule) |
| Matches patterns (sqlc, dto/tygo, testkit, access levels) | ✅ |
| Spec-anchored outcome check | ❌ PROJ-09 test asserts behavior the spec forbids; PROJ-02/ID-05 unarchive asserted vacuously |
| Per-layer coverage (domain 1:1, routes happy+edge+error) | ✅ (auth matrix `agent_manage_test.go:224`) |
| Every test maps to an AC / edge case / Done-when | ✅ |
| Guidelines followed: `AGENTS.md`, `.specs/LESSONS.md` | ⚠️ L-019 (contract-note behavior needs a test) is partly unmet: the "promotion clears `archived_at`" behavior in the contract notes has no discriminating test |

---

## Fix Plans

### Fix 1: Unarchive assertions are vacuous (M7, M16 survived) - Major

- **Root cause**: `listAgents(t, alice, "")` hides archived agents, so `[name].Archived` on a still-archived agent reads the zero value `false`. See `internal/api/agent_manage_test.go:126` and `:166`.
- **Fix task**: in `TestTemporaryAgentArchivedAfterADayOffline` and `TestPromotingAnArchivedAgentUnarchivesIt`, assert that the agent is present in the default list (`a, ok := …; require.True(ok)`) and/or read `?include_archived=true` and assert `Archived == false`.
- **Done when**: M7 (drop `archived_at = NULL` from `MarkAgentConnected` and `UpdateAgentIdentity`) and M16 (drop it from `PromoteAgent`) both fail the tests.

### Fix 2: PROJ-09 narrowed without ratification - Major (decision)

- **Root cause**: the spec says the reply is not delivered to "any session of the same project". The implementation delivers it to a same-project session that is a direct room member (`internal/delivery/rules.go:76`; `project_delivery_test.go:119`).
- **Fix task**: either (a) André ratifies the narrower rule: amend PROJ-09 to "SHALL NOT be delivered back through the project member" and record an AD in `STATE.md` (no code change); or (b) exclude direct agent targets whose project is the author's project when the room has that project as a member, and flip the assertion at `:119`.
- **Done when**: spec text and test agree.

### Fix 3: Tools-only sessions vs ID-01 edge case / PROJ-07 - Minor (spec precision + docs)

- **Root cause**: tools-only sessions are never online, so concurrent Codex/Gemini sessions of one project share one temporary agent (`internal/agents/identity.go:333`, probe), and they never become a project's active session.
- **Fix task**: amend ID-01's edge case and PROJ-07 to state the tools-only behavior, or give tools-only registrations distinct numbers. Document in `docs/agents.md:81` that a project member reaches only channel/listen sessions.

### Fix 4: Project rename does not refresh project-member rooms live - Minor

- **Root cause**: `RenameProject` publishes `MembersChanged` only for rooms where the project's temporary agents are direct members (`internal/agents/projects.go:104`, `:115`). Rooms that have the project itself as a member keep the old name in open viewers until they refetch.
- **Fix task**: also publish for the project's member rooms (`kind = 'project'`), with a live test.

---

## Requirement Traceability Update

| Requirement | Previous Status | New Status |
| ----------- | --------------- | ---------- |
| ID-01, ID-02, ID-03, ID-04, ID-05 | Implementing | ✅ Verified (ID-01 tools-only edge ⚠️) |
| PROJ-01, PROJ-03, PROJ-04, PROJ-05, PROJ-06, PROJ-07, PROJ-08, PROJ-10 | Implementing | ✅ Verified |
| PROJ-02 | Implementing | ❌ Needs Fix (Fix 1) |
| PROJ-09 | Implementing | ❌ Needs decision (Fix 2) |
| CLI-01, CLI-02 | Implementing | ✅ Verified |

---

## Summary

**Overall**: ❌ Not Ready. Fix 1 changes tests only, and Fix 2 can be closed by a spec amendment.

- **Spec-anchored check**: 15/17 ACs match the spec outcome; 2 gaps (PROJ-02 unarchive, PROJ-09); 2 spec-precision gaps flagged (ID-01 and PROJ-07 for tools-only sessions).
- **Sensor**: 21/23 mutations killed (M7 and M16 survived).
- **Gate**: `make check` exit 0; `make e2e` 13 passed, 1 expected skip.

**What works**:
- Readable temporary names, with concurrency-safe numbering for WebSocket sessions.
- Fixed agents with newest-wins.
- The home-folder warning.
- Rename, promote and the 24 h archive.
- Project members, with owner/use authorization.
- Active-session delivery that switches on disconnect and delivers once to a session that is also a direct member.
- Echo suppression through the project.
- Project backlog with the cap, notice and cursor.
- Per-owner trust.
- Owner-scoped `/agent/agents`.
- The CLI listing and the web grouping and filters.

**Next steps**: Fix 1 (tests) → re-verify. Get André's decision on Fix 2. Fixes 3 and 4 can follow as minor tasks.

---

## Lessons

- L-038 (surviving_mutant, tests): when the list under test filters items out, assert that an item is present before reading its fields.
- L-039 (spec_deviation, delivery): an exclusion rule on a group must say whether it also covers the group's direct recipients.
- L-040 (spec_precision_gap, specs): presence-based rules must say how they apply to sessions that never connect live.
- L-019 (confirmed) penalized once. It was in this feature's guidelines, and the same failure happened again: the contract notes state "promotion clears `archived_at`", and no test discriminates it (M16).
