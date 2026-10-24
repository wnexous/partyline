# Organizations Validation

**Date**: 2026-10-24
**Spec**: `.specs/features/organizations/spec.md`
**Diff range**: `git diff c869e26...wnexous/pl-org-g0` (head `9a5d968`). The branch's merge-base with main is `f8038d8`, not `c869e26`: see Issue 1.
**Verifier**: independent sub-agent (author ≠ verifier)

**Verdict: PASS.** All 31 requirements have tests that check the outcome the spec defines. All 35 behaviour mutants were killed. The full gate, the e2e suite and the real v0.5.0 upgrade test all pass. Before landing, the branch must be merged with main and re-gated (Issue 1).

---

## Task Completion

| Task | Status | Notes |
| --- | --- | --- |
| T1-T43, T45-T58 | ✅ Done | Every Done-when item is ticked |
| T44 | ⚠️ Bookkeeping | Commit `15d356e` implements it and `web/src/routes/_app/orgs/org-chart.test.tsx` covers its Done-when list, but `tasks.md:2089` and `tasks.md:2096` are still unticked |

`validate_spec`: 0 errors, 0 warnings. `validate_tasks`: 0 errors, 1 warning (T55 has `Tests: none`; it is the docs task).

---

## Spec-Anchored Acceptance Criteria

Every cited line was opened. `RequireError(status, code)` checks both the HTTP status and the error code. `requireRefusal`, `runRoleRoutes` and `requireRefused` map 403 to `forbidden` and 404 to `not_found`.

