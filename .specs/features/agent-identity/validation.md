# Agent Identity Verification Report

## Validation (iteration 2): agent-identity - FAIL

**Date**: 2026-10-21
**Spec**: `.specs/features/agent-identity/spec.md` (PROJ-09 amended per AD-009; tools-only precision row in Assumptions and Edge Cases)
**Diff range**: `334dcab..99021f2` (T12-T20: 39c0515, cc59327, 9b330be, e36482a, 8c4f1ca, cb20271, cdced07, 23e7464, 99021f2), verified on `main` at 99021f2
**Verifier**: independent sub-agent (author ≠ verifier), worktree `partyline-verify-ai`, branch `verify/agent-identity-2`

**Result**: FAIL. All four iteration-1 gaps are closed: M7 and M16 are killed, PROJ-09 and AD-009 agree with the tests, tools-only sessions get their own agents, and a project rename refreshes project-member rooms. Both gates pass. The verdict is FAIL because 2 of the 21 sensor mutants survived, both on the new session-key hold logic, and validate.md does not mark a feature done while a mutant survives. The code behaves correctly: scratch probes pass on the real code and kill both mutants. Both fixes are tests only.
1. **M17a**: `freeName`'s held-agent lookup (`internal/agents/identity.go:372`) can be deleted and every test still passes. Without it, a tools-only session whose agent is not the lowest free one in its family moves to a lower agent once that agent frees up (an ended live session, or an archived sibling). This is the ID-01 edge case "the same key gets it back".
2. **M17d**: the owner filter of `ListSessionAgents` (`internal/store/queries/agents.sql:104`) can be dropped and every test still passes. The lookup returns a name, which is then resolved within the owner, so no other owner's agent is ever reachable. Under the mutant, though, another owner's hold on the same key steers a session onto its own owner's agent that a different session holds.

### Iteration-1 gaps

