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
- [ ] Results recorded; failures fixed or documented
**Tests**: e2e
**Gate**: build
**Commit**: `docs(specs): record agent identity smoke test`