| Req | Spec-defined outcome | `file:line` + assertion | Result |
| --- | --- | --- | --- |
| ORG-01 | Creator is `admin` holding a root "Admin"; name 1-100 | `internal/orgs/service_test.go:58` - `require.Equal(t, Position{ID: got.PositionID, Name: "Admin"}, root, ...)`; bounds at :73-99 (100 accepted, 101 refused); `internal/api/orgs_test.go:113` - `require.Equal(t, dto.PermissionAdmin, created.Permission)` | ✅ |
| ORG-02 | Exactly one org per AI, connection and group; none for a DM | `internal/store/organizations_backfill_test.go:159` - `require.Zero(...org_id IS NULL...)`; `:149` "a DM belongs to no organization" | ✅ (see Issue 5) |
| ORG-03 | Group or connection outside a member org (connections: outside an admin org) → 403 `forbidden` | `internal/api/groups_org_test.go:52` - `resp.RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)`; `internal/api/connectors_handlers_test.go:329` - `create(roles.Member, roles.Org.ID).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)` | ✅ |
| ORG-04 | `--org`, else the default org; `member` when a user starts, `guest` with `--parent` | `internal/api/agent_placement_test.go:183` - `requireStartedAs(..., orgs.Member{Permission: orgs.PermissionMember})`; `:210` - `orgs.Member{Permission: orgs.PermissionGuest, PositionID: w.worktrees, InviterIID: w.lead}`; `internal/api/agent_default_placement_test.go:86` | ✅ (see Issue 3) |
| ORG-05 | Creator not a member → 403 `forbidden` | `internal/api/agent_placement_test.go:274` - `w.requireRefused(t, before, refused, http.StatusForbidden, dto.ErrorCodeForbidden)`, which also checks that nothing was created | ✅ |
| ORG-06 | Org owning AIs, connections or groups → 409 `organization_not_empty` | `internal/api/orgs_test.go:149` - `RequireError(http.StatusConflict, dto.ErrorCodeOrganizationNotEmpty)`; `internal/orgs/service_test.go:328-365` (each kind alone blocks) | ✅ |
| ORG-07 | Operator is an AI of the connection's org | `internal/api/connector_operator_test.go:215` - `RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeInvalid)` with field `operator_id` | ✅ (status not given in spec) |
| OMEM-01 | Admin invites with any permission; members and guests invite guests; inviter recorded | `internal/api/org_invitations_handlers_test.go:123` - `require.Equal(t, c.User.ID, inv.InvitedBy.ID, ...)`; matrix at :94-104 | ✅ |
| OMEM-02 | Accepting gives that permission and position | `internal/api/org_invitations_handlers_test.go:226` - `require.Equal(t, dto.PermissionAdmin, org.Permission)`; `:227` - `require.Equal(t, r.ManagerPosition, org.PositionID)` | ✅ |
| OMEM-03 | Several orgs, each with its own permission and position | `internal/api/org_members_handlers_test.go:154-159` - `require.Equal(t, globex.ManagerPosition, inGlobex.PositionID)` | ✅ |
| OMEM-04 | Non-admin positions, non-guest invite, permission change, connections, rename → 403; a guest creating a group → 403 | `internal/api/positions_handlers_test.go:31`, `:43`; `internal/api/org_invitations_handlers_test.go:115`; `internal/api/org_members_handlers_test.go:190`; `internal/api/connectors_handlers_test.go:241-266`; `internal/api/orgs_test.go:127`; `internal/api/groups_org_test.go:52` - all `RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)` | ✅ |
| OMEM-05 | Leaving no admin → 409 `last_admin` | `internal/api/org_members_handlers_test.go:240` - `.RequireError(http.StatusConflict, dto.ErrorCodeLastAdmin)`; `internal/orgs/members_test.go:155-226` (demote, leave, remove) | ✅ |
| OMEM-06 | Out of the org's groups and position; the org's AIs stay | `internal/orgs/members_test.go:418-422`; `internal/api/delete_identities_test.go:506` - `require.Zero(..., "out of Acme's group")` | ✅ |
| OMEM-07 | Expires after 14 days | `internal/api/org_invitations_handlers_test.go:482` - `RequireError(http.StatusGone, dto.ErrorCodeGone)`; boundary 14d−1ms accepted at :474-477 | ✅ |
| OMEM-08 | An operator that leaves the org leaves the connection with no operator | `internal/orgs/members_test.go:587` - `require.Empty(t, ...operator_iid..., "no operator any more")`; `internal/api/delete_identities_test.go:593` | ✅ |
| POS-01 | Admin adds a position (1-60) under a parent of the same org | `internal/orgs/positions_test.go:36` - `require.Equal(t, Position{ID: child.ID, Name: "Night   shift", ParentID: root.ID}, child, ...)`; `internal/api/positions_handlers_test.go:68-76` (61 characters and a foreign parent → 422) | ✅ |
| POS-02 | Cycle → 409 `position_cycle` | `internal/api/positions_handlers_test.go:95` - `.RequireError(http.StatusConflict, dto.ErrorCodePositionCycle)`; concurrent moves at `internal/orgs/positions_test.go:210` | ✅ |
| POS-03 | Strictly above; with no position, the creator or above the creator | `internal/orgs/chart_test.go:60-145` - `require.Equal(t, tc.want, c.Manages(tc.a, tc.b), tc.name)`; the admin permission alone gives no management (:96-102) | ✅ |
| POS-04 | A manager edits, ends the session and moves below its own position | `internal/api/org_members_handlers_test.go:325` - `require.Equal(t, below, got.PositionID)`; same level, above or another branch → 403 at :336-341; `internal/api/leave_and_end_session_test.go:64` | ✅ |
| POS-05 | Admins delete anyone; a guest's inviter chain and its managers delete the guest; only an admin deletes a member; else 403 | `internal/api/delete_identities_test.go:236-245` (manager, peer and inviter → 403 on a member); `:463`, `:469` (guest chain → 204); `:407` (Sentinel → 403) | ✅ |
| POS-06 | An AI deleted in its home org is gone for good; a person or a foreign AI is only removed; the account stays | `internal/api/delete_identities_test.go:414` - `requireDeletedForGood(t, w.env, w.workers[0])`; `:505` - `requireRemovedOnly(t, env, r.Org.ID, bob)` (logs in again); `:572` - `requireAIIntact` | ✅ |
| POS-07 | Holds `--position`; not below the creator and not an admin → 403 | `internal/api/agent_placement_test.go:339` - `w.requireRefused(..., http.StatusForbidden, dto.ErrorCodeForbidden)`; `:346` - `require.Equal(t, c.held, m.PositionID, ...)`; CLI pre-check at `internal/cli/launcher_placement_test.go` | ✅ |
| POS-08 | `--parent` recorded as creator when the holder may act for it; else the linking user | `internal/api/agent_placement_test.go:210`; foreign parent `:256` - `requireStartedAs(t, id, c.org, c.host.User.ID, ...)`; `PARTYLINE_PARENT` at `internal/cli/launcher_placement_test.go:113` | ✅ |
| POS-09 | Holders and children move to the parent | `internal/orgs/positions_test.go:298` - `require.Equal(t, a.manager, f.parentOf(a.trees), ...)`; `:299` holders | ✅ (API test is weak: Issue 4) |
| POS-10 | `trusted=true` only when the author manages the AI in its home org | `internal/api/delivery_trust_test.go:190` - `require.Equal(t, a.trusted, got.Trusted, a.name)` (a manager in a shared non-home org is false); backlog, pending and history covered too | ✅ |
| CHART-01 | Tree; holder fields; members with no position under their creator or under "Unassigned" | `internal/api/org_chart_handlers_test.go:157-198`; `web/src/routes/_app/orgs/org-chart.test.tsx:212` - `expect(levels).toEqual([1, 2, 2, 3, ...])` | ✅ |
| CHART-02 | Members and guests only; guests under their inviter | `internal/api/org_chart_handlers_test.go:254` - `resp.RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)` for outsiders; guest `Under` equals the inviter at :220; web at `org-chart.test.tsx:217` | ✅ (404 not given in spec: Issue 2) |
| CHART-03 | Move only where the viewer manages; delete per POS-05; edit positions for admins only | `internal/api/org_chart_handlers_test.go:358-370` (flags equal `Authz.CanDelete`; PATCH without the move flag → 403); `org-chart.test.tsx:520`, `:558`; `web/e2e/orgs.spec.ts:392` | ⚠️ Spec-precision gap: admin move (Issue 2) |
| CHART-04 | `tree` and `treeitem` roles, keyboard, fits 360 px | `web/e2e/orgs.spec.ts:361` - `expectNoSidewaysScroll(page, "the org chart")` in project `mobile-360`; `org-chart.test.tsx:338` - roving tabindex `expect(stops).toHaveLength(1)`; axe at :778 | ✅ |
| OUPG-01 | Personal org with the user as admin holding "Admin"; resources moved; grantees join as members with no position; one transaction | `internal/store/organizations_backfill_test.go:137`, `:177` - `"alice\|u-bob\|member\|\|"`; `internal/store/grants_drop_migration_test.go:102` - "0019 was rolled back" after a forced failure in 0020; `internal/api/upgrade_v05_binary_test.go` (real v0.5.0 database) | ✅ |
| OUPG-02 | The last admin deleting their account makes the instance admin an admin | `internal/users/delete_test.go:276` - `require.Equal(t, orgs.Member{IID: heir.ID, Permission: orgs.PermissionAdmin}, m, ...)` | ✅ |

