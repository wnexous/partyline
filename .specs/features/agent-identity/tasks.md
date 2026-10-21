# Agent Identity Tasks

## Execution Protocol (MANDATORY -- do not skip)

Implement these tasks with the `tlc-spec-driven` skill: **activate it by name and follow its Execute flow and Critical Rules.** Do not search for skill files by filesystem path. The skill is the source of truth for the full flow (per-task cycle, sub-agent delegation, adequacy review, Verifier, discrimination sensor).

**If the skill cannot be activated, STOP and tell the user - do not proceed without it.**

---

**Design**: `.specs/features/agent-identity/design.md`
**Status**: Approved

---

## Test Coverage Matrix

> Guidelines: `AGENTS.md`, all `contract-notes.md`, `.specs/LESSONS.md`.

| Code Layer | Required Test Type | Coverage Expectation | Location Pattern | Run Command |
| --- | --- | --- | --- | --- |
| Store/services/delivery | integration | Every AC + edge cases | `internal/**/*_test.go` | `make test` |
| HTTP/WS/CLI | e2e (testkit) | Happy + edge + error; auth matrix | `internal/api/*_test.go`, `internal/cli/*_test.go` | `make test` |
| Web | unit (Vitest) + e2e smoke update | Each visible AC | `web/src/**/*.test.tsx`, `web/e2e` | `cd web && bun run test`, `make e2e` |

## Gate Check Commands

| Gate Level | When to Use | Command |
| --- | --- | --- |
| Quick | Unit-only | `go test -count=1 ./internal/...` / `cd web && bun run test` |
| Full | Integration/e2e | `make test` |
| Build | Before every commit | `make check` |

---

## Execution Plan

### Phase 1: Model and registration

```
T1 → T2 → T3 → T4
```

### Phase 2: Project delivery

```
T5 → T6 → T7
```

### Phase 3: CLI and web

```
T8 → T9 → T10 → T11
```

### Phase 4: Verifier fixes (iteration 1)

```
T12 → T13 → T14 → T15 → T16 → T17 → T18 → T19 → T20 → T21 → T22
```

---

## Task Breakdown

### Phase 1: Model and registration

#### T1: Projects schema and migration

**What**: migrations per design (projects, agent columns, project_cursors, room_members/grants CHECK rebuild incl. `project`), backfill of existing agents, sqlc queries, DTOs (Agent fields, Project), tygo.
**Where**: `internal/store/migrations/`
**Depends on**: None
**Reuses**: migration runner
**Requirement**: PROJ-01
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] Migration test from the previous schema with existing agents, members and grants; CHECKs accept `project`
**Tests**: integration
**Gate**: build
**Commit**: `feat(store): add projects and agent identity columns`

#### T2: Identity on connect

**What**: headers project/harness/machine; temporary naming `<project>-<harness>[-N]` + display `<Project> · <Harness> [#N]`; explicit `--name` fixed agents; connected_at; unarchive on connect; replaces the derived unique-suffix mode.
**Where**: `internal/agents/gateway.go`
**Depends on**: T1
**Reuses**: registry, cap
**Requirement**: ID-01, ID-02
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] Two sessions same project+harness → `#2`; explicit name newest-wins; concurrent connects get distinct N; cap respected
**Tests**: e2e
**Gate**: build
**Commit**: `feat(agents): name sessions after project and harness`

#### T3: Rename, promote, archive, list

**What**: PATCH agents display_name/fixed; projects GET/PATCH/sharing; archiving in the retention job; list hides archived by default; outbound/mentions use display name.
**Where**: `internal/agents/registry.go`
**Depends on**: T2
**Reuses**: retention job, access grants
**Requirement**: ID-04, ID-05, PROJ-02, PROJ-03, PROJ-05
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] Rename visible in timeline/members/outbound; promote stops archiving; 24 h archive with fake clock; reconnect unarchives; auth matrix
**Tests**: e2e
**Gate**: build
**Commit**: `feat(agents): rename, promote and archive agents`

#### T4: Project room members

**What**: `kind: "project"` members with use/owner check; member settings rows apply; list_rooms/room_info show projects.
**Where**: `internal/rooms/members.go`
**Depends on**: T3
**Reuses**: access
**Requirement**: PROJ-06
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] Owner/use can add; stranger 403; duplicate 409; settings PATCH works for project members
**Tests**: e2e
**Gate**: build
**Commit**: `feat(rooms): add projects as room members`

