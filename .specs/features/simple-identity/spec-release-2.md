# Simple identity — release 2: fewer concepts

Release 1 (`spec.md`, SI-01..SI-10) fixed what hurt without removing anything. Release 2 removes the
concepts nobody uses and replaces the chains of management with one explicit link. The owner's words
(2026-11-18): it has become "too complex, messy"; "it has to be simpler, maybe even remove features".
Every organization on the reference instance has only the default `Admin` position.

## The model after release 2

- An **organization** is a space shared with other people: its AIs, groups and connections. Two
  permissions: `admin` (settings, invitations, connections, deleting identities of it) and `member`.
- An AI is **instructed** (its messages from them are `trusted`) by its host — the user who runs it —
  and by the identities listed as its **instructors**. Nothing else makes a message trusted.
- `--parent @x` at the start of a new AI adds `@x` as an instructor of it. The flag stays; its meaning is
  this one link.

## Requirements

### Guests go

1. The permission of a membership or an invitation SHALL be `admin` or `member`; the migration SHALL turn
   every `guest` membership and pending guest invitation into `member`. (SR2-01)
2. An AI started by another AI (`--parent`) SHALL join as a `member`. (SR2-02)
3. `inviter_iid` SHALL no longer grant anything (management, deletion); the column may stay as history or
   be dropped. (SR2-03)

### Positions and the org chart go

4. The migration SHALL drop positions: the `positions` table, `org_members.position_id`,
   `org_invitations.position_id`. (SR2-04)
5. The API routes, DTO fields, CLI flags (`--position`, `PARTYLINE_POSITION`, `partyline orgs <slug>`
   chart output) and web pages of positions and the org chart SHALL be removed. A launcher given
   `--position` SHALL refuse with a short message that positions were removed (exit 2). (SR2-05)

### One explicit link: instructors

6. A new table `agent_instructors (agent_iid, instructor_iid, created_by, created_at, PRIMARY KEY
   (agent_iid, instructor_iid))` SHALL list who instructs an AI besides its host. (SR2-06)
7. A message, platform item, schedule or room text SHALL be trusted for an AI exactly when its author is
   the AI's host or one of its instructors (a verified chat account of such a user counts as that user,
   as today). The AI's own sibling sessions stay trusted. (SR2-07)
8. The migration SHALL add, for every AI whose `creator_iid` is another AI (or another user than its
   host), that creator as an instructor, so no AI loses a manager it has today through creation. (SR2-08)
9. `--parent @x` on a new AI SHALL add `@x` as an instructor (when the token holder hosts `@x`, as today);
   on an existing AI it SHALL change nothing. (SR2-09)
10. Only the host SHALL add or remove instructors: web (the AI's profile: "Can instruct this AI", add by
    @handle, remove) and API (`GET/POST/DELETE /api/v1/agents/{id}/instructors`). An AI SHALL NOT change
    its own instructors. (SR2-10)