**Status**: ✅ 31/31 covered. 1 spec-precision gap flagged (CHART-03/POS-04 admin move), plus outcomes the spec leaves unstated (Issue 2).

---

## Discrimination Sensor

The sensor ran in a detached scratch worktree of `9a5d968`, one mutant at a time. Each mutant was restored with `git checkout` inside the scratch, and the scratch was checked clean before the next. Go mutants ran `go test -p 2 -count=1` with `GOMAXPROCS=4` on the covering packages; every mutant that survived a `-run` filter was re-run against whole packages. Web mutants ran `bun x vitest run`. The `pl-org-g0` porcelain was empty before and after.

| # | File | Mutation | Killed by (first failing tests) |
| --- | --- | --- | --- |
| M01 | `internal/orgs/positions.go` CreatePosition | Admin check (before and inside the tx) → member | ✅ `TestPositionsAreEditedByAdminsOnly`, `TestPositionRoutesMatrix` |
| M02 | `internal/orgs/members.go` UpdateMember | Permission change without admin | ✅ `TestPatchOrgMemberPermissionMatrix` +4 |
| M03 | `internal/orgs/invitations.go:83` | A non-admin invites as member | ✅ `TestInviteMatrix`, `TestMembersAndGuestsInviteGuestsOnly` +2 |
| M04 | `internal/orgs/invitations.go` ListInvitations | Admin → member | ✅ `TestListAndRevokeInvitationsMatrix` |
| M05 | `internal/orgs/members.go` snapshot | Chart and members readable by non-members | ✅ `TestChartViewIsForMembers`, `TestChartVisibility` |
| M06 | `internal/orgs/personal.go` SetDefault | Default org without membership | ✅ `TestSetDefaultRequiresMembership`, `TestTheDefaultOrganizationMustBeOneYouBelongTo` |
| M07 | `internal/rooms/rooms.go:254` | A guest creates a group | ✅ `TestCreateGroupInAnOrganization` +2 |
| M08 | `internal/orgs/chart.go:234` | Any member deletes any guest | ✅ `TestCanDelete`, `TestGuestsInviteGuestsAndTheChainDeletesThem` +2 |
| M09 | `internal/orgs/chart.go:234` | A manager deletes a member | ✅ `TestCanDelete` ("a manager cannot delete a member below") |
| M10 | `internal/orgs/chart.go:97` | Guest inviter chain ignored | ✅ `TestCanDelete`, `TestAManagerDeletesAGuestItsPositionReaches` +2 |
| M11 | `internal/orgs/members.go` remove | Removing a person also runs `DeleteUser` | ✅ `TestDeletingAPersonOnlyRemovesThemFromThatOrganization` (401 on re-login) +2 |
| M12 | `internal/agents/placement.go:72` | A guest host's AI joins as member | ✅ `TestAGuestHostsNewAIJoinsAsTheirGuest` |
| M13 | `internal/agents/placement.go:80` | `--position` allowed above the creator | ✅ `TestStartWithAPositionFollowsTheCreatorsStanding`, `TestTheLauncherRefusesAStartTheServerRefuses` (CLI) |
| M14 | `internal/orgs/authz.go` trusts | Any member of the home org trusted | ✅ `TestLivePushIsTrustedOnlyWhenTheAuthorManagesTheAI` +5 |
| M15 | `internal/live/hub.go:565` | Org events reach non-members | ✅ `TestLiveUserMembershipFollowsAccess`, `TestLiveRemovalFromAnOrganizationTellsThatOrganizationsGroups` |
| M15b | `internal/live/hub.go` canSeeAgent | Every user sees every AI's presence | ✅ `TestLiveAgentPresenceFollowsOrganizationMembership` +3 |
| M16 | `web/src/routes/_app/orgs/$orgId/members.tsx:353` | Delete offered without `can_delete` | ✅ 3 vitest cases (POS-04..06 block) |
| M16b | `members.tsx:365` | Remove offered without `can_delete` | ✅ 4 vitest cases |
| M17 | `internal/store/migrations/0020_drop_grants.sql` | Grants table kept | ✅ `TestMigration0020DropsTheGrantsTable` +5 |
| M18 | `internal/cli/agents.go` | `partyline agents` prints the machine again | ✅ `TestAgentsPrintsTheHostedAIsByOrganization` +3 |
| M19 | `internal/orgs/chart.go:84` | Cycle check only rejects self | ✅ `TestPositionCycleIsConflict`, `TestTwoMovesThatTogetherMakeACycleCannotBothSucceed` +2 |
| M20 | `internal/orgs/positions.go:218` | Holders of a deleted position lose their position instead of moving to the parent | ✅ `TestDeletingAMiddlePositionMovesHoldersAndChildrenToItsParent` +1 |
| M21 | `internal/orgs/members.go:302` | Last-admin bound `<= 1` → `< 1` | ✅ `TestDemotingTheLastAdminIsConflict` +3 |
| M22 | `internal/store/organizations_backfill.go:141` | Upgrade grantee joins as admin | ✅ `TestOrganizationsUpgradeTurnsGrantsAndGroupMembersIntoMemberships`, `TestUpgradedV05InstallationBecomesOrganizations` |
| M23 | `internal/users/delete.go` inheritAdmin | Heir joins as member | ✅ `TestTheHeirBecomesAdminOfEachOrganizationTheUserWasTheLastAdminOf` +2 |
| M24 | `internal/orgs/invitations.go:21` | TTL of 15 days | ✅ `TestInvitationsExpireAfterFourteenDays` +5 |
| M25 | `internal/agents/placement.go:110` | A foreign `--parent` accepted | ✅ `TestAParentTheHostCannotActForFallsBackToTheHost` |
| M26 | `internal/connectors/service.go` creatorOrg | A member creates a connection | ✅ `TestConnectorRoutesAuthMatrix`, `TestCreateConnectorNamesItsOrganization` |
| M27 | `internal/orgs/chart.go` Manages | The admin permission implies management | ✅ `TestAdminPermissionDoesNotImplyManagement` +4 |
| M28 | `web/src/routes/_app/orgs/$orgId/chart.tsx:288` | Move offered without `can_move` | ✅ org-chart vitest |
| M29 | `chart.tsx:90` | "Edit positions" for non-admins | ✅ 2 org-chart vitest cases |
| M30 | `internal/orgs/members.go:379` | Removal keeps the org's group memberships | ✅ `TestRemovalDropsOnlyThatOrganizationsGroupsAndKeepsTheAIs` +4 |
| M31 | `internal/orgs/members.go:387` | Operator kept after leaving (OMEM-08) | ✅ `TestRemovingAnOperatorClearsItsConnections` |
| M32 | `internal/orgs/service.go:460` | Connections do not block org deletion | ✅ `TestDeleteRefusesWhileTheOrganizationOwnsSomething` |