### Phase 2: Project delivery

#### T5: Plan and dispatcher for projects

**What**: Plan returns ProjectIDs; dispatcher pushes to the active session (latest connected_at online), dedupes with direct agent membership, skips the author's project for its own replies, trust per project owner.
**Where**: `internal/delivery/dispatcher.go`
**Depends on**: T4
**Reuses**: Wants, Trusted
**Requirement**: PROJ-07, PROJ-09, PROJ-10
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] Only the most recent online session gets it; active switches on disconnect; no echo to the same project; one delivery when also a direct member
**Tests**: integration
**Gate**: build
**Commit**: `feat(delivery): deliver project messages to the active session`

#### T6: Project cursors and backlog

**What**: acks advance project_cursors for project deliveries; backlog on connect merges project rooms (50 cap + notice).
**Where**: `internal/agents/backlog.go`
**Depends on**: T5
**Reuses**: gateway backlog
**Requirement**: PROJ-08
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] Offline post → next session of the project receives it; restart → no duplicate; cap/notice
**Tests**: e2e
**Gate**: build
**Commit**: `feat(agents): deliver project backlog to new sessions`

#### T7: Channel identity

**What**: channel/listen/tools-only send project/harness/machine headers; `--name`/`--project`; home-folder warning; launcher passes harness and flags.
**Where**: `internal/channel/run.go`
**Depends on**: T6
**Reuses**: launchers (v0.2.1)
**Requirement**: ID-03, CLI-01
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] Headers asserted; home dir → project `home` + one warning; flags override
**Tests**: e2e
**Gate**: build
**Commit**: `feat(channel): send project, harness and machine on connect`

### Phase 3: CLI and web

#### T8: partyline agents

**What**: `partyline agents` grouped by project with online state.
**Where**: `internal/cli/agents.go`
**Depends on**: T7
**Reuses**: config, API client
**Requirement**: CLI-02
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] Output matches API data against testkit
**Tests**: e2e
**Gate**: build
**Commit**: `feat(cli): list agents by project`

#### T9: Agents page by project

**What**: grouped list, machine/harness filters, archived toggle, rename agent, promote to fixed, rename project.
**Where**: `web/src/routes/_app/agents.tsx`
**Depends on**: T8
**Reuses**: queries, StatusDot
**Requirement**: PROJ-04, ID-04, ID-05, PROJ-05
**Tools**: MCP: NONE; Skill: `impeccable`
**Done when**:
- [x] Groups render with online-first order; filters; toggle; rename/promote call PATCH
**Tests**: unit
**Gate**: build
**Commit**: `feat(web): organize agents by project`

#### T10: Projects in rooms UI

**What**: member picker lists projects ("<Project> (any session)") and fixed agents first; member list shows project members with the active session.
**Where**: `web/src/components/member-picker.tsx`
**Depends on**: T9
**Reuses**: member-list
**Requirement**: PROJ-06, PROJ-07
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] Add project calls API with kind project; active session label updates on presence events
**Tests**: unit
**Gate**: build
**Commit**: `feat(web): add projects to rooms`

#### T11: Real UAT

**What**: local server + two real sessions of one project (Claude Code) + one in the home folder; verify names, warning, project room delivery across a restart; record `uat.md`.
**Where**: `.specs/features/agent-identity/uat.md`
**Depends on**: T10
**Reuses**: harness UAT rules (temp HOME, copied auth only)
**Requirement**: ID-01, ID-03, PROJ-07, PROJ-08
**Tools**: MCP: NONE; Skill: `orca-cli`
**Done when**:
- [x] Results recorded; failures fixed or documented
**Tests**: e2e
**Gate**: build
**Commit**: `docs(specs): record agent identity smoke test`

### Phase 4: Verifier fixes (iteration 1)

#### T12: Unarchive assertions that discriminate

