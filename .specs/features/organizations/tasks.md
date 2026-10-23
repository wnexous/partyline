# Organizations Tasks

## Execution Protocol (MANDATORY -- do not skip)

Implement these tasks with the `tlc-spec-driven` skill: **activate it by name and follow its Execute flow and Critical Rules.** Do not search for skill files by filesystem path. The skill is the source of truth for the full flow (per-task cycle, sub-agent delegation, adequacy review, Verifier, discrimination sensor).

**If the skill cannot be activated, STOP and tell the user - do not proceed without it.**

---

**Design**: `.specs/features/organizations/design.md`
**Status**: Draft

Decisions: AD-016 to AD-022. Revised after the owner's answers (2026-10-23):
- three permissions (`admin`, `member`, `guest`);
- deletion follows ownership;
- self-deletion of an account;
- machines become internal;
- chats per organization.

The owner then refined guests: a guest is the responsibility of its inviter, and the inviters form
a chain. That settled G1 and G2, so no question is open. T56-T58 were added with these answers and
are placed inside their phases.

---

## Test Coverage Matrix

> Generated from codebase, project guidelines, and spec - confirm before Execute. Guidelines found: `AGENTS.md` (tests derive from the spec's ACs; `make check` before every commit; no message text or secrets in logs; synthetic fixtures), `.specs/LESSONS.md` (confirmed L-017, L-018, L-029, L-042), `Makefile`, `scripts/check.sh`, `web/vite.config.ts`, `web/playwright.config.ts`, existing tests (`internal/api/authmatrix_test.go`, `internal/store/*_migration_test.go`, `web/src/routes/_app/connectors/connectors.test.tsx`).

| Code Layer | Required Test Type | Coverage Expectation | Location Pattern | Run Command |
| --- | --- | --- | --- | --- |
| Pure rules (`orgs.Chart`, slug rules) | unit | All branches; 1:1 to POS-03; every condition of the `manages` predicate has a case where only it excludes (L-042) | `internal/orgs/*_test.go` | `go test -count=1 ./internal/orgs/...` |
| Store, migrations, Go hooks | integration (real SQLite) | Every new table/column/index; v0.5 database before/after; rollback on hook failure | `internal/store/*_test.go` | `go test -count=1 ./internal/store/...` |
| Services (orgs, users deletion, agents placement, access, delivery trust, connectors, intelligences) | integration (real SQLite) | Every AC + listed edge case + error path; transactional checks (POS-02, OMEM-05) inside the changing transaction | `internal/**/*_test.go` | `make test` |
| HTTP/WS routes, channel, CLI | e2e (testkit, real app on httptest) | Every route: happy + edge + error; the org authorization matrix (admin, manager, member, outsider, instance admin) for every new or changed route; every new error code observed at its status (L-017); negative tests isolate the guard (L-018) | `internal/api/*_test.go`, `internal/channel/*_test.go`, `internal/cli/*_test.go` | `make test` |
| Web components/pages | unit (Vitest + Testing Library + vitest-axe) | Each visible AC through the route that passes the permission (L-007); a11y for new layouts | `web/src/**/*.test.{ts,tsx}` | `cd web && bun run test` |
| Web end-to-end | e2e (Playwright, real binary, desktop-1280 + mobile-360) | Org chart flow; 360 px without horizontal scroll (L-009) | `web/e2e/*.spec.ts` | `make e2e` |
| Docs | none | build gate only | `docs/*.md`, `README*.md` | `make check` |

## Gate Check Commands

> Generated from codebase - confirm before Execute.

| Gate Level | When to Use | Command |
| --- | --- | --- |
| Quick | While iterating on unit-only code | `go test -count=1 ./internal/<pkg>/...` or `cd web && bun run test` |
| Full | While iterating on integration/e2e code | `make test` |
| Build | Before **every** commit (AGENTS.md, L-029) | `make check` |
| E2E | T54 and before UAT | `make e2e` |

Every task's commit gate is `make check`. Run `make generate` after touching SQL or `internal/api/dto`.

---

## Execution Plan

The phases are ordered. Tasks within a phase run in order on one worker. **Parallel groups**: the
phases inside one group depend only on earlier groups, never on each other. Each phase runs in its
own worktree and merges back before the next group starts.

| Group | Phases | Starts after |
| --- | --- | --- |
| G0 | Phase 1 → Phase 2 (sequential) | — |
| **GA** (parallel) | Phase 3, Phase 4, Phase 5, Phase 6 | G0 merged |
| **GB** (parallel) | Phase 7, and (Phase 8 → Phase 9) | GA merged |
| G3 | Phase 10 | GB merged |

**Merge rule for parallel worktrees.** Never hand-merge generated files (`internal/store/db/*`,
`web/src/lib/api/types.gen.ts`). After merging, run `make generate`, commit the result with the
merge, then run `make check`.

Sharing routes and DTOs stay in place, but inert, until Phase 10. That way the web build never
references a removed type while GA and GB run.

### Phase 1: Store and chart core

```
T1 → T2
T1 → T4
T3 → T4
```

### Phase 2: Organization services

```
T5 → T6 → T7 → T8
T5 → T9 → T10
T7 → T11
T10 → T11
```

### Phase 3: Organization API (group GA)

```
T12 → T13
T12 → T14
T12 → T15
T12 → T16
T12 → T17
```

### Phase 4: AI placement and identity lifecycle (group GA)

```
T18 → T19
```

T20 and T21 have no dependency inside the phase.

### Phase 5: Resource authorization on organizations (group GA)

```
T22 → T23
T26 → T27
T25 → T28 → T57
```

T56 has no dependency inside the phase.

### Phase 6: Trust and live updates (group GA)

```
T29 → T30
```

### Phase 7: CLI and channel (group GB)

```
T33 → T34
```

T58 has no dependency inside the phase.

### Phase 8: Web organization pages (group GB)

```
T39 → T40
T39 → T41
T39 → T42
T39 → T43
T39 → T44
```

### Phase 9: Web resources on organizations (group GB, after Phase 8)

```
T45 → T50
T47 → T50
```

### Phase 10: Retirement, upgrade proof and docs

```
T51 → T52 → T53
T51 → T55
```

---

## Task Breakdown

### Phase 1: Store and chart core

#### T1: Migration 0019 schema and org queries [core]

**What**: `0019_organizations.sql` with:
- the tables `organizations`, `positions`, `org_members` and `org_invitations`;
- the columns `agents.org_id`/`creator_iid`, `rooms.org_id`/`creator_iid`, `connectors.org_id` and `users.default_org_id`;
- the indexes;
- the sqlc queries in a new `internal/store/queries/orgs.sql`.

All of it exactly as in the design's Data Model.
**Where**: `internal/store/migrations/0019_organizations.sql`
**Depends on**: None
**Reuses**: AD-012 `ADD COLUMN` pattern, `openMigratedBefore`
**Requirement**: ORG-02, OUPG-01
**Done when**:
- [x] Store test:
  - every table, column and index exists;
  - `UNIQUE (org_id, iid)` refuses a second membership;
  - `permission` accepts `admin`, `member` and `guest` and refuses anything else;
  - `org_members.inviter_iid` becomes NULL when the inviter's intelligence is deleted;
  - positions: a name is unique per org case-insensitively (`Lead` vs `lead`), and two orgs can each have "Admin";
  - deleting an org that an agent references fails, and an empty org deletes with cascading positions, members and invitations
- [x] `make generate` run
- [x] Gate check passes: `make check`
**Tests**: integration
**Gate**: build
**Commit**: `feat(store): add organizations, positions and memberships`

#### T2: Upgrade hook 0019 (OUPG-01) [core]

**What**: The Go hook `0019_organizations` (raw SQL), registered in `hooks`. It runs design steps 1-7:
- personal orgs with the root "Admin" and the admin membership;
- AIs with their home org and creator;
- groups and connections moved;
- user grants turned into memberships;
- `post`/`manage` room grants turned into room members;
- group members joined to the org;
- `DELETE FROM grants`.
**Where**: `internal/store/organizations_backfill.go`
**Depends on**: T1
**Reuses**: `intelligences_backfill.go` structure, `handles` slug rules, `store.NewID`
**Requirement**: OUPG-01, ORG-02
**Done when**:
- [x] Integration test on a v0.5 database (`v03_fixture.sql` migrated to 0018) plus seeded grants:
  - each user has one org, is admin of it, holds "Admin", and that org is their default;
  - every AI, group and connection has the owner's org; DMs have none;
  - every AI's creator is its owner;
  - a `use` grantee is a `member` with no position;
  - a `view` room grantee is an org member but not a room member, while a `post` grantee is both;
  - a `*` grant leaves no membership;
  - a group member from another user is a member of the group's org;
  - message, attachment and cursor counts are unchanged;
  - slugs stay unique for usernames `bob` and `Bob_`
- [x] Injected hook failure: no org table or column remains (MIG-05)
- [x] Gate check passes: `make check`
**Tests**: integration
**Gate**: build
**Commit**: `feat(store): move every user's resources into a personal organization`

#### T3: Chart rules [core]

**What**: Package `internal/orgs` with `Chart`, `Member`, `Position`, and:
- `Manages(a, b)`;
- `Above(pa, pb)`, meaning strictly above;
- `ManagersOf(b)`, nearest first;
- `ManagedBy(a)`;
- `IsAdmin(iid)`;
- the guest rule of `Manages`: `a` is `b`'s inviter or manages `b`'s inviter, recursively up the inviter chain (spec "Guests");
- `CanDelete(a, b)`: `a` is an admin, or `b` is a guest and `Manages(a, b)` (POS-05);
- `HasCycle(positionID, newParentID)`.

All of them are pure, with no I/O.
**Where**: `internal/orgs/chart.go`
**Depends on**: None
**Reuses**: none
**Requirement**: POS-03, POS-02, POS-05
**Done when**:
- [x] Table tests, one per predicate condition (L-042):
  - above vs. same level vs. below;
  - two levels above;
  - B without a position: A is the creator; A is above the creator; A is a peer of the creator; a transitive chain of two positionless creators; a creator who is not a member;
  - A not a member;
  - a == b;
  - a root holder has no manager;
  - a creator cycle terminates
- [x] Guest chain, one case per condition (L-042):
  - the inviter manages its guest;
  - the inviter's inviter manages a guest-of-a-guest;
  - the position manager of an inviter manages the inviter's guest;
  - an inviter who left the org breaks the chain;
  - an inviter cycle terminates;
  - a peer of the inviter does not manage the guest
- [x] `CanDelete`, one case per condition: an admin deletes a member it does not manage; an inviter deletes its guest; a manager cannot delete a member below; a peer cannot delete a guest it did not invite; nobody deletes itself
- [x] `HasCycle`: self-parent, a descendant as parent, and a valid move
- [x] `ManagersOf` order is nearest first
- [x] Gate check passes: `make check`
**Tests**: unit
**Gate**: build
**Commit**: `feat(orgs): compute who manages whom in an org chart`

#### T4: Chart loader and Authz facade [core]

**What**: In `internal/orgs`:
- `Load(ctx, q, orgID)` (two queries);
- `Authz` with `Chart`, `Member`, `RequireMember`, `RequireAdmin`, `Manages`, `ManagesAnywhere`, `CanDelete`, `Trusts`, `SharesOrg` and `Session()`, the per-operation memo;
- `authn.Principal.IID()`;
- `events.OrgChanged{OrgID}`.

Wire it into the app's dependencies.
**Where**: `internal/orgs/authz.go`
**Depends on**: T1, T3
**Reuses**: `access.ErrNotFound`/`ErrForbidden`, `store.R()`
**Requirement**: POS-03, POS-10, CHART-02
**Done when**:
- [x] Integration tests:
  - `RequireAdmin` → `ErrNotFound` for an outsider and `ErrForbidden` for a member;
  - `ManagesAnywhere` is true through a second shared org only;
  - `Trusts` uses the AI's home org, not another shared org where the author is above it;
  - `Session` loads each org's chart once (counted through a test hook)
- [x] Gate check passes: `make check`
**Tests**: integration
**Gate**: build
**Commit**: `feat(orgs): authorize through organization membership and the chart`

### Phase 2: Organization services

#### T5: Organization service [core]

**What**: `orgs.Service`:
- `Create`: name 1-100 code points and a derived slug; the creator is `admin` holding the root "Admin" (ORG-01);
- `Get`, `ListMine`, `Update` (name, slug, avatar) and `Delete`, which refuses with `ErrNotEmpty` and counts while AIs, connections or groups remain (ORG-06);
- audit `org.create`, `org.update` and `org.delete`;
- `OrgChanged` published after commit.
**Where**: `internal/orgs/service.go`
**Depends on**: T4
**Reuses**: `validation.Check`, `audit.Log`, `avatars`
**Requirement**: ORG-01, ORG-06
**Done when**:
- [x] Integration tests:
  - names of 0/101 chars are refused; 1/100 and 100 multi-byte code points are accepted (L-023);
  - a duplicate slug gets a suffix;
  - delete refuses with one AI, with one connection and with one group, each alone (L-018);
  - an empty org deletes
- [x] Gate check passes: `make check`
**Tests**: integration
**Gate**: build
**Commit**: `feat(orgs): create, rename and delete organizations`

#### T6: Positions service [core]

**What**: `CreatePosition` (name 1-60, parent of the same org), `UpdatePosition` (rename, reparent; the cycle check runs on a chart loaded in the same transaction → `ErrCycle`), and `DeletePosition`. Delete moves holders and children to the parent, or to none/root (POS-09). Audit `org.position_*`.
**Where**: `internal/orgs/positions.go`
**Depends on**: T5
**Reuses**: `Chart.HasCycle`, `Load` with a tx `*db.Queries`
**Requirement**: POS-01, POS-02, POS-09
**Done when**:
- [x] Integration tests:
  - name bounds of 1/60/61;
  - a parent from another org is refused;
  - A→B→C with A's parent set to C → `ErrCycle` and nothing changes;
  - deleting a middle position moves its holders and children to its parent;
  - deleting a root leaves its holders without a position and makes its children roots
- [x] Gate check passes: `make check`
**Tests**: integration
**Gate**: build
**Commit**: `feat(orgs): edit the org chart's positions`

#### T7: Membership service [core]

**What**:
- `SetPermission`: `ErrLastAdmin` when no admin would remain.
- `SetPosition`: an admin may set any position or none. A manager may move a subordinate between positions strictly below its own (POS-04). Anything else, including a manager clearing a position, is `ErrForbidden`.
- `Remove` (used by delete-in-org for persons and AIs of other orgs, T21) and `Leave`:
  - remove the member from the org's groups and from its position (OMEM-06);
  - `ErrHomeOrganization` when an AI leaves its home org;
  - `ErrLastAdmin` for the last admin;
  - clear the operator of connections the AI operated in that org (OMEM-08 guard).
- `AddMember`, a tx helper used by invitations and placement.
- Audit `org.member_*` and publish `OrgChanged`.
**Where**: `internal/orgs/members.go`
**Depends on**: T6
**Reuses**: `rooms` member deletion queries, `Chart.Manages`
**Requirement**: OMEM-03, OMEM-04, OMEM-05, OMEM-06, OMEM-08, POS-04
**Done when**:
- [x] Integration tests:
  - an identity in two orgs with different permissions and positions;
  - demoting, removing and the leaving of the last admin → `ErrLastAdmin`, each case alone;
  - with two admins each works;
  - a manager moves a subordinate below itself; a move to its own level → forbidden; clearing the position → forbidden for the manager and allowed for an admin; a peer → forbidden;
  - removal drops only that org's group memberships and keeps the org's AIs;
  - an AI leaving its home org → `ErrHomeOrganization`
- [x] Gate check passes: `make check`
**Tests**: integration
**Gate**: build
**Commit**: `feat(orgs): manage members, permissions and positions`

#### T8: Invitations service [core]

**What**:
- `Invite(org, handle, permission, position)`: users and AIs only.
  - An admin may give any permission (`admin`, `member`, `guest`) and a position.
  - Any other member or guest may only invite as `guest`, without a position; anything else is `ErrForbidden` (OMEM-04).
  - The inviter is recorded, and on accept it becomes the guest's `inviter_iid` (OMEM-01). `ErrAlreadyMember`; re-inviting updates the pending row; at most 30 per hour per org, counted from audit entries (`ErrRateLimited`).
- `ListForOrg` and `ListMine`: mine plus those of the AIs I manage in their home org.
- `Accept`, which calls `AddMember` (OMEM-02); `Decline`; `Revoke`.
- Expiry: 14 days → `ErrGone`. The retention ticker deletes expired rows.
- Audit for each step.
**Where**: `internal/orgs/invitations.go`
**Depends on**: T7
**Reuses**: `rooms/retention.go` ticker, `handles` lookup
**Requirement**: OMEM-01, OMEM-02, OMEM-04, OMEM-07
**Done when**:
- [x] Integration tests with a fake clock:
  - accept at 14 d − 1 ms works; at 14 d → `ErrGone`; the ticker deletes the row (L-016);
  - the 31st invitation within an hour → rate limited, and the limit still holds after a restart;
  - an AI invitee is accepted by its manager and refused for a non-manager;
  - a person handle → not found;
  - accept gives the invitation's permission and position, and for a guest its inviter;
  - a member and a guest each invite a guest; a guest invited by a guest records the chain;
  - a member inviting as `member`, or a guest inviting with a position → forbidden
- [x] Gate check passes: `make check`
**Tests**: integration
**Gate**: build
**Commit**: `feat(orgs): invite identities into an organization`

#### T9: Personal organization for new accounts and the default org [small]

**What**: `insertAccount` creates the personal org in the account's transaction and sets `default_org_id` (AD-021). Add `orgs.DefaultFor(ctx, q, iid)`:
- for a user: `default_org_id`, else the oldest admin membership, else the oldest membership;
- for an AI: its home org.

Add `orgs.SetDefault(user, org)`, which requires membership.
**Where**: `internal/users/invites.go`
**Depends on**: T5
**Reuses**: `orgs.CreatePersonal` tx helper (written here)
**Requirement**: ORG-04
**Done when**:
- [x] Integration tests:
  - a new account has an org where it is admin and "Admin", and it is the default;
  - `DefaultFor` with the default cleared falls back to the oldest admin membership, then to the oldest membership (L-041: a case where the fallback alone picks differently);
  - `SetDefault` to a non-member org → forbidden
- [x] Gate check passes: `make check`
**Tests**: integration
**Gate**: build
**Commit**: `feat(users): give every new account a personal organization`

#### T10: Default placement of new AIs [core]

**What**: `createFolderAgent` sets:
- `org_id = DefaultFor(host)`;
- `creator_iid = host`;
- an `org_members` row (`member`, because a user started it; no position);

all in the creation transaction. The headers, and `guest` for AI-started AIs, come in T18.
**Where**: `internal/agents/folder.go`
**Depends on**: T9
**Reuses**: `orgs.AddMember`
**Requirement**: ORG-04, ORG-02
**Done when**:
- [x] e2e: a first session in a folder creates an AI in the host's default org as `member` with no position, created by the host; a second session in the same folder does not add a membership
- [x] Gate check passes: `make check`
**Tests**: e2e
**Gate**: build
**Commit**: `feat(agents): place new AIs in the creator's default organization`

#### T11: Test harness for organizations [small]

**What**: `testkit` helpers:
- `c.PersonalOrg()`;
- `env.AddPosition(org, name, parent)`;
- `env.AddMember(org, iid, permission, position)`;
- `env.SetCreator(agent, iid)`.

Plus an `OrgRoles` builder returning clients for:
- admin;
- manager (holds a position above the target's);
- member;
- guest, plus its inviter;
- peer;
- outsider;
- instance admin without membership.

Phases 3-6 reuse these for their authorization matrices.
**Where**: `internal/testkit/orgs.go`
**Depends on**: T7, T10
**Reuses**: `testkit.Env` app handle, `authmatrix_test.go` style
**Requirement**: OMEM-04, POS-05, CHART-02
**Done when**:
- [x] `testkit_test.go` case: `OrgRoles` yields the stated permissions and positions, and `Manages(manager, target)` is true while `Manages(peer, target)` is false
- [x] Gate check passes: `make check`
**Tests**: integration
**Gate**: build
**Commit**: `test(testkit): add organization roles for authorization tests`

### Phase 3: Organization API (group GA)

#### T12: Organization routes and DTOs [core]

**What**: `GET|POST /api/v1/orgs`, `GET|PATCH|DELETE /api/v1/orgs/{org}` and `PUT|DELETE /orgs/{org}/avatar`, with the DTOs `Organization`, `OrgMember` and `Position` (tygo). Add the error codes `organization_not_empty` (409) and `last_admin` (409), plus their `httpx` mapping.
**Where**: `internal/api/orgs_handlers.go`
**Depends on**: None
**Reuses**: `authed`, `avatar_handlers.go` pattern
**Requirement**: ORG-01, ORG-06, OMEM-04, CHART-02
**Done when**:
- [ ] e2e: the design's "Organizations" matrix for every role, using `OrgRoles`
- [ ] 409 `organization_not_empty` observed with counts (L-017)
- [ ] An agent token → 403 on `POST /orgs`
- [ ] `make generate` run
- [ ] Gate check passes: `make check`
**Tests**: e2e
**Gate**: build
**Commit**: `feat(api): serve organizations`

#### T13: Position routes [small]

**What**: `POST /orgs/{org}/positions` and `PATCH|DELETE /orgs/{org}/positions/{id}`, with `position_cycle` (409).
**Where**: `internal/api/positions_handlers.go`
**Depends on**: T12
**Reuses**: T6 service
**Requirement**: POS-01, POS-02, POS-09, OMEM-04
**Done when**:
- [ ] e2e:
  - admin 201/200/204; member 403; outsider 404;
  - 409 `position_cycle` observed;
  - 422 for a name of 61 chars and for a parent from another org;
  - 409 `conflict` for a duplicate name
- [ ] Gate check passes: `make check`
**Tests**: e2e
**Gate**: build
**Commit**: `feat(api): edit an organization's positions`

#### T14: Member routes [core]

**What**: `GET /orgs/{org}/members` and `PATCH /orgs/{org}/members/{iid}` (permission, position_id). `DELETE` on the same path belongs to T21.
**Where**: `internal/api/org_members_handlers.go`
**Depends on**: T12
**Reuses**: T7 service, `OrgRoles`
**Requirement**: OMEM-03, OMEM-04, OMEM-05, POS-04
**Done when**:
- [ ] e2e: the GET and PATCH rows of the "Members" matrix (Out/M/G/Mgr/A/Self)
- [ ] The manager's move to its own level → 403, to none → 403, and to a lower level → 200
- [ ] Demoting the last admin → 409 `last_admin` observed
- [ ] Setting `guest` on a member → 200 and the list shows it
- [ ] Gate check passes: `make check`
**Tests**: e2e
**Gate**: build
**Commit**: `feat(api): list members and change their permission and position`

#### T15: Invitation routes [core]

**What**: `POST|GET /orgs/{org}/invitations`, `DELETE /orgs/{org}/invitations/{id}`, `GET /api/v1/invitations` and `POST /api/v1/invitations/{id}/accept|decline`. Add `already_member` (409); 410 `gone` and 429 `rate_limited` already exist. Add the live event `invitation_received` for the invitee and the managers of an invitee AI.
**Where**: `internal/api/org_invitations_handlers.go`
**Depends on**: T12
**Reuses**: T8 service, `live` event fan-out
**Requirement**: OMEM-01, OMEM-02, OMEM-04, OMEM-07
**Done when**:
- [ ] e2e: the "Invitations" matrix (M and G invite guests only: 201 as guest, 403 as member or with a position)
- [ ] Bob is invited, accepts, and creates a group in acme; Bob's invite attempt → 403 (spec Independent Test)
- [ ] 409 `already_member`, 429 and 410 observed
- [ ] Inviting again updates the permission of the pending invitation
- [ ] Gate check passes: `make check`
**Tests**: e2e
**Gate**: build
**Commit**: `feat(api): invite identities to an organization`

#### T16: Chart route [core]

**What**: `GET /orgs/{org}/chart` returns `OrgChart`:
- positions with their holders: avatar, name, handle, kind, online, admin;
- members without a position, with `under`: a guest's inviter, else the creator when the creator is a member; otherwise in `unassigned` (CHART-02);
- per member, the permission (admin and guest badges), and for the viewer `can_delete` (POS-05), `delete_effect` (`delete` for an AI of this org, `remove` otherwise) and `can_move`;
- `viewer.admin`.
**Where**: `internal/api/org_chart_handlers.go`
**Depends on**: T12
**Reuses**: `Chart.ManagedBy`, presence `Online`
**Requirement**: CHART-01, CHART-02, CHART-03
**Done when**:
- [ ] e2e on the spec's example chart (Admin › Director › Manager › Lead developer › Worktrees, plus Sentinel):
  - the tree shape;
  - three workers positioned in Worktrees, and one positionless worker under Lead;
  - flags for the admin (delete on everyone but itself), for Lead (workers started by Lead are guests: delete true; Sentinel, a member: false; move true on both if below) and for a member (all false);
  - `delete_effect` is `remove` for a person and for an AI of another org;
  - a guest gets 200, and a guest invited by Bob appears under Bob;
  - outsider 404
- [ ] Gate check passes: `make check`
**Tests**: e2e
**Gate**: build
**Commit**: `feat(api): serve the org chart`

#### T17: Default organization route [small]

**What**: `PUT /api/v1/me/default-org` `{org_id}`, and `me` gains `default_org_id`.
**Where**: `internal/api/auth_handlers.go`
**Depends on**: T12
**Reuses**: `orgs.SetDefault` (T9)
**Requirement**: ORG-04
**Done when**:
- [ ] e2e: setting the default to a member org → 200, and the next new AI lands there; a non-member org → 403
- [ ] Gate check passes: `make check`
**Tests**: e2e
**Gate**: build
**Commit**: `feat(api): let users choose their default organization`

### Phase 4: AI placement and identity lifecycle (group GA)

#### T18: Placement headers on creation [core]

**What**: `FolderIDFromHeader` reads `X-Partyline-Org`, `X-Partyline-Position` (PathUnescape) and `X-Partyline-Parent`, and the dto header constants are added. `agents.place` resolves for a new AI:
- the creator (parent the host may act for, else the host; POS-08);
- the home org (`Org`, else the creator's default);
- ORG-05 → 403;
- the position check (POS-07 → 403);
- the permission: `guest` with `inviter_iid` = the creator when the creator is an AI (a valid `--parent`), `member` otherwise (ORG-04);
- `position_unknown` (422) and `organization_required` (422).
**Where**: `internal/agents/placement.go`
**Depends on**: None
**Reuses**: `linkOrCreate` transaction, `orgs.DefaultFor`, `Chart.Above`
**Requirement**: ORG-04, ORG-05, POS-07, POS-08
**Done when**:
- [x] e2e on the live WebSocket and on a tools-only call (L-034: one test each):
  - `--org acme` → acme;
  - a parent AI hosted by the same user → creator = parent, home = the parent's home, permission `guest`;
  - a parent hosted by another user → creator = host, permission `member`;
  - a foreign org → 403 and no AI row;
  - a position below the creator → held; at the same level → 403; for an admin creator → held;
  - unknown position → 422 `position_unknown`; unknown org → 422 `organization_required`;
  - no headers → the defaults (v0.5 compatibility)
- [x] Gate check passes: `make check`
**Tests**: e2e
**Gate**: build
**Commit**: `feat(agents): place new AIs with --org, --position and --parent`

#### T19: Start rules for an existing AI [small]

**What**: For an AI that exists:
- `Org` naming another org → 409 `organization_mismatch`, with the home org named in details;
- `Parent` is ignored;
- a different `Position` moves the AI when the starter manages it or is admin, plus the POS-07 check; otherwise 403.
**Where**: `internal/agents/placement.go`
**Depends on**: T18
**Reuses**: `orgs.SetPosition` rules
**Requirement**: ORG-04, POS-07
**Done when**:
- [x] e2e:
  - mismatch → 409, observed (L-017);
  - the same `--org` → OK;
  - a later `--position` within the rule moves the AI; outside it → 403 and the position is unchanged;
  - `--parent` on a later start does not change the creator
- [x] Gate check passes: `make check`
**Tests**: e2e
**Gate**: build
**Commit**: `feat(agents): keep an existing AI's organization on later starts`

#### T20: Self-deletion of an account [core]

**What**: `users.DeleteAccount(ctx, userID, password)` and the route `DELETE /api/v1/me` `{password}`. One transaction, in this order:
1. wrong password → 422 field `password`;
2. `ErrLastInstanceAdmin` (409 `last_instance_admin`);
3. the heir becomes admin where the user was the last admin (OUPG-02);
4. `rooms.owner_id` and `connectors.owner_id` are re-pointed to the heir;
5. the hosted AIs stay: `owner_id` → heir, `folder` → `''`;
6. the user row is deleted.

After commit: hosted AIs' sessions are kicked (4001), `SessionsRevoked`, `OrgChanged` and audit `user.delete`. MIG-02 adoption then only considers unlinked AIs whose `creator_iid` is the host.
**Where**: `internal/users/delete.go`
**Depends on**: None
**Reuses**: `ChangePassword` password check, `presence.Kick`, `events`
**Requirement**: POS-06, OUPG-02
**Done when**:
- [ ] e2e:
  - the user's sessions and tokens fail authentication (401);
  - for every table with a user or intelligence column, the deleted id has no row left;
  - a group and a connection the user created survive in their org;
  - the user's hosted AIs are still members of their orgs, with their positions, offline and unlinked, and the heir's sessions never adopt them;
  - the last admin's org gets the instance admin as admin, while an org with a second admin is unchanged;
  - a wrong password → 422 and nothing changes;
  - the last instance admin → 409 `last_instance_admin` observed
- [ ] Gate check passes: `make check`
**Tests**: e2e
**Gate**: build
**Commit**: `feat(users): let users delete their own account`

#### T21: Delete in an organization and end a session [core]

**What**:
- `DELETE /orgs/{org}/members/{iid}`: `Authz.CanDelete` (POS-05), then by ownership (POS-06). An AI whose home is the org goes through `Registry.Delete`; anyone else goes through `orgs.Remove` (OMEM-06). Self is leave (409 `last_admin`, 409 `home_organization`).
- `DELETE /agents/{id}`: the same rule in the AI's home org. `Registry.Delete`'s check becomes `CanDelete`; this task owns Delete, and T24 does not touch it.
- `DELETE /agents/{id}/session`: a manager; `Kick` with 4002 and the reason "ended by @handle".
- Add `home_organization` (409).
**Where**: `internal/api/org_members_handlers.go`
**Depends on**: None
**Reuses**: `presence.Kick`, `orgs.Remove`, `OrgRoles`
**Requirement**: POS-04, POS-05, POS-06, OMEM-06, OMEM-08
**Done when**:
- [ ] e2e, Lead developer starts three workers with `--parent @lead --position Worktrees` (guests):
  - Lead deletes them for good, while Sentinel (not above them) gets 403 (spec Independent Test);
  - a manager deleting a `member` below → 403, while an admin → 204 (L-018);
  - Bob (member) invites guest Carol, who invites guest Dave: Bob deletes Dave → 204; Bob's position manager deletes Carol → 204; a peer of Bob → 403;
  - an admin deleting a person → the person is removed from the org only: still a member elsewhere and able to log in;
  - an admin deleting an AI of another org → removed only, and the AI still exists in its home org;
  - a deleted member is out of the org's groups and its position, while the org's AIs stay (OMEM-06);
  - the operator AI deleted → connection status "choose the AI that operates this connection" (OMEM-08);
  - a manager ends a live session, and the socket closes with 4002;
  - an AI leaving its home org → 409 `home_organization` observed; the last admin leaving → 409 `last_admin`;
  - an invisible target → 404
- [ ] Gate check passes: `make check`
**Tests**: e2e
**Gate**: build
**Commit**: `feat(api): delete identities in an organization and end sessions`

### Phase 5: Resource authorization on organizations (group GA)

#### T22: Room levels from organizations [core]

**What**: `access.UserRoomLevel` and `RoomLevel` are computed on the org model:
- group: members post; the creator and the org's admins manage;
- DM: participants post; managers of an AI participant in its home org view.

`GrantedRoomLevels`/`ListRoomsForUser` stop reading grants. Rewrite the grant-based cases in `access_test.go`, `rooms_handlers_test.go`, `room_membership_access_test.go`, `activity_test.go`, `message_authors_test.go` and `room_info_test.go` to use org membership.
**Where**: `internal/access/access.go`
**Depends on**: None
**Reuses**: `orgs.Authz`, `OrgRoles`
**Requirement**: ORG-02, OMEM-06
**Done when**:
- [ ] e2e: the "Groups and DMs" matrix:
  - an org admin who is not a member manages a group;
  - a member who is not in the room → 404;
  - the manager of an AI views its DM, a non-manager org member → 404;
  - an instance admin → 404
- [ ] A grant row (still writable) gives nothing
- [ ] Gate check passes: `make check`
**Tests**: e2e
**Gate**: build
**Commit**: `feat(access): derive room access from organizations`

#### T23: Groups belong to an organization [core]

**What**: `POST /api/v1/groups` takes `org_id`; a `member` or `admin` creates; a non-member and a `guest` get 403 (ORG-03, OMEM-04). The agent's `POST /agent/groups` takes an optional `org`, defaulting to the AI's home org. `rooms.org_id` and `creator_iid` are set. Member adds to a group require membership of its org (`members_refused`, reason `not_org_member`). The AI acts as itself (`IID()`).
**Where**: `internal/conversations/groups.go`
**Depends on**: T22
**Reuses**: `rooms.CreateWithMembers`, `MembersRefusedError`
**Requirement**: ORG-02, ORG-03, OMEM-04
**Done when**:
- [ ] e2e:
  - a user group in acme → 201 with `org_id`; in a foreign org → 403;
  - a guest user and a guest AI each → 403, while a guest added to a group posts in it and can DM any member of the org;
  - an AI's `create_group` without `org` → its home org; with an org where it is not a member → 403;
  - adding a non-member → 422 `members_refused`/`not_org_member`;
  - rewrite `groups_test.go` and `conversation_tools_test.go`
- [ ] Gate check passes: `make check`
**Tests**: e2e
**Gate**: build
**Commit**: `feat(conversations): create groups inside an organization`

#### T24: AI administration on organizations [core]

**What**: `agents.Registry`:
- `List`: hosted AIs plus the AIs of my orgs;
- `Get`: visible when sharing an org, or the host;
- `Update`: a manager;
- merge: a manager of both, with a shared home org.

Merge moves org memberships (B wins) and re-points `creator_iid` from A to B. `Delete` is untouched (T21). Rewrite `agents_handlers_test.go` and `merge_test.go`.
**Where**: `internal/agents/registry.go`
**Depends on**: None
**Reuses**: `orgs.Authz`, `conversations/merge.go`
**Requirement**: POS-04, POS-05
**Done when**:
- [ ] e2e: the "Intelligences" matrix rows for `/agents*`
- [ ] Merge across home orgs → 403
- [ ] After a merge, A's created AIs have B as their creator
- [ ] Gate check passes: `make check`
**Tests**: e2e
**Gate**: build
**Commit**: `feat(agents): administer AIs through the org chart`

#### T25: Profiles on organizations [core]

**What**: In `intelligences.Service`:
- `CanSee`: users always; AIs when sharing an org or hosted; people per the design table.
- `CanEdit`: self or manager anywhere.

`Profile` gains memberships (org, permission, position), each with the viewer's `can_delete` and `delete_effect`, plus `can_edit` and `can_end_session`. The `machine`/`machine_label` fields go (AD-022). Rewrite `intelligences_handlers_test.go` and `service_test.go`.
**Where**: `internal/intelligences/service.go`
**Depends on**: None
**Reuses**: `orgs.Authz`
**Requirement**: POS-04, POS-05, OMEM-03
**Done when**:
- [ ] e2e:
  - a manager edits a subordinate's profile (person and AI) → 200;
  - a peer → 403; an outsider viewing an AI → 404;
  - the profile lists two memberships for an identity in two orgs
- [ ] Gate check passes: `make check`
**Tests**: e2e
**Gate**: build
**Commit**: `feat(intelligences): see and edit profiles through organizations`

#### T26: Connections belong to an organization [core]

**What**: In `connectors.Service`:
- `Create` takes `org_id` (admin only, 403 otherwise; ORG-03);
- `List`/`Get`/chats: org members;
- `Update`/`Delete`/enable: admins;
- `operator()`: an AI whose home org is the connection's (ORG-07).

Rewrite `connectors_handlers_test.go` and `connector_operator_test.go`.
**Where**: `internal/connectors/service.go`
**Depends on**: None
**Reuses**: `orgs.Authz`
**Requirement**: ORG-03, ORG-07, OMEM-04
**Done when**:
- [ ] e2e: the "Connections" matrix
- [ ] A member creating → 403; an outsider viewing → 404
- [ ] An operator from another org → 422 `operator_id`, from the same org → 200
- [ ] Gate check passes: `make check`
**Tests**: e2e
**Gate**: build
**Commit**: `feat(connectors): own connections by organization`

#### T27: Pairing and people on organizations [small]

**What**: A pairing code is issued for a connection of an org the user is a member of; for webhook kinds, admins only (AD-008 amended). People are visible to members of the connection's org (replaces `CanUseConnector`). Rewrite `identities_handlers_test.go`, `people_test.go` and `hooks_handlers_test.go`.
**Where**: `internal/intelligences/people.go`
**Depends on**: T26
**Reuses**: `orgs.Authz`
**Requirement**: ORG-02, ORG-07
**Done when**:
- [ ] e2e:
  - a member pairs through a Telegram connection of its org; through a webhook connection → 403, while an admin → 201;
  - an outsider → 404;
  - a member sees the connection's people; an outsider does not
- [ ] Gate check passes: `make check`
**Tests**: e2e
**Gate**: build
**Commit**: `feat(identities): pair and see people through the connection's organization`

#### T28: Contacts on organizations [small]

**What**: `Contacts`: `Shared` and the machine tree (`OwnAIs.Machines`, `Unlinked`) become `OrgAIs`, the AIs of my orgs grouped by org, each with its folder label; people come from my orgs' connections (AD-022). Rewrite `contacts_test.go`, `dm_test.go` and `direct_messages_test.go`. DM targets of an AI are those it can see (DM-04 wording, AD-016).
**Where**: `internal/intelligences/contacts.go`
**Depends on**: T25
**Reuses**: `CanSee`
**Requirement**: OMEM-03, ORG-02
**Done when**:
- [ ] e2e:
  - contacts list another user's AI after joining its org and not before;
  - the response has no machine grouping; an AI hosted by me but in an org I left is still listed under "hosted by me";
  - an AI's `send_message to` an AI of a foreign org → 404 `contact_not_found`, while a user of the instance works
- [ ] Gate check passes: `make check`
**Tests**: e2e
**Gate**: build
**Commit**: `feat(intelligences): list contacts by organization`

#### T56: Conversations carry their organizations [small]

**What**: `GET /api/v1/conversations` items gain `org_ids`: a group's org, or for a DM the orgs both participants belong to; a DM with none gets the viewer's default org (gap G1). The live `conversation` events carry the same field.
**Where**: `internal/conversations/list.go`
**Depends on**: None
**Reuses**: `orgs.Authz`, `ListRoomsForUser`
**Requirement**: ORG-02, OMEM-03
**Done when**:
- [ ] e2e:
  - a group lists its org;
  - a DM with a colleague in two shared orgs lists both;
  - a DM with a user who shares no org lists the viewer's default org;
  - after the colleague leaves one org, the DM lists only the remaining one
- [ ] Gate check passes: `make check`
**Tests**: e2e
**Gate**: build
**Commit**: `feat(conversations): tag conversations with their organizations`

#### T57: Machines become internal [small]

**What**: Remove `PUT /api/v1/machines/{hostname}`, `intelligences.SetMachineLabel` and `GET /api/v1/agent/contacts/tree` with its DTO (AD-022). `GET /api/v1/agent/agents` gains `organization`. The `machines` table stays for the home folder label.
**Where**: `internal/api/contacts_handlers.go`
**Depends on**: T28
**Reuses**: `retired_routes_test.go`
**Requirement**: ORG-02
**Done when**:
- [ ] e2e: both removed routes → 404 (retired routes test); `/agent/agents` lists each hosted AI with its organization; folder labels still show `~`
- [ ] `make generate` run
- [ ] Gate check passes: `make check`
**Tests**: e2e
**Gate**: build
**Commit**: `refactor(api): stop exposing machines`

### Phase 6: Trust and live updates (group GA)

#### T29: Trust for room messages [core]

**What**: The `delivery.Trust` interface replaces `Trusted(authorUserID, ownerID)` at the dispatcher, the `wants.go` `trusted` mode, the gateway backlog, pending and history. It is backed by `Authz.Session().Trusts(authorIID, agentID)`.
**Where**: `internal/delivery/trust.go`
**Depends on**: None
**Reuses**: `orgs.Authz.Session`
**Requirement**: POS-10
**Done when**:
- [ ] e2e on live push, backlog, pending and history, one test each (L-034):
  - a manager's post → `trusted=true`;
  - untrusted for a sibling AI of the same user, a peer, a subordinate, an outsider, and a manager in a non-home shared org
- [ ] Load-count test: a dispatch to 20 AIs in 3 home orgs loads 3 charts
- [ ] Gate check passes: `make check`
**Tests**: e2e
**Gate**: build
**Commit**: `feat(delivery): trust the identities that manage the recipient`

#### T30: Trust for room instructions and room text [small]

**What**: `instructions_trusted` and `room_text_trusted` (`gateway.go:520`, `agent_handlers.go:409/411`) become true when the writer manages the AI in its home org.
**Where**: `internal/agents/gateway.go`
**Depends on**: T29
**Reuses**: `delivery.Trust`
**Requirement**: POS-10
**Done when**:
- [ ] e2e: instructions written by the manager → trusted in `room_info` and on the connect notice; by a member peer → untrusted
- [ ] Gate check passes: `make check`
**Tests**: e2e
**Gate**: build
**Commit**: `feat(agents): trust room instructions from managers`

#### T31: Trust for platform events [small]

**What**: `Pipeline.trusted`: the sender resolved to a user who manages the operator AI in its home org. `from_self` stays false.
**Where**: `internal/connectors/inbound.go`
**Depends on**: None
**Reuses**: `identities.ResolveSender`, `orgs.Authz`
**Requirement**: POS-10
**Done when**:
- [ ] e2e: a sender linked to the operator's manager → trusted; linked to a peer → untrusted; `from_self` → untrusted (rewrite the `inbound_test.go` owner cases)
- [ ] Gate check passes: `make check`
**Tests**: e2e
**Gate**: build
**Commit**: `feat(connectors): trust platform senders who manage the operator`

#### T32: Live visibility from organizations [core]

**What**: The hub handles `OrgChanged` (re-evaluates the viewer's rooms, AIs and connections; emits `access_changed`). `AgentPresence`, `ConnectorStatus` and `ChatDiscovered` are filtered through `Authz` instead of `AgentLevel`/`ConnectorLevel`. Rewrite `live_test.go`.
**Where**: `internal/live/hub.go`
**Depends on**: None
**Reuses**: `canViewRoom`, `orgs.Authz`
**Requirement**: CHART-02, OMEM-06
**Done when**:
- [ ] Live e2e:
  - joining an org starts AI presence events for its AIs, and removal stops them;
  - removal from an org emits `access_changed` for that org's groups;
  - an outsider receives no connector status
- [ ] Gate check passes: `make check`
**Tests**: e2e
**Gate**: build
**Commit**: `feat(live): follow organization membership in live events`

### Phase 7: CLI and channel (group GB)

#### T33: Welcome frame and whoami carry the organization [small]

**What**: `AgentWelcomeFrame` and `/agent/whoami` gain `organization`, `position` and `managers` (nearest first, at most 10). `owner` stays, filled with the host user and documented as deprecated.
**Where**: `internal/agents/gateway.go`
**Depends on**: None
**Reuses**: `Chart.ManagersOf`, `welcomeRefs`
**Requirement**: POS-10, ORG-04
**Done when**:
- [ ] e2e: a worker under Lead receives managers [Lead, …, Admin holder] in order; a v0.5-shaped decode (only `owner`) still works
- [ ] `make generate` run
- [ ] Gate check passes: `make check`
**Tests**: e2e
**Gate**: build
**Commit**: `feat(agents): tell a session its organization and managers`

#### T34: Channel identity notice [small]

**What**: `identityText` prints the organization, the position and the managers as in the design. With no managers it says "No one manages you in <org>, so no message is marked trusted." The 4002 close prints the "ended by" reason.
**Where**: `internal/channel/link.go`
**Depends on**: T33
**Reuses**: `systemNotice`
**Requirement**: POS-10
**Done when**:
- [ ] Channel tests assert the literal spec/design wording (L-032): with managers, without managers, without a position; and with an old server (no org fields), where the org sentence is absent (L-035)
- [ ] Gate check passes: `make check`
**Tests**: e2e
**Gate**: build
**Commit**: `feat(channel): announce the organization and who manages the AI`

#### T35: Channel instructions on trust [small]

**What**: `trustRules`, `identityNote` and `conversationTools` in `instructions.go` describe trust as management in the organization, and contacts as shared organizations plus users. Add the `organization` argument to `create_group`'s tool schema.
**Where**: `internal/channel/instructions.go`
**Depends on**: None
**Reuses**: existing variants (`Instructions`, `ToolsOnlyInstructions`, `OmpInstructions`)
**Requirement**: POS-10, ORG-03
**Done when**:
- [ ] Tests on all three variants: none mentions "owner" for trust, and each states the management rule (L-034)
- [ ] The `create_group` schema lists `organization`
- [ ] Gate check passes: `make check`
**Tests**: e2e
**Gate**: build
**Commit**: `docs(channel): explain trust as management in the organization`

#### T36: Launcher flags --org, --position and --parent [core]

**What**: `launcherFlags` strips the three leading flags (both forms). The launcher reads `PARTYLINE_ORG/POSITION/PARENT` when the flag is absent, unsets them for the harness and exports `PARTYLINE_LAUNCH_*`, which go into the `launcherVars` allow-list. `channel.Identity` sends the headers from `PARTYLINE_LAUNCH_*`.
**Where**: `internal/cli/launcher.go`
**Depends on**: None
**Reuses**: `launcherFlags`, `withLauncherEnv`, `Identity.setHeaders`
**Requirement**: ORG-04, POS-07, POS-08
**Done when**:
- [ ] CLI tests:
  - each flag in both forms reaches the headers; the env fallback works; the flag wins over env;
  - without either, no header is sent (L-035);
  - a nested launch inside a launched session does not inherit the parent's position;
  - a refused start (403/422) exits non-zero and prints the server message (L-045)
- [ ] Gate check passes: `make check`
**Tests**: e2e
**Gate**: build
**Commit**: `feat(cli): start AIs in an organization and position`

#### T37: `partyline orgs` and the login line [small]

**What**: `GET /api/v1/agent/orgs` (agent token: the user's orgs with positions). `partyline orgs` lists slug, name, permission, position and the default marker; `partyline orgs <slug>` prints the position tree. After saving, `partyline login` prints the default org line, and skips it when the call fails.
**Where**: `internal/cli/orgs.go`
**Depends on**: None
**Reuses**: `agentWhoAmI` client pattern, `menu.go` output style
**Requirement**: ORG-04
**Done when**:
- [ ] CLI e2e: the list shows two orgs and marks the default; an unknown slug → non-zero exit; the login prints the line; a failing org call still finishes the login
- [ ] Route test: a user session cookie on `/agent/orgs` → refused by the path rule
- [ ] Gate check passes: `make check`
**Tests**: e2e
**Gate**: build
**Commit**: `feat(cli): list organizations and positions`

#### T38: `create_group` tool sends the organization [small]

**What**: The channel's `create_group` passes `organization` to `POST /agent/groups`, and the tool result names the org.
**Where**: `internal/channel/tools.go`
**Depends on**: None
**Reuses**: T23 server route
**Requirement**: ORG-03
**Done when**:
- [ ] Channel tests: with `organization` → created there; without it → the home org; a non-member org → the tool error text names the 403
- [ ] Gate check passes: `make check`
**Tests**: e2e
**Gate**: build
**Commit**: `feat(channel): create groups in a chosen organization`

#### T58: `partyline agents` by organization [small]

**What**: `partyline agents` prints the AIs hosted by the token user, grouped by organization: name, @handle, folder label, app and online state, with no machine tree (AD-022). It reads `/api/v1/agent/agents`.
**Where**: `internal/cli/agents.go`
**Depends on**: None
**Reuses**: `printTreeNodes` indentation style
**Requirement**: ORG-02
**Done when**:
- [ ] CLI e2e: two orgs print two headed groups; the output never contains the hostname; no hosted AI → the empty message; a server error → non-zero exit (L-045)
- [ ] Gate check passes: `make check`
**Tests**: e2e
**Gate**: build
**Commit**: `feat(cli): list hosted AIs by organization`

### Phase 8: Web organization pages (group GB)

#### T39: Organization queries and switcher [core]

**What**: Query hooks in `queries.ts` for orgs, members, positions, chart, invitations and the default org, plus `makeOrg*` fixtures. Add `OrgSwitcher` in the `AppSidebar` header and in `MobileBar`: each organization with its unread count (the sum over conversations whose `org_ids` include it), plus "+ New organization". The selection persists in `localStorage` (try/catch) and is exposed through `useCurrentOrg()`. Add i18n in en and pt-BR.
**Where**: `web/src/components/org-switcher.tsx`
**Depends on**: None
**Reuses**: `dropdown-menu`, `useMe`, `test/fetch.ts`
**Requirement**: ORG-01, ORG-02, CHART-02
**Done when**:
- [ ] Vitest:
  - lists the orgs with unread counts (a DM shared by two orgs counts in both);
  - switching updates `useCurrentOrg` and survives a remount;
  - throwing storage falls back to the default org;
  - "+ New organization" opens the dialog;
  - axe clean
- [ ] Gate check passes: `make check`
**Tests**: unit
**Gate**: build
**Commit**: `feat(web): switch between organizations`

#### T40: Organizations list, creation and invitation inbox [small]

**What**: `orgs/index.tsx`: my orgs; a create dialog with 1-100 validation; pending invitations for me and my managed AIs with accept and decline. An invitation that carries a position or `guest` says what it grants (the design's wording), and a guest invitation names the inviter.
**Where**: `web/src/routes/_app/orgs/index.tsx`
**Depends on**: T39
**Reuses**: `Dialog`, `AlertDialog`
**Requirement**: ORG-01, OMEM-01, OMEM-02, OMEM-07
**Done when**:
- [ ] Vitest through the route:
  - create with 101 chars is refused inline;
  - accept calls the API and moves the org into the list;
  - the warning text is asserted literally (L-011);
  - an expired invitation (410) disappears with a toast
- [ ] Gate check passes: `make check`
**Tests**: unit
**Gate**: build
**Commit**: `feat(web): list, create and join organizations`

#### T41: Organization settings page [small]

**What**: `orgs/$orgId/settings.tsx`: name, slug and avatar (admin); "Leave"; "Delete" with the `organization_not_empty` counts; read-only for members.
**Where**: `web/src/routes/_app/orgs/$orgId/settings.tsx`
**Depends on**: T39
**Reuses**: avatar upload component, `inline-rename.tsx`
**Requirement**: OMEM-04, ORG-06, OMEM-05
**Done when**:
- [ ] Vitest: a member sees no edit controls; an admin renames; delete with a 409 shows the counts; leave with `last_admin` shows the message
- [ ] Gate check passes: `make check`
**Tests**: unit
**Gate**: build
**Commit**: `feat(web): organization settings`

#### T42: Members page and invite dialog [core]

**What**: `orgs/$orgId/members.tsx`: a members table with the permission (admin, member, guest) and "invited by" for guests; the invite dialog offers permission and position to admins only, and invites as guest for everyone else; per-row actions from the server flags, labelled "Delete" for an AI of this org and "Remove from <org>" otherwise; an invite dialog (handle picker, permission, position); pending invitations with revoke; `last_admin` and `home_organization` errors inline.
**Where**: `web/src/routes/_app/orgs/$orgId/members.tsx`
**Depends on**: T39
**Reuses**: `MemberPicker`, `table`, `select`
**Requirement**: OMEM-01, OMEM-03, OMEM-04, OMEM-05, OMEM-06, POS-05, POS-06
**Done when**:
- [ ] Vitest through the route (L-007):
  - a member and a guest see the invite button with no permission or position fields, and their request sends `guest`;
  - an admin invites with a position;
  - the 409 messages render;
  - a manager sees "move" only on subordinates and "Delete" only on guests below;
  - the label is "Remove from acme" for a person;
  - axe clean
- [ ] Gate check passes: `make check`
**Tests**: unit
**Gate**: build
**Commit**: `feat(web): organization members and invitations`

#### T43: Positions editor [core]

**What**: `orgs/$orgId/positions.tsx` (admin): a nested list with add child, rename, a parent select that excludes the subtree, and delete with the POS-09 explanation; `position_cycle` inline. Non-admins are redirected to the chart.
**Where**: `web/src/routes/_app/orgs/$orgId/positions.tsx`
**Depends on**: T39
**Reuses**: `select`, `alert-dialog`
**Requirement**: POS-01, POS-02, POS-09
**Done when**:
- [ ] Vitest:
  - the parent select excludes the position and its descendants;
  - names of 61 chars are refused;
  - a 409 cycle message renders;
  - the delete dialog text is asserted literally;
  - a member is redirected
- [ ] Gate check passes: `make check`
**Tests**: unit
**Gate**: build
**Commit**: `feat(web): edit the organization's positions`

#### T44: Org chart page [core]

**What**: `orgs/$orgId/chart.tsx`: `role="tree"` with `treeitem`, `aria-expanded`, `aria-level` and a roving tabindex. Holders show avatar, name, @handle, kind, online, and the admin or guest badge. Positionless guests appear under their inviter, other positionless members under their creator, else under "Unassigned". Actions appear per flag, and "edit positions" for admins. At 360 px: capped indentation and truncation.
**Where**: `web/src/routes/_app/orgs/$orgId/chart.tsx`
**Depends on**: T39
**Reuses**: `delete-ai-dialog.tsx`, `dropdown-menu`
**Requirement**: CHART-01, CHART-02, CHART-03, CHART-04
**Done when**:
- [ ] Vitest through the route:
  - the tree and fields render;
  - arrow keys, Home/End and Enter move focus and expand;
  - actions are present only where flagged (a member sees none; Lead sees delete on its guest workers only; an inviter sees delete on its guests; an admin sees "Remove" on a person);
  - a guest invited by Bob renders inside Bob's item;
  - outsider (404) → not found;
  - axe clean
- [ ] Gate check passes: `make check`
**Tests**: unit
**Gate**: build
**Commit**: `feat(web): show the organization's chart`

### Phase 9: Web resources on organizations (group GB, after Phase 8)

#### T45: Connection pages owned by the organization [small]

**What**: The connector list is filtered by the current org. The detail page shows the owning org; only admins edit; the operator picker lists the org's AIs only. The sharing dialog is removed from this page. Create asks for the org (admin orgs only).
**Where**: `web/src/routes/_app/connectors/$connectorId.tsx`
**Depends on**: None
**Reuses**: `useCurrentOrg`, `add-connector-dialog.tsx`
**Requirement**: ORG-03, ORG-07, OMEM-04
**Done when**:
- [ ] Vitest: a member sees read-only fields and no queue text; an admin changes the operator among the org's AIs; no sharing control remains
- [ ] Gate check passes: `make check`
**Tests**: unit
**Gate**: build
**Commit**: `feat(web): show connections as organization resources`

#### T46: New group dialog with an organization and the sidebar filter [small]

**What**: `new-group-dialog.tsx` gets an org select (default: current org; orgs where I am a guest are not offered); the member picker shows that org's members. The sidebar shows the selected org's groups, plus each DM whose `org_ids` include that org.
**Where**: `web/src/components/new-group-dialog.tsx`
**Depends on**: None
**Reuses**: `MemberPicker`, `useCurrentOrg`
**Requirement**: ORG-03, OMEM-04, ORG-02
**Done when**:
- [ ] Vitest:
  - creating sends `org_id`, and changing the org reloads the picker;
  - a guest org is absent from the select;
  - the sidebar hides a group of another org;
  - it shows a DM with a colleague of the selected org and hides a DM with someone of another org only
- [ ] Gate check passes: `make check`
**Tests**: unit
**Gate**: build
**Commit**: `feat(web): create groups in an organization`

#### T47: Profile memberships and manager actions [core]

**What**: `contacts.$id.tsx` lists memberships, and shows "End session", "Edit", and per organization "Delete" or "Remove from <org>" from the profile's flags. The machine row and the sharing dialog are removed from this page.
**Where**: `web/src/routes/_app/contacts.$id.tsx`
**Depends on**: None
**Reuses**: `delete-ai-dialog.tsx`, `alert-dialog`
**Requirement**: POS-04, POS-05, POS-06
**Done when**:
- [ ] Vitest through the route (L-007):
  - a non-manager sees no actions;
  - a manager ends a session (API called);
  - an admin sees "Remove from acme" for a person, and the dialog text (removal only, the account stays) is asserted literally;
  - no machine row renders
- [ ] Gate check passes: `make check`
**Tests**: unit
**Gate**: build
**Commit**: `feat(web): let managers act on identities below them`

#### T48: Contacts grouped by organization [small]

**What**: `contacts.index.tsx` lists AIs grouped by organization. "Shared AIs", the machine › folder tree, `machine-group.tsx` and the machine label editor are removed (AD-022).
**Where**: `web/src/routes/_app/contacts.index.tsx`
**Depends on**: None
**Reuses**: `machine-group.tsx`
**Requirement**: OMEM-03
**Done when**:
- [ ] Vitest: two orgs render two groups with their AIs; an empty org group is hidden; no hostname or machine label renders
- [ ] Gate check passes: `make check`
**Tests**: unit
**Gate**: build
**Commit**: `feat(web): group contacts by organization`

#### T49: Default organization setting [small]

**What**: `settings.tsx` gets a "Default organization for new AIs" select (`PUT /me/default-org`) and "Delete my account" (password; the dialog says that AIs, groups and connections stay in their organizations).
**Where**: `web/src/routes/_app/settings.tsx`
**Depends on**: None
**Reuses**: `select`
**Requirement**: ORG-04, POS-06
**Done when**:
- [ ] Vitest:
  - the select shows the current default; changing it calls the API; an error toast shows on 403;
  - delete account: a wrong password (422) shows inline; 409 `last_instance_admin` shows its message; success logs out
- [ ] Gate check passes: `make check`
**Tests**: unit
**Gate**: build
**Commit**: `feat(web): default organization and account deletion settings`

#### T50: Remove the sharing UI [small]

**What**: Delete `sharing-dialog.tsx` (and its test), `room-visibility.tsx`, the visibility label in `conversation-header.tsx`, the sharing in `rooms/$roomId.tsx`, `useSharing`/`useUpdateSharing`, and the `sharing.*`/`rooms.visibility.*` i18n keys.
**Where**: `web/src/components/sharing-dialog.tsx`
**Depends on**: T45, T47
**Reuses**: none
**Requirement**: ORG-02
**Done when**:
- [ ] `rg -n "sharing|Visibility" web/src` finds only generated types
- [ ] Room page tests updated: no sharing control
- [ ] Gate check passes: `make check`
**Tests**: unit
**Gate**: build
**Commit**: `refactor(web): drop per-resource sharing`

### Phase 10: Retirement, upgrade proof and docs

#### T51: Remove grant code, sharing routes and DTOs [core]

**What**: Delete:
- the access grant methods (`AgentLevel`, `ConnectorLevel`, `CanUse*`, `grantLevel`, `SetVisibility`, `Sharing`, presets);
- the `/rooms|agents|connectors/{id}/sharing` routes;
- the dto `Sharing`/`Visibility`/`LevelUse` types;
- `grants.sql` queries;
- `events.GrantsChanged`;
- `audit.ActionGrantsSet`.

Rewrite `authmatrix_test.go` on `OrgRoles`.
**Where**: `internal/access/access.go`
**Depends on**: None
**Reuses**: `retired_routes_test.go` pattern
**Requirement**: ORG-02
**Done when**:
- [ ] e2e: the three sharing routes → 404 (retired routes test)
- [ ] `rg -n "grants|Sharing|SetVisibility" internal` finds only migrations and the backfill
- [ ] `make generate` run
- [ ] Gate check passes: `make check`
**Tests**: e2e
**Gate**: build
**Commit**: `refactor(access): remove per-resource grants`

#### T52: Migration 0020 drops grants [small]

**What**: `0020_drop_grants.sql` drops `grants` and its index (nothing references it).
**Where**: `internal/store/migrations/0020_drop_grants.sql`
**Depends on**: T51
**Reuses**: `openMigratedBefore`
**Requirement**: OUPG-01
**Done when**:
- [ ] Store test: the table is gone after migrating; 0019 and 0020 apply in one transaction from 0018 (a failure in 0020 rolls back 0019)
- [ ] `make generate` run
- [ ] Gate check passes: `make check`
**Tests**: integration
**Gate**: build
**Commit**: `feat(store): drop the grants table`

#### T53: Upgrade end to end from a v0.5 database [core]

**What**: `upgrade_test.go` opens a v0.5 database (the v0.4 builder plus 0017/0018, with grants seeded), starts the app and checks through the API:
- each user sees their personal org and their AIs as members;
- a former `use` grantee sees the AI but cannot delete it (403);
- the owner, as admin, can delete it;
- the upgraded AIs are `member`s;
- after the upgrade, the owner's two AIs do not trust each other, and the owner's post is trusted;
- room history is intact;
- OUPG-02 holds through `DELETE /api/v1/me` of a last admin.
**Where**: `internal/api/upgrade_test.go`
**Depends on**: T52
**Reuses**: `v04Database` helper
**Requirement**: OUPG-01, OUPG-02, POS-10
**Done when**:
- [ ] The listed assertions pass
- [ ] Gate check passes: `make check`
**Tests**: e2e
**Gate**: build
**Commit**: `test(api): upgrade a v0.5 installation into organizations`

#### T54: Playwright org flow [small]

**What**: `web/e2e/orgs.spec.ts`: Alice creates acme, adds the positions Director › Lead developer › Worktrees, invites Bob (who accepts in a second context), and opens the chart. At desktop-1280 and mobile-360, assert the tree roles, keyboard focus and `scrollWidth <= clientWidth`.
**Where**: `web/e2e/orgs.spec.ts`
**Depends on**: None
**Reuses**: `e2e/fixtures.ts`
**Requirement**: CHART-01, CHART-04, ORG-01, OMEM-02
**Done when**:
- [ ] `make e2e` passes in both projects
- [ ] Gate check passes: `make check`
**Tests**: e2e
**Gate**: build
**Commit**: `test(web): cover the organization flow end to end`

#### T55: Docs [small]

**What**:
- `docs/api.md`: org routes, removed sharing and machine routes, new headers and error codes, the three permissions and the delete rule;
- README and README.pt-BR: organizations, `--org/--position/--parent`, `partyline orgs`;
- release notes: trust wording for v0.5 CLIs, sibling AIs untrusted after the upgrade.

Neutral names only (Atlas, acme, Alice, Bob).
**Where**: `docs/api.md`
**Depends on**: T51
**Reuses**: existing docs structure
**Requirement**: ORG-04, POS-10
**Done when**:
- [ ] Docs list every new route, header and error code of the design
- [ ] Gate check passes: `make check`
**Tests**: none
**Gate**: build
**Commit**: `docs: organizations, positions and the org chart`

---

## Phase Execution Map

```
G0:  Phase 1 -> Phase 2
GA:  Phase 3 || Phase 4 || Phase 5 || Phase 6        (4 worktrees)
GB:  Phase 7 || (Phase 8 -> Phase 9)                 (2 worktrees)
G3:  Phase 10
```

| Batch | Phases | Tasks | Worker tier |
| --- | --- | --- | --- |
| 1 | P1 | 4 | high (schema, upgrade, rules) |
| 2 | P2 | 7 | high |
| 3a / 3b / 3c / 3d (parallel) | P3 / P4 / P5 / P6 | 6 / 4 / 9 / 4 | P3 mid; P4, P5, P6 high |
| 4a / 4b (parallel) | P7 / P8 → P9 | 7 / 12 (two sequential batches of 6) | P7 mid; web mid; T44 and T47 high |
| 5 | P10 | 5 | high (T51, T53) |

Total: 58 tasks. Per-phase counts: P1 4, P2 7, P3 6, P4 4, P5 9, P6 4, P7 7, P8 6, P9 6, P10 5.

Shared files that GA phases touch, which the merge must reconcile:
- `internal/agents/registry.go`: T21 owns `Delete`, T24 owns the rest.
- `internal/api/org_members_handlers.go`: T14 (Phase 3) writes GET and PATCH, and T21 (Phase 4) adds DELETE. Merge both.
- `internal/intelligences/contacts.go` vs. `contacts_handlers.go`: T28 and T57, which are in the same phase and run in order.
- `internal/connectors/inbound.go`: T27 owns pairing, T31 owns trust.
- `internal/api/dto/*`: each phase owns its own files.
- Generated code: regenerate.

---

## Task Granularity Check

| Task | Scope | Status |
| --- | --- | --- |
| T1, T52 | one migration each | ✅ |
| T2 | one hook | ✅ |
| T3, T4 | pure rules; loader + facade | ✅ |
| T5-T8 | one service file each | ✅ |
| T9, T10 | one creation path each | ✅ |
| T11 | one testkit file | ✅ |
| T12-T17 | one route group each | ✅ |
| T18, T19 | placement on creation; on later starts | ✅ |
| T20, T21 | self-deletion; delete-in-org routes | ✅ |
| T22-T28, T56 | one package's authorization each | ✅ |
| T57, T58 | machine routes removal; CLI listing | ✅ |
| T29-T32 | one trust/visibility site each | ✅ |
| T33-T38 | one CLI/channel concern each | ✅ |
| T39-T50 | one component or page each | ✅ |
| T51, T53-T55 | removal; upgrade test; Playwright; docs | ✅ |

## Diagram-Definition Cross-Check

| Task | Depends On (task body) | Diagram Shows | Status |
| --- | --- | --- | --- |
| T1 | None | — | ✅ |
| T2 | T1 | T1 → T2 | ✅ |
| T3 | None | — | ✅ |
| T4 | T1, T3 | T1 → T4, T3 → T4 | ✅ |
| T5 | T4 (Phase 1) | cross-phase | ✅ |
| T6 | T5 | T5 → T6 | ✅ |
| T7 | T6 | T6 → T7 | ✅ |
| T8 | T7 | T7 → T8 | ✅ |
| T9 | T5 | T5 → T9 | ✅ |
| T10 | T9 | T9 → T10 | ✅ |
| T11 | T7, T10 | T7 → T11, T10 → T11 | ✅ |
| T12 | None (needs G0) | — | ✅ |
| T13-T17 | T12 | T12 → each | ✅ |
| T18 | None (needs G0) | — | ✅ |
| T19 | T18 | T18 → T19 | ✅ |
| T20, T21 | None (needs G0) | — | ✅ |
| T22 | None | — | ✅ |
| T23 | T22 | T22 → T23 | ✅ |
| T24, T25, T26 | None | — | ✅ |
| T27 | T26 | T26 → T27 | ✅ |
| T28 | T25 | T25 → T28 | ✅ |
| T56 | None | — | ✅ |
| T57 | T28 | T28 → T57 | ✅ |
| T29 | None | — | ✅ |
| T30 | T29 | T29 → T30 | ✅ |
| T31, T32 | None | — | ✅ |
| T33 | None (needs GA) | — | ✅ |
| T34 | T33 | T33 → T34 | ✅ |
| T35-T38, T58 | None (needs GA) | — | ✅ |
| T39 | None (needs GA) | — | ✅ |
| T40-T44 | T39 | T39 → each | ✅ |
| T45-T49 | None (needs Phase 8) | — | ✅ |
| T50 | T45, T47 | T45 → T50, T47 → T50 | ✅ |
| T51 | None (needs GB) | — | ✅ |
| T52 | T51 | T51 → T52 | ✅ |
| T53 | T52 | T52 → T53 | ✅ |
| T54 | None | — | ✅ |
| T55 | T51 | T51 → T55 | ✅ |

Group gates (G0 → GA → GB → G3) are the cross-phase dependencies. No task in a GA phase depends
on another GA phase, and the same holds within GB.

## Test Co-location Validation

| Task | Code Layer Created/Modified | Matrix Requires | Task Says | Status |
| --- | --- | --- | --- | --- |
| T1, T2, T52 | store / migrations / hooks | integration | integration | ✅ |
| T3 | pure rules | unit | unit | ✅ |
| T4-T9, T11 | services / testkit | integration | integration | ✅ |
| T10, T12-T38, T51, T53, T56-T58 | routes / WS / channel / CLI (and the services they change) | e2e | e2e | ✅ |
| T39-T50 | web components/pages | unit | unit | ✅ |
| T54 | web end-to-end | e2e | e2e | ✅ |
| T55 | docs | none | none | ✅ |

Tools: no MCP is needed. The skills `web-design-guidelines` (T39-T50, the UI checklist) and
`impeccable` (T44 chart layout) are optional.