**Sensor depth**: P0-full (authorization and data integrity): 35 manual behaviour mutants across every phase (schema and upgrade, chart rules, services, API routes, placement, live, CLI, web).
**Result**: 35/35 killed - PASS ✅

---

## Gate Check

- **Gate command**: `make check SHARDS=2 GOFLAGS=-p=2` with `GOMAXPROCS=4`, run in the foreground in the scratch copy → exit 0 (4m09s). Every Go package passed (internal/api 576 tests, internal/orgs 109). Integrations: 85 passed. Web: 63 files, 1227 tests passed. Lint, vet, drift, web-build and `go build` all passed.
- **e2e**: `make e2e` with the cached chrome-headless-shell → 34 passed (17 tests × desktop-1280 and mobile-360), 7 of them in `web/e2e/orgs.spec.ts`.
- **Upgrade with the real binary**: v0.5.0 was built from the tag in a temporary worktree. `PARTYLINE_V05_BINARY=… go test -run TestUpgradedRealV05 ./internal/api/` ran and was not skipped: `TestUpgradedRealV05InstallationBecomesOrganizations` passed all 6 subtests (personal orgs, grantee rights, history kept, trust, admin delete, heir).
- **Test count** (`func Test` / vitest cases / e2e): before 1348 / 810 / 13; after 1718 / 1106 / 17.
- **Skipped**: none in the gate. The real-binary test is opt-in and was run explicitly.
- **Flake**: `TestDispatchSkipsPausedRoom` (`internal/api/agent_dispatch_test.go:92`) failed once in the M23 run and once in 10 iterations of `-count=10` on the unmutated tree. It then passed 10/10 in separate runs and 10/10 in another `-count=10`. The test predates this feature and the feature changed only its fixture. It is a pre-existing timing race: a room paused by a direct DB write still delivered once.