**What**: the reconnect and promotion tests read `archived` from a list that hides archived agents (Verifier Fix 1, mutants M7 and M16 survived). Assert presence in the default list and read `?include_archived=true` with an explicit `archived: false`; add a promotion test that proves `archived_at` is cleared. Prove M7 (reconnect keeps `archived_at`) and M16 (promotion keeps it) now fail, in a scratch worktree.
**Where**: `internal/api/agent_manage_test.go`
**Depends on**: T11
**Reuses**: `listAgents`, `dialSession`, testkit clock
**Requirement**: PROJ-02, ID-05
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] Reconnect and promotion tests assert presence and `archived == false` from `include_archived=true`; M7 and M16 are killed (scratch worktree)
**Tests**: e2e
**Gate**: build
**Commit**: `test(agents): assert unarchive on reconnect and promotion`

#### T13: PROJ-09 covers the project member path only (AD-009)

**What**: amend PROJ-09: the project member never receives its own sessions' posts; a session of the same project that is itself a direct room member still receives them as that member. Record AD-009 in `STATE.md`, a matching sentence in `contract-notes.md`, a `PROJ-09 (AD-009)` comment at the plan rule, and make the tests cite the amended AC for both halves.
**Where**: `.specs/features/agent-identity/spec.md`
**Depends on**: T12
**Reuses**: `internal/api/project_delivery_test.go` (PROJ-09 test)
**Requirement**: PROJ-09
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] Spec, AD-009, contract notes and test comments agree; tests assert no echo through the project member and delivery to the direct member
**Tests**: integration
**Gate**: build
**Commit**: `docs(specs): narrow PROJ-09 to the project member path`

#### T14: Tools-only sessions of one project get distinct agents

**What**: send the session key (`PARTYLINE_SESSION`, else the existing fallback) in `X-Partyline-Session` from the channel, listen, tools-only and hook clients. The server keeps the key of a tools-only allocation on the agent (migration `0014`) and, when a session that names no agent sends a key, reuses that session's own agent, else the lowest free one, skipping offline temporary agents still held by another tools-only session (until they are archived). Without a key the old rule applies. Registrations share the allocation lock with the WebSocket. Document that tools-only sessions never become a project's active session (`docs/agents.md`, spec Assumptions).
**Where**: `internal/agents/identity.go`
**Depends on**: T13
**Reuses**: `freeName`, the gateway allocation lock, `channel.Identity` headers
**Requirement**: ID-01, PROJ-07
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] Two tools-only sessions of one project → two agents (distinct names and ids); the same session (register again, hook) → its agent; no key → the previous agent; concurrent keyed registrations get distinct N; clients send the header
**Tests**: e2e
**Gate**: build
**Commit**: `fix(agents): keep tools-only sessions of one project apart`

#### T15: Project rename refreshes the project's rooms live

**What**: `RenameProject` also publishes `MembersChanged` for every room that has the project as a `kind: "project"` member (the live hub keeps its viewer-only rule).
**Where**: `internal/agents/projects.go`
**Depends on**: T14
**Reuses**: `Registry.membersChanged`, live hub
**Requirement**: PROJ-05
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] A room viewer gets `members.changed` for a project-member room on rename and the member list shows the new name; a non-viewer gets nothing
**Tests**: e2e
**Gate**: build
**Commit**: `fix(agents): refresh project member rooms on project rename`

#### T16: Traceability and contract notes for the fixes

**What**: annotate harness-onboarding NAME-01 as superseded by agent-identity ID-01; record the T12-T15 contract (session key header, reuse rule, rename event) in `contract-notes.md`.
**Where**: `.specs/features/agent-identity/contract-notes.md`
**Depends on**: T15
**Reuses**: none
**Requirement**: ID-01
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] NAME-01 carries the superseded note; contract notes describe the fixes
**Tests**: none
**Gate**: build
**Commit**: `docs(specs): record agent identity fix iteration 1 contract`

#### T17: Recover the launcher variables a harness strips