| Gap | Fix | Re-check | Status |
| --- | --- | -------- | ------ |
| Fix 1: M7/M16 survived (vacuous unarchive) | T12 `39c0515` | `internal/api/agent_manage_test.go:132-139` `requireUnarchived` (present in the default list + `archived == false` with `include_archived=true`), used at `:126` (reconnect) and `:179` (promotion); `:178` - `Equal([]any{AgentKindFixed,false}, …)` on the PATCH answer. M7 and M16 were re-run in scratch and are **killed** | ✅ Closed |
| Fix 2: PROJ-09 SPEC_DEVIATION | T13 `cc59327` + AD-009 | Spec PROJ-09 text, `STATE.md` AD-009, `contract-notes.md` "PROJ-09 (AD-009, T13)" and the comment at `internal/delivery/rules.go:76` all agree. Both halves are tested: no echo through the project at `internal/api/project_delivery_test.go:125` (`active.ExpectNone`) and in the plan at `internal/delivery/rules_test.go:283`; the direct member still receives the post at `project_delivery_test.go:120` and `rules_test.go:284-285` (`AgentIDs == ["A1"]`). M3 (the plan keeps the author's project) still fails `TestPlanProjectMembers` | ✅ Closed |
| Fix 3: tools-only sessions collapse into one agent / PROJ-07 precision | T14, T17, T18, T19, T20 | See ID-01 edge rows below. Spec Assumptions now state the tools-only rule; `docs/agents.md:81-98` documents it | ✅ Closed (2 weak tests, see Result) |
| Fix 4: project rename does not refresh project-member rooms | T15 `e36482a` | `internal/agents/projects.go:102` appends `ListProjectRoomIDs`; `internal/api/agent_manage_test.go:303-330` - the viewer (bob) and the owner get `members.changed` for the room, carol (no access) `ExpectNone` (`:322`), and the member list reads `["Ruthe"]` (`:330`). M20 killed | ✅ Closed |
| NAME-01 superseded note | T16 `8c4f1ca` | `.specs/features/harness-onboarding/spec.md:96`, traceability row `:200` | ✅ |

### Spec-anchored acceptance criteria (iteration 2)

ACs that the diff did not touch were re-checked through the full gate (no regression; all iteration-1 evidence tests still pass). Rows changed by this iteration:

| AC | Spec-defined outcome | `file:line` + assertion | Result |
| -- | -------------------- | ----------------------- | ------ |
| ID-01 (tools-only edge: different keys → distinct agents) | Two tools-only sessions of one project/harness with different keys get distinct temporary agents (` #N`) | `internal/api/agent_session_test.go:47-49` - `Equal(["proj-codex","Proj · Codex"])`, `Equal(["proj-codex-2","Proj · Codex #2"])`, `NotEqual(one.ID, two.ID)`; end to end through the CLI `internal/cli/hook_test.go` `TestToolsOnlySessionsAndTheirHooksKeepTheirOwnAgents`; stripped environment `internal/cli/procenv_test.go:153-175` | ✅ |
| ID-01 (tools-only edge: same key → own agent back) | A session that registers again with its key (tools or turn-end hook) gets its own agent | `agent_session_test.go:51-52`, `:104`, `:137` (listen joins its tools' agent); hook `procenv_test.go:168-173`. **Not discriminating** for the held lookup: in every scenario the loop already reaches the session's own agent first (M17a survived; probe below) | ⚠️ weak (fix task) |
| ID-01 edge (concurrent) | Distinct numbers for simultaneous sessions | WebSocket: iteration-1 evidence (`agent_identity_test.go`), still green. Registration: `agent_session_test.go:89` - `Equal(want, got)` over 6 concurrent keyed registrations; M25 (registration without `allocMu`) killed 3/3 | ✅ |
| ID-01 (holds scoped by owner and machine, T19) | Same key on another machine → another agent; same key + machine → same agent; another owner's key never reaches the owner's agents | `agent_session_test.go:167-176`; M17c (machine scope ignored) killed. Owner scope: `:174-176` cannot tell (resolution by name is per owner anyway), so M17d survived | ⚠️ weak (fix task) |
| ID-01 (hold lifetime) | A hold lasts until the agent is archived; a registration counts as activity; a live connect releases it | `agent_session_test.go:108` `Zero(n)`, `:110` "a held agent is not taken over", `:115-118`; `:144` "a live session does not hold its agent after it ends". M24, M23, M29 killed | ✅ |
| ID-01 / CLI (T17, `/proc` recovery) | Only `PARTYLINE_SESSION`, `PARTYLINE_HARNESS`, `PARTYLINE_PROJECT`, `PARTYLINE_AGENT`; nearest ancestor; own vars win; depth 4 | `internal/cli/procenv_test.go:49-51` exact map (token, URL and PATH left out); `:66-83`; `:116-126`; real `/proc` `:101-104`. M18, M26, M27 killed | ✅ |
| ID-01 / HAR-01 (T18, `--harness`) | `channel --tools-only --harness codex|gemini|opencode|pi` sends that harness and wins over `PARTYLINE_HARNESS`; others exit 2; setup writes the new command lines; old entries stay owned | `internal/cli/channel_config_test.go` `TestToolsOnlyChannelHarnessFlag` (`["codex","Notes · Codex"]`, `"gemini"`), `TestToolsOnlyChannelHarnessFlagErrors` (exit 2 + exact message); `internal/harness/clis_test.go:178`, `:342`, `TestToolsOnlyEntriesWithoutHarnessFlagStayOwned`. M19 killed | ✅ |
| ID-01 (T20, launcher pid key) | On Unix `PARTYLINE_SESSION` = launcher pid | `internal/cli/launcher_test.go:172-175` - `Equal(strconv.Itoa(r.pid), id)` under `execKeepsPID`. M21 killed | ✅ |
| PROJ-02 | Archive after 24 h offline; reconnect unarchives | `agent_manage_test.go:115`, `:126` (`requireUnarchived`); M7 killed | ✅ |
| ID-05 | Promoted agent no longer archived (and unarchived) | `agent_manage_test.go:178-179`; M16 killed | ✅ |
| PROJ-05 | Name changes everywhere, slug stable (live in project-member rooms) | `agent_manage_test.go:330` `Equal(["Ruthe"], names)` + live event `:318-321`; M20 killed | ✅ |
| PROJ-07 (tools-only precision) | Tools-only sessions are never online, so they are never the active session | Unchanged code path (`presence`-based active session); spec Assumptions row + `docs/agents.md:96-98`. No new test needed: iteration-1 evidence `project_delivery_test.go` active-session tests | ✅ |
| PROJ-09 (amended, AD-009) | No delivery through the project member to any session of the project; a direct-member session of the project still receives it | `project_delivery_test.go:118-120`, `:125`; `rules_test.go:283-285` | ✅ |

**Status**: 17/17 ACs match the spec outcome. The ID-01 tools-only edge case holds 2 weak (non-discriminating) assertions, listed above. No spec-precision gap is open.

**Payload/conjunction rule**: the session tests compare whole `dto.AgentInfo` values (`agent_session_test.go:51`, `:104`, `:117`, `:137`, `:144`, `:170`), not just names, so ID, name, display name and owner are all asserted. The rename test asserts the event's `room_id` (`agent_manage_test.go:319`, `:321`) and the member's `name` (`:330`). The launcher test asserts the key value (`launcher_test.go:175`).

### Discrimination sensor (iteration 2)

Scratch: a detached worktree `partyline-verify-ai-mut` at 99021f2. Each mutant was applied by exact-string edit, the covering `go test -count=1 -run …` ran, and the edit was reverted with `git checkout -- .`. The worktree was then removed and `git worktree prune` run. Afterwards the verify worktree's `git status --porcelain` was empty, matching the baseline.

| Mutation | File:line | Description | Killed? |
| -------- | --------- | ----------- | ------- |
| M7 (re-run) | `internal/store/db/agents.sql.go:1121`, `:1210` | Reconnect / identity update keeps `archived_at` | ✅ Killed (`TestTemporaryAgentArchivedAfterADayOffline`, `TestToolsOnlySessionHoldsItsAgentUntilArchived`) |
| M16 (re-run) | `internal/store/db/agents.sql.go:1135` | Promotion keeps `archived_at` | ✅ Killed (`TestPromotingAnArchivedAgentUnarchivesIt`) |
| M17a | `internal/agents/identity.go:372` | Held-agent lookup disabled (key ignored for reuse) | ❌ **Survived**. Fix 5 |
| M17b | `internal/agents/identity.go:392` | Loop ignores holds of other keys | ✅ Killed (5 tests) |
| M17c | `internal/agents/identity.go:371` | Machine scope ignored (`sameMachine` always true) | ✅ Killed (`TestSessionHoldsAreScopedByOwnerAndMachine`) |
| M17d | `internal/store/db/agents.sql.go:1026` (`ListSessionAgents`) | Owner scope ignored (`owner_id = ? OR 1`) | ❌ **Survived**. Fix 6 |
| M17e | `internal/store/db/agents.sql.go:1026` | Held lookup matches any held agent of the family (key ignored) | ✅ Killed (4 tests) |
| M18 | `internal/cli/procenv.go:26-27` | `/proc` whitelist keeps every `PARTYLINE_*` variable (token, URL) | ✅ Killed (`TestLauncherEnvironKeepsOnlyLauncherVariables` +2) |
| M19 | `internal/cli/channel.go:154-155` | `--harness` ignored with `--tools-only` | ✅ Killed (`TestToolsOnlyChannelHarnessFlag`) |
| M20 | `internal/agents/projects.go:102` | Rename skips the project-member rooms | ✅ Killed (`TestProjectRenameRefreshesItsRoomsLive`) |
| M21 | `internal/cli/launcher.go` (`launchSessionID`) | Launcher key random on Unix again | ✅ Killed (`TestLaunchersExecTheHarness/*`) |
| M22 | `internal/agents/identity.go:139-141` | Any non-empty session key accepted | ✅ Killed (`TestLinkSendsTheSessionKey`; client and server share `agents.SessionKey`) |
| M23 | `internal/agents/identity.go:272` | A live connect keeps the hold | ✅ Killed (`TestLiveSessionsAndToolsOnlySessionKeys`) |
| M24 | `internal/store/db/agents.sql.go` (`HoldAgentForSession`) | A hold no longer refreshes `last_seen_at` | ✅ Killed (`TestToolsOnlySessionHoldsItsAgentUntilArchived`) |
| M25 | `internal/agents/registry.go:159-162` | `POST /agent/register` without the shared `allocMu` | ✅ Killed 3/3 runs (`TestConcurrentToolsOnlySessionsGetDistinctNumbers`) |
| M26 | `internal/cli/procenv.go:90` | Ancestor variables win over the process's own | ✅ Killed (`TestWithLauncherEnv`) |
| M27 | `internal/cli/procenv.go:15` | `maxAncestors` 4 → 5 | ✅ Killed (`TestAncestorLauncherEnvNearestWins`) |
| M28 | `internal/cli/hook.go:69` | Hook sends no session key | ✅ Killed (`TestToolsOnlySessionsAndTheirHooksKeepTheirOwnAgents`, `TestStrippedToolsOnlyChannelRecoversLauncherVariables`) |
| M29 | `internal/agents/identity.go:233` | Tools-only registration never holds | ✅ Killed (5 tests) |

**Probes** (scratch only, never committed). Each passes on the real code and fails under its mutant:
- *Held agent beats a freed lower agent*: a live session takes `proj-opencode` and tools-only `pl-tools` takes `proj-opencode-2`. The live session ends, then `pl-tools` registers again. Expected `proj-opencode-2`; under M17a it gets `proj-opencode`. Killed M17a.
- *Another owner's hold does not steer*: bob's `b1` holds `proj-codex` and `b2` holds `proj-codex-2`; alice's key `4242` holds her `proj-codex-2`. Bob's `4242` must get `proj-codex-3`. Under M17d it takes bob's `b2` agent. Killed M17d.
- *Invalid key ignored server-side*: two registrations with invalid keys (`bad key!`, `other key!`) get the same agent, as keyless ones do. Killed M22 at API level too.

**Sensor depth**: expanded (P0: identity and authorization of agent allocation), 21 behavior-level mutations.
**Sensor outcome**: 19 of 21 killed, 2 survived (M17a, M17d). FAIL.

### Security assessment: session keys

- **Cross-owner**: none possible. `ListSessionAgents` filters `owner_id` (`internal/store/queries/agents.sql:104`), and the chosen name is resolved with `GetAgentByName{OwnerID}` (`internal/agents/identity.go:284`, `:300`, `:385`), so even with M17d a key cannot reach another owner's agent. Bob's test (`agent_session_test.go:173-176`) and the probe confirm this.
- **Online agent of another live session**: not reachable. The loop skips online agents (`identity.go:393`). Only tools-only registrations create holds, and only on an offline agent they picked. Every WebSocket resolve clears a hold (`identity.go:272`), so an agent can never be held and online at the same time. The "online or not" branch serves only the session's own key.
- **Spoofing `X-Partyline-Session`**: the effect stays within one owner. The agent token is owner-wide, and its holder can already name any of the owner's agents through `X-Partyline-Agent` (`internal/api/agent_handlers.go:167`), so a forged key adds no capability. The worst case is confusion between the owner's own sessions. Accepted.
- **Header validation**: `^[A-Za-z0-9._-]{1,64}$` (`identity.go:136`). Any other value becomes `""`, which falls back to the keyless rule. The client applies the same filter (`internal/channel/name.go:65`). M22 is killed.
- **`/proc` reader**: paths are built only from integer pids under a constant root (`filepath.Join(procRoot, strconv.Itoa(pid), …)`), so no attacker-controlled path or symlink target is involved. The reader takes at most 4 ancestors and stops at pid 1. It keeps only the 4 whitelisted keys; `PARTYLINE_TOKEN` and `PARTYLINE_URL` are dropped (`procenv_test.go:46-51`, M18 killed). It logs nothing: there are no fmt or log calls in `procenv.go`. Unreadable environments are skipped. Reads are bounded by the kernel's per-process environment size. One trust note: the ancestors are the user's own processes, whose environment `/proc` only exposes to the same uid or root. A recovered `PARTYLINE_AGENT` therefore cannot come from another user.
- **Migration 0014**: `ALTER TABLE agents ADD COLUMN session_key TEXT NOT NULL DEFAULT ''` is valid SQLite on populated tables, and `''` means not held, so existing agents keep the keyless behavior. `check-generate` shows no drift. There is no dedicated migration test; the full suite runs it on every fresh database. Acceptable.
- **Concurrency**: one `Registry.allocMu` covers both the WebSocket (`gateway.go:172`) and `Register` (`registry.go:160`). M25 is killed.
- **PID-key reuse**: acceptable. A pid is unique while its process runs. A recycled pid can only re-adopt an offline agent of the same owner, machine, project and harness, which matches the old "lowest offline agent" reuse. Stale holds from dead sessions block reuse for up to 24 h, so a day of many short Codex sessions in one project adds ` #N` agents until they are archived. They count toward the 50-agent cap. Observation only, not a spec violation: the spec says a hold lasts "until archived".
- **Residual (documented, pre-existing)**: Codex started without the launcher sends a keyless hook (`hook.go:69`), which reads the project's lowest offline temporary agent. With two such sessions, the hook of the second one acts as the first one's agent. This is stated in `docs/agents.md:91-92` and the contract notes.
- **Windows/macOS**: `GOOS=windows go build ./...`, `GOOS=windows go vet` (cli, agents, channel), `GOOS=windows go test -c ./internal/cli/` and `GOOS=darwin GOARCH=arm64 go build ./...` all succeed. `execKeepsPID` is false on Windows, which keeps the random key.

### Gate check (iteration 2)

- **`make check`** exited 0. Details: check-generate clean, `golangci-lint` 0 issues, `go vet`, 31 Go packages `ok` with `-race`, web Vitest 338/338 (32 files), integrations `bun test` 53 pass / 0 fail, web build, `go build ./...`. `TestLinkPausesAfterReplaced` did not fail, so no rerun was needed.
- **`make e2e`** (`PLAYWRIGHT_CHROMIUM_EXECUTABLE` = headless shell 1248) exited 0 with 13 passed and 1 skipped. The skip is the expected mobile-360 `smoke.spec.ts:277`.
- **Test delta (`334dcab..99021f2`)**: 17 test functions added and 0 removed. Assertions were strengthened, not weakened: `agent_manage_test.go:126`, `:178-179`. The changed setup tests only update expected command lines (`setup_harness_test.go:56-57`, `clis_test.go:178`, `:342`, `:411`).
- **Completion gate**: `validate_state.py --root . agent-identity` exits 1 with "verdict is FAIL - route the ranked gaps to fix tasks". That is expected for this verdict.
- **Spec/tasks gates**: `validate_spec.py` 0/0. `validate_tasks.py` 0 errors and 1 warning (T16 `Tests: none`, a docs task).

### Code quality (iteration 2)

| Principle | Status |
| --------- | ------ |
| Minimum code / surgical changes | ✅ (`freeName` extension, one migration, one query per need) |
| No scope creep | ✅ (`--harness` and the launcher pid key are needed to close Fix 3 end to end) |
| Matches patterns (sqlc, dto headers, testkit, `stdio` injection) | ✅ |
| Spec-anchored outcome check | ⚠️ ID-01 "own agent back" and owner scope of holds asserted non-discriminatingly (M17a, M17d) |
| Every test maps to an AC / edge case / Done-when | ✅ |
| Guidelines followed: `AGENTS.md` (no secrets logged, synthetic fixtures), `.specs/LESSONS.md` | ✅. L-038 applied (`requireUnarchived` asserts presence first) |

### Fix plans (iteration 2)

#### Fix 5: The held-agent lookup is not discriminated (M17a) - Major (tests only)
- **Root cause**: every keyed-reuse test puts the session's own agent at the lowest free slot reachable by the loop, so the held lookup at `internal/agents/identity.go:372-382` never decides an outcome.
- **Fix task**: in `internal/api/agent_session_test.go`, add a test where a lower-numbered agent of the family becomes free while a tools-only session holds a higher one. Two ways to free it: a live session's agent going offline, or a sibling's agent being archived. Assert that the session re-registers to its own agent (whole `AgentInfo`).
- **Done when**: M17a (lookup disabled) fails the test in a scratch worktree.

#### Fix 6: The owner scope of session holds is not discriminated (M17d) - Minor (tests only; defense in depth)
- **Root cause**: `TestSessionHoldsAreScopedByOwnerAndMachine` (`agent_session_test.go:173-176`) gives bob no held agents of his own, so a cross-owner hold list only yields a name that bob would get anyway.
- **Fix task**: add a case where bob already holds `proj-codex` and `proj-codex-2` with his own keys, and alice's key `K` holds her `proj-codex-2`. Bob's registration with `K` must get a new `proj-codex-3`, and bob's `proj-codex-2` stays with its session.
- **Done when**: M17d (`owner_id = ? OR 1` in `ListSessionAgents`) fails the test.

#### Optional (not blocking)
- Add an API-level test that the server ignores an invalid `X-Partyline-Session` value (probe 3). Today the server-side filter is covered only through the shared client function.

### Requirement traceability update (iteration 2)

| Requirement | Previous | New |
| ----------- | -------- | --- |
| PROJ-02 | ❌ Needs Fix | ✅ Verified (M7 killed) |
| PROJ-09 | ❌ Needs decision | ✅ Verified (AD-009; both halves tested) |
| ID-05, PROJ-05 | ✅ (weak / no live refresh) | ✅ Verified (M16, M20 killed) |
| ID-01 | ✅ (tools-only ⚠️) | ⚠️ Behavior verified, 2 weak tests (Fix 5, Fix 6) |
| others | ✅ Verified | ✅ Verified (no regression) |

### Summary (iteration 2)

**Overall**: ❌ Not ready. Two test-only fix tasks remain (Fix 5, Fix 6). The code behaves correctly, and the probes prove the missing assertions can be written. This was iteration 2 of 3.

- **Spec-anchored check**: 17/17 ACs match the spec outcome. ID-01 carries 2 non-discriminating assertions.
- **Sensor**: 19/21 killed (M17a and M17d survived; M7 and M16 now killed).
- **Gate**: `make check` exit 0; `make e2e` 13 passed, 1 expected skip; Windows and macOS cross-builds compile.

**Lessons (iteration 2)**:
- L-041 (surviving_mutant, tests, new candidate) from M17a: when a preferred lookup runs before a fallback, test a case where the fallback alone would pick a different result.
- L-018 (confirmed) penalized once (harmful 1) for M17d. L-018 was in this feature's guidelines and the same failure recurred: the negative owner-scope test was not isolated from the later owner-scoped name resolution, which hid the missing filter. No separate lesson was added, to avoid a near-duplicate.
- L-038 was applied and worked: M7 and M16 are killed.

---


## Iteration 1 report

**Date**: 2026-10-21
**Spec**: `.specs/features/agent-identity/spec.md`
**Diff range**: feature commits 6ec1a99, 583e3bc, 4e5e7a7, 1897c40, 0cd16d2, 641cd5c, 8eb6761, 9026095, 16a0e95, 61e975f, aee919a, 558eb7f, da4db01, cd33de9 (interleaved with other features; verified on `main` at 4f8ea13, which includes the harness-onboarding merge 30a168f)
**Verifier**: independent sub-agent (author ≠ verifier), worktree `partyline-verify-ai`, branch `verify/agent-identity`

## Iteration 1 verdict: agent-identity - FAIL

**Iteration 1 verdict**: FAIL. The code meets 15 of the 17 ACs and both gates pass. The verdict is FAIL for two reasons:
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