---

## Code Quality

| Principle | Status |
| --- | --- |
| Minimum code / surgical changes | ✅ |
| Matches patterns | ✅ (authorization goes through `orgs.Authz`/`Chart`; checks are repeated inside the write transaction) |
| Spec-anchored outcome check | ✅ with the gaps flagged below |
| Per-layer coverage (domain 1:1; routes happy, edge and error) | ✅ (role matrices for every org route) |
| Every test maps to a requirement | ✅ (tests are tagged with their requirement IDs) |
| Public-repo hygiene | ✅ The added docs, specs and tests contain no private names, hosts, phone numbers or tokens. Phone numbers are masked synthetics (`+55 11 *****-0000`), `/home/dev/...` fixture folders were already on main, and the one first name in `integrations/testdata/framing.json` was already on main. |

---

## Issues (ranked)

1. **Major - the branch is not based on `c869e26` and conflicts with main.** The merge-base is `f8038d8`. `c869e26` (pl-ux3 profile panel, pl-theme2) and `2a7c3b6` (pl-launchonly) are missing. `git merge-tree HEAD main` conflicts in `docs/agents.md`, `internal/cli/procenv_test.go`, `web/e2e/smoke.spec.ts` and the web surfaces this feature rewrote (`machine-group.tsx`, `sharing-dialog.tsx`, `new-group-dialog.tsx`, `profile-editor.tsx`, `conversation-header.tsx`, `_app.tsx`, i18n, `connectors.test.tsx`). This verdict covers `9a5d968` as built. After the merge, re-run `make check` and `make e2e`, and check that the profile panel from pl-ux3 shows no machine and no grant UI.
2. **Minor - spec-precision gaps.** (a) CHART-03/POS-04 say "move" only where the viewer manages the target, yet the admin permission gives no management (POS-03, `internal/orgs/chart_test.go:96`). The code and `TestChartFlagsForTheAdmin` (`internal/api/org_chart_handlers_test.go:277`) let an admin move anyone; `design.md:357` sanctions this, but the spec text does not. (b) Outsiders get 404 `not_found` on every org route and on the chart, while ORG-03 and the default org give 403. Expired invitations answer 410 `gone`; unknown `--org` and `--position` answer 422. All of this is consistent and tested, but the spec states none of it.
3. **Minor - spec deviations not written back into the spec.** ORG-04 says an AI joins as `member` when a user starts it. The code makes it a `guest` when that user is only a guest (`internal/agents/placement.go:72`; test `internal/api/agent_default_placement_test.go:177`). ORG-03 says the creator "SHALL name an organization", but the code falls back to the default org. Both are sensible; amend the spec.
4. **Minor - the POS-09 API test is weak and its comment is wrong.** At `internal/api/positions_handlers_test.go:114` the comment says "The child is now a root", but it moves under `AdminPosition`, and the test only checks a rename. The service test (`internal/orgs/positions_test.go:298`) proves the rule, and M20 was killed.
5. **Minor - ORG-02 "exactly one" is enforced by code, not by the schema.** `org_id` stays nullable for legacy unplaced AIs.
6. **Cosmetic - bookkeeping.** T44 Done-when is unticked (`tasks.md:2089`, `:2096`), and the traceability table in `spec.md` still says "Implementing".

---

## Requirement Traceability Update

All 31 requirements (ORG-01..07, OMEM-01..08, POS-01..10, CHART-01..04, OUPG-01..02): Implementing → ✅ Verified (the orchestrator updates `spec.md`).

---

## Summary

**Overall**: ✅ Ready for merge prep. Resolve Issue 1 and re-gate before landing.
**Spec-anchored check**: 31/31 covered; 1 spec-precision gap (CHART-03 admin move) and unstated refusal codes flagged.
**Sensor**: 35/35 mutations killed.
**Gate**: `make check` exit 0; e2e 34/34; real v0.5.0 upgrade 6/6.