**What**: Codex (and other harnesses) start MCP servers with a filtered environment, so a tools-only channel of a `partyline <harness>` session misses `PARTYLINE_SESSION` and keys itself by its parent pid while the hook uses the launcher's key: one session, two agents. When `PARTYLINE_SESSION` is missing, channel, listen and hook read the nearest ancestor process (up to 4 levels, `/proc/<pid>/environ`, unreadable ones skipped) that has it and take only `PARTYLINE_SESSION`, `PARTYLINE_HARNESS`, `PARTYLINE_PROJECT` and `PARTYLINE_AGENT` from it; their own variables win. Without `/proc` (macOS, Windows) or such an ancestor, the old rule applies (parent pid key for channel and listen, no key for the hook). Never logs the environment.
**Where**: `internal/cli/procenv.go`
**Depends on**: T16
**Reuses**: `stdio.getenv`, `resolveAgent`, hook identity
**Requirement**: ID-01
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] Environ parser and ancestor walk tested on a fake `/proc` (nearest wins, unreadable skipped, depth bound, only the four keys) and on a real Linux child process
- [x] Tools-only channel with a stripped environment takes key, harness and project from the ancestor; the hook of that session maps to the same single agent; no ancestor → parent pid key
**Tests**: e2e
**Gate**: build
**Commit**: `fix(cli): recover launcher variables a harness strips from its servers`

#### T18: Setup names the harness of tools-only servers

**What**: `partyline channel --tools-only --harness <id>` (codex, gemini, opencode, pi) sends that harness id, winning over the environment; without `--tools-only` only `omp` stays valid. `partyline setup` registers Codex and Gemini as `channel --tools-only --harness codex|gemini`. Entries written before (`channel --tools-only`) are still listed, updated and removed (ownership is by command, not arguments).
**Where**: `internal/harness/clis.go`
**Depends on**: T17
**Reuses**: `channelCmd` flag checks, harness registrar tests
**Requirement**: ID-01, HAR-01
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] Setup plan/apply run the new command lines; old entries still recognised by Entries/Remove
- [x] A tools-only channel with `--harness codex` and no environment registers harness codex; the flag wins over `PARTYLINE_HARNESS`; invalid combinations exit 2
**Tests**: e2e
**Gate**: build
**Commit**: `fix(harness): register tools-only servers with their harness`

#### T19: Session holds are scoped by machine

**What**: a session key matches a held agent only on the same machine (an unknown machine on either side matches), so parent pid keys of two machines of one owner never share an agent; holds stay scoped by owner.
**Where**: `internal/agents/identity.go`
**Depends on**: T18
**Reuses**: `freeName`, `registerSession`
**Requirement**: ID-01
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] Same key on two machines → two agents; same key and machine → the same agent; same key of another owner → that owner's own agent
**Tests**: e2e
**Gate**: build
**Commit**: `fix(agents): scope session holds by machine`

#### T20: Launcher session key is its process id

**What**: on Unix the launcher sets `PARTYLINE_SESSION` to its own pid, which `exec` keeps for the harness, so a tools-only server started by a harness that strips the environment and cannot be read through `/proc` (native Codex) derives the same key from its parent pid as the hook gets from the variable; Windows keeps the random id.
**Where**: `internal/cli/launcher.go`
**Depends on**: T19
**Reuses**: `execProgram`, channel parent-pid fallback
**Requirement**: ID-01
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] Every Unix launch passes its pid as `PARTYLINE_SESSION`; launches still get distinct keys; the random-id mutant fails the launcher test
**Tests**: unit
**Gate**: build
**Commit**: `fix(cli): use the launcher pid as the session key on unix`

#### T21: Tests for session holds (Verifier iteration 2)

**What**: API tests that kill the iteration-2 survivors: a tools-only session gets its held agent back when a lower number frees up (M17a); another owner's hold on the same key never steers a session (M17d); the server ignores an invalid `X-Partyline-Session`.
**Where**: `internal/api/agent_session_test.go`
**Depends on**: T20
**Reuses**: `registerSession`, `dialSession`, `waitOffline`
**Requirement**: ID-01
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] M17a and M17d fail the new tests in a scratch worktree; the invalid-key test passes only with the server-side filter
**Tests**: e2e
**Gate**: build
**Commit**: `test(agents): pin session hold lookup and owner scope`

#### T22: Test that a hold stays in its project and harness (Verifier iteration 3)

**What**: API test that kills M33: a session key holding `proj-codex` that registers for Gemini gets `proj-gemini`, for project `other` gets `other-codex`, and still gets `proj-codex` back.
**Where**: `internal/api/agent_session_test.go`
**Depends on**: T21
**Reuses**: `registerSession`
**Requirement**: ID-01
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] M33 (held lookup ignoring the project-harness family) fails the new test in a scratch worktree
**Tests**: e2e
**Gate**: build
**Commit**: `test(agents): keep a session hold within its project and harness`