11. The welcome and identity notice SHALL name the instructors ("Messages from @andre and @x are
    trusted") instead of managers from the chart; release 1's creator/created lines stay. (SR2-11)

### Deleting and editing

12. An AI SHALL be deletable by its host or an admin of its home organization; a person is removed from an
    organization by an admin, or leaves it. (SR2-12)
13. An AI's profile and settings SHALL be editable by its host and by its instructors that are users.
    (SR2-13)

### Less in Contacts

14. The Contacts page SHALL drop the **People** (pre-v0.5 platform persons) and **Hosted by me** sections;
    merging stays on the AI's profile ("Merge into…"). Earlier history-only rooms stay readable. (SR2-14)

### Migration safety

15. Migration `0040` SHALL run after the automatic pre-migration backup and SHALL be one transaction; a
    test SHALL load a database with guests, positions, creators and inviters and check the result. (SR2-15)

## Out of scope

Changing an AI's home organization; multi-tenant permission models; anything in connections.

## Traceability

| Req | Code | Test |
| --- | --- | --- |
| SR2-01 | `internal/orgs/roster.go` `Permission` (admin, member); `internal/orgs/invitations.go` `Invite` (admins only); migration `0040_simple_identity.sql` (guests and guest invitations → member, CHECK narrowed) | `internal/orgs/roster_test.go` `TestSR201PermissionsAreAdminAndMember`; `internal/orgs/invitations_test.go` `TestSR201OnlyAdminsInviteAsAdminOrMember`; `internal/orgs/members_test.go` `TestAddMember`; `internal/store/organizations_schema_test.go` `TestOrgPermissionsAreAdminOrMember`; `internal/store/simple_identity_migration_test.go` `TestSR215Migration0040ConvertsGuestsPositionsAndCreators`; `internal/api/org_invitations_handlers_test.go` `TestSR201InviteMatrix`; `internal/api/org_members_handlers_test.go` `TestSR201PatchOrgMemberValidates` |
| SR2-02 | `internal/agents/placement.go` `place`; `internal/agents/folder.go` `createFolderAgent` (always `member`) | `internal/agents/placement_test.go` `TestSR209ParentBecomesTheInstructorOfANewAIOnly`; `internal/cli/launcher_placement_e2e_test.go` `TestTheLauncherStartsAWorkerUnderAParentAI`; `internal/api/agent_placement_test.go` `TestSR209StartWithAParentAIMakesTheParentAnInstructor` |
| SR2-03 | migration `0040` drops `org_members.inviter_iid`; `internal/orgs/roster.go` `CanRemove`; `internal/conversations/merge.go` (no inviter reassignment) | `internal/orgs/members_test.go` `TestSR212RemoveIsForAdmins`; `TestSR215Migration0040ConvertsGuestsPositionsAndCreators` |
| SR2-04 | migration `0040` (drops `positions`, `org_members.position_id`, `org_invitations.position_id`) | `TestSR215Migration0040ConvertsGuestsPositionsAndCreators`; `internal/store/organizations_schema_test.go` `TestOrganizationsSchemaHasEveryTableColumnAndIndex`; `internal/orgs/personal_test.go` |
| SR2-05 | routes, DTOs and services of positions, chart and move preview removed (`internal/api`, `internal/orgs`); `internal/cli/launcher.go` `errPositionRemoved`, `launcherFlags`; `internal/cli/orgs.go`; `internal/cli/procenv.go`; `internal/agents/folder.go` `FolderIDFromHeader` (position header ignored); web chart and positions pages removed | `internal/cli/launcher_placement_test.go` `TestSR205LauncherRefusesPosition`; `internal/cli/launcher_placement_e2e_test.go` `TestTheLauncherRefusesPosition`, `TestTheLauncherPutsTheAIInTheOrganizationItIsAskedFor`; `internal/cli/orgs_test.go` `TestOrgsOfOneOrganizationListsOnlyIt`; `internal/agents/placement_test.go` `TestFolderIDFromHeaderReadsThePlacementHeaders`; `internal/api/retired_org_chart_routes_test.go` `TestSR205OrgChartAndPositionRoutesAreGone`; `internal/api/agent_placement_test.go` `TestSR205APositionHeaderIsIgnored`; `internal/api/agent_orgs_test.go` `TestAgentOrgsTellsEveryRoleWhereItStands` |
| SR2-06 | migration `0040` (`agent_instructors`); `internal/store/queries/instructors.sql`; `internal/orgs/authz.go` `Links`, `LoadLinks` | `TestSR215Migration0040ConvertsGuestsPositionsAndCreators`; `internal/orgs/instructors_test.go` |
| SR2-07 | `internal/orgs/authz.go` `Trusts` (host or instructor), used by `internal/delivery`, `internal/agents`, `internal/connectors/inbound.go`, `internal/schedules`, `internal/rooms/secret.go`; `internal/access/access.go` `CanSeeAI` (instructors see the AI) | `internal/orgs/authz_test.go` `TestSR207TrustsTheHostAndTheInstructorsOnly`; `internal/connectors/inbound_test.go` `TestInboundTrustIsWhoManagesTheOperator`; `internal/schedules/heartbeats_test.go` `TestTrustIsDecidedAtDelivery`; `internal/access/access_test.go` `TestCanSeeAI`; `internal/api/delivery_trust_test.go` `TestSR207LivePushIsTrustedOnlyFromTheHostAndInstructors`, `TestSR207BacklogIsTrustedOnlyFromTheHostAndInstructors`, `TestSR207HistoryIsTrustedOnlyFromTheHostAndInstructors`, `TestSR207TrustFollowsTheInstructorsAtDeliveryTime`, `TestSR207ARemovedInstructorIsNotTrusted` |
| SR2-08 | migration `0040` (creator chain → instructors) | `TestSR215Migration0040ConvertsGuestsPositionsAndCreators` |
| SR2-09 | `internal/agents/placement.go` `place` (`instructorIID`), `checkStart` (existing AI: nothing) | `TestSR209ParentBecomesTheInstructorOfANewAIOnly`; `TestTheLauncherStartsAWorkerUnderAParentAI`; `internal/api/agent_placement_test.go` `TestSR209StartWithAParentAIMakesTheParentAnInstructor` |
| SR2-10 | `internal/orgs/instructors.go` `Instructors`, `AddInstructor`, `RemoveInstructor`; `internal/api/instructors_handlers.go`; `internal/intelligences/service.go` `instructedBy`; web `features/manage-instructors` | `internal/orgs/instructors_test.go` `TestSR210OnlyTheHostChangesInstructors`, `TestSR210AddInstructorChecksTheCandidate`; `internal/intelligences/service_test.go` `TestAIProfileTellsWhatTheCallerMayDo`; `internal/api/instructors_handlers_test.go` `TestSR210ListingTheInstructorsOfAnAI`, `TestSR210TheHostAddsAndRemovesInstructors`, `TestSR210OnlyTheHostChangesTheInstructors`, `TestSR210InstructorRefusals`, `TestSR210TheProfileAndTheLiveSessionFollowTheInstructors` |
| SR2-11 | `internal/agents/gateway.go` `placement`, `welcome` (`instructors`, deprecated `managers`); `internal/channel/link.go` `identityText`, `trustedText`; `internal/channel/instructions.go` | `internal/orgs/instructors_test.go` `TestSR210OnlyTheHostChangesInstructors` (fresh welcome event); `internal/api/agent_welcome_org_test.go` `TestSR211WelcomeTellsTheOrganizationAndTheInstructors`, `TestSR211WelcomeOfAnAIOnlyItsHostInstructs`, `TestSR211WelcomeListsInstructorsWhateverTheOrganizations`; `internal/channel/identity_org_test.go` `TestSR211IdentityNoticeNamesTheHostAndTheInstructors` |
| SR2-12 | `internal/orgs/authz.go` `CanDeleteAI`; `internal/agents/lifecycle.go` `authorizeDelete`; `internal/orgs/members.go` `remove`, `memberRows` | `internal/orgs/authz_test.go` `TestSR212CanDeleteAIIsTheHostOrAHomeAdmin`; `internal/orgs/roster_test.go` `TestSR212CanRemoveIsForAdmins`; `internal/orgs/members_test.go` `TestSR212RemoveIsForAdmins`, `TestListMembersShowsTheProfileThePermissionAndTheRights`; `internal/agents/delete_test.go` `TestSR212DeleteIsTheHostsOrAHomeAdmins`; `internal/intelligences/service_test.go` `TestProfileListsTheMembershipsTheCallerShares`; `internal/api/delete_identities_test.go` `TestSR212DeletingAMemberOfAnOrganization`, `TestSR212DeletingAnAIOfAnOrganization`, `TestSR212TheHostDeletesTheWorkersAndAnInstructorCannot`; `internal/api/org_members_handlers_test.go` `TestSR212TheMembersListSaysWhoMayDeleteWhom` |
| SR2-13 | `internal/orgs/authz.go` `Manages` (host or user instructor); `internal/intelligences/service.go` `rights`; `internal/agents/lifecycle.go` `managedAgent`; `internal/agents/registry.go` `authorize`; `internal/access/access.go` `managesAI`; `internal/orgs/invitations.go` `mayAnswer` | `internal/orgs/authz_test.go` `TestSR213ManagesIsTheHostAndTheUserInstructors`; `internal/intelligences/service_test.go` `TestUpdateAuthorization`, `TestCanSeeAndCanEdit`; `internal/agents/delete_test.go` `TestEndSessionClosesTheLiveSessionOfWhoeverIsManagedByTheCaller`; `internal/access/access_test.go` `TestDMRoomLevel`, `TestDMViewFollowsTheLinks`; `internal/orgs/invitations_test.go` `TestAnAIInvitationIsAcceptedByItsManagers`; `internal/api/instructors_handlers_test.go` `TestSR213AUserInstructorEditsTheAIAndAnAIInstructorDoesNot`; `internal/api/agents_handlers_test.go` `TestSR213TheHostAndUserInstructorsManageWhateverTheOrganization`; `internal/api/leave_and_end_session_test.go` `TestSR213TheHostAndAUserInstructorEndALiveSession` |
| SR2-14 | web `pages/contacts` (People and Hosted by me removed) | web unit tests |
| SR2-15 | migration `0040` runs in `Store.Migrate` after `backupBeforeMigrate`, in its one transaction | `internal/store/simple_identity_migration_test.go` `TestSR215Migration0040IsBackedUpAndAtomic`, `TestSR215Migration0040ConvertsGuestsPositionsAndCreators` |
