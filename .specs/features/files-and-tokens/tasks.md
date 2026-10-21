# Room Files and Token Hygiene Tasks

## Execution Protocol (MANDATORY -- do not skip)

Implement these tasks with the `tlc-spec-driven` skill: **activate it by name and follow its Execute flow and Critical Rules.** Do not search for skill files by filesystem path. The skill is the source of truth for the full flow (per-task cycle, sub-agent delegation, adequacy review, Verifier, discrimination sensor).

**If the skill cannot be activated, STOP and tell the user - do not proceed without it.**

---

**Design**: inline (Medium scope: one endpoint pair, two UI surfaces; follows existing patterns in `internal/api/rooms_handlers.go`, `internal/rooms`, `internal/users/tokens.go`, `internal/rooms/retention.go`, `web/src/routes/_app/tokens.tsx`, room side panel).
**Status**: Approved

---

## Test Coverage Matrix

> Guidelines: `AGENTS.md`, all `contract-notes.md`, `.specs/LESSONS.md`.

| Code Layer | Required Test Type | Coverage Expectation | Location Pattern | Run Command |
| --- | --- | --- | --- | --- |
| Services/queries | integration | Every AC + error paths | `internal/**/*_test.go` | `make test` |
| HTTP endpoints | e2e (testkit) | Happy + edge + error; auth matrix | `internal/api/*_test.go` | `make test` |
| Web | unit (Vitest) | Each visible AC incl. live update | `web/src/**/*.test.tsx` | `cd web && bun run test` |

## Gate Check Commands

| Gate Level | When to Use | Command |
| --- | --- | --- |
| Quick | Unit-only | `cd web && bun run test` |
| Full | Integration/e2e | `make test` |
| Build | Before every commit | `make check` |

---

## Execution Plan

### Phase 1: Backend

```
T1 → T2
```

### Phase 2: Web

```
T3 → T4
```

---

## Task Breakdown

### Phase 1: Backend

#### T1: Room attachments endpoint

**What**: query + service + `GET /api/v1/rooms/{id}/attachments` with kind filter, cursor and limits; DTO `RoomFile`; tygo regen.
**Where**: `internal/api/rooms_handlers.go`
**Depends on**: None
**Reuses**: rooms.Require, attachments queries
**Requirement**: FILES-01, FILES-02, FILES-03
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] Newest first, limit default 50 / max 100, cursor works; kind filter; 422 unknown kind; auth matrix (viewer 200, stranger 404)
**Tests**: e2e
**Gate**: build
**Commit**: `feat(api): list a room's files`

#### T2: Token hygiene backend

**What**: list hides revoked unless `include_revoked=true`; `DELETE /api/v1/tokens/{id}/purge` (revoked only, 409 otherwise, audit); retention job purges tokens revoked >30 days.
**Where**: `internal/users/tokens.go`
**Depends on**: T1
**Reuses**: retention job, audit
**Requirement**: TOKH-01, TOKH-02, TOKH-03
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] Default list omits revoked; purge rules; 29-day kept / 31-day purged (fake clock); purged secret → 401
**Tests**: e2e
**Gate**: build
**Commit**: `feat(api): hide, purge and expire revoked tokens`

### Phase 2: Web

#### T3: Files tab in the room

**What**: Files tab in the room side panel: list with thumbnails/icons, kind filter, open-in-timeline, live append, unavailable state, empty state; pt-BR + en.
**Where**: `web/src/components/room-files.tsx`
**Depends on**: T2
**Reuses**: message-item helpers (formatBytes, inline image rules), live handlers
**Requirement**: FILES-04, FILES-05, FILES-06
**Tools**: MCP: NONE; Skill: `impeccable`
**Done when**:
- [ ] Renders files; filter changes query; live room.message with attachments appends; unavailable rows have no link
**Tests**: unit
**Gate**: build
**Commit**: `feat(web): show a room's files`

#### T4: Tokens page hygiene

**What**: hide revoked by default, "Show revoked" toggle (localStorage), Delete on revoked rows with alert-dialog.
**Where**: `web/src/routes/_app/tokens.tsx`
**Depends on**: T3
**Reuses**: alert-dialog
**Requirement**: TOKH-04
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [ ] Default hides revoked; toggle shows; delete calls purge and removes the row
**Tests**: unit
**Gate**: build
**Commit**: `feat(web): tidy revoked tokens`
