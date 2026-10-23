# Organizations Contract Notes

Binding for later tasks, the web app and the Verifier. Each task adds its section to this file.
Where a section refines the design, it says so.

## T1: Migration 0019, the organization schema (ORG-02, OUPG-01)

- **File**: `internal/store/migrations/0019_organizations.sql`, exactly the design's Data Model.
  Additive (AD-012): no table is rebuilt, and every new reference to an existing table is an
  `ADD COLUMN` with a NULL default.
- **Tables**:
  - `organizations (id, name, slug, avatar_sha256, created_by, created_at, updated_at)`; `slug`
    is `COLLATE NOCASE` with a unique index, so `Acme` and `acme` are one address and a lookup by
    slug ignores case.
  - `positions (id, org_id, name, parent_id, created_at)`; `name` is `COLLATE NOCASE` and unique
    per organization (`positions_name`), two organizations can each have "Admin". `org_id`
    cascades; `parent_id` is `ON DELETE SET NULL`, so a child of a deleted position becomes a root.
  - `org_members (org_id, iid, permission, position_id, inviter_iid, joined_at)`; the primary key
    `(org_id, iid)` is the one membership, hence at most one position, per identity and
    organization. `permission` is checked against `admin|member|guest`. `iid` cascades from the
    intelligence, `position_id` and `inviter_iid` are `ON DELETE SET NULL`.
  - `org_invitations (id, org_id, invitee_iid, permission, position_id, invited_by, created_at,
    expires_at)`; `UNIQUE (org_id, invitee_iid)` is the one pending invitation per invitee.
    `invited_by` has no foreign key: it is history, and an invitation outlives its inviter's
    intelligence.
- **Columns on existing tables**: `agents.org_id` (the home organization) and `agents.creator_iid`,
  `rooms.org_id` (NULL for a DM) and `rooms.creator_iid`, `connectors.org_id`, and
  `users.default_org_id` (`ON DELETE SET NULL`). `creator_iid` is `ON DELETE SET NULL`.
  `agents.org_id`, `rooms.org_id` and `connectors.org_id` have **no** `ON DELETE` action: deleting
  an organization that still owns an AI, a group or a connection fails in the database (belt and
  braces for ORG-06).
- **What the database does not enforce** (services do): a member's `position_id` and a position's
  `parent_id` belong to the same organization; `inviter_iid` is set only for guests; an AI's
  `agents.org_id` is an organization it is a member of; a connection's operator belongs to the
  connection's organization (ORG-07).
- **Indexes**: `organizations_slug` (unique), `positions_name` (unique), `positions_parent`,
  `org_members_iid`, `org_members_inviter` (partial), `org_members_position`,
  `org_invitations_invitee`, `agents_org`, `rooms_org` (partial, `org_id IS NOT NULL`),
  `connectors_org`.
- **Generated models**: `db.Agent{OrgID, CreatorIid}`, `db.Room{OrgID, CreatorIid}`,
  `db.Connector{OrgID}`, `db.User{DefaultOrgID}` (all `sql.NullString`), and the new
  `db.Organization`, `db.Position`, `db.OrgMember` and `db.OrgInvitation`. Queries that return
  those tables with `*` gained the fields, no call site had to change.
- **Queries** (`queries/orgs.sql`): `CreateOrganization`, `GetOrganization`,
  `GetOrganizationBySlug`, `CreatePosition`, `CountPositionName`, `CreateOrgMember` and
  `GetOrgMember`. Each later task adds the queries it needs to the same file.

## T2: Upgrade hook 0019 (OUPG-01, ORG-02)

- **Where**: `store.hooks["0019_organizations"]` = `backfillOrganizations`
  (`internal/store/organizations_backfill.go`). It runs in the migration transaction right after the
  SQL of 0019 (AD-012, MIG-05), raw SQL only, and is set-based where it can be.
- **Steps, in this order**:
  1. **Personal organizations**: for each user, oldest first (`created_at`, then `id`; disabled
     users included), one organization with `name` = the intelligence's display name, else the
     username (control characters and invalid UTF-8 dropped, trimmed, cut to **100 code points**),
     `slug` = the first free slug derived from the **username** (`handles.AllocateSlug`),
     `created_by` = the user, the root position `Admin` (no parent), the user as `admin` holding it
     (`joined_at` = the user's `created_at`) and `users.default_org_id` set to it.
  2. **AIs**: `agents.org_id` = the owner's organization, `agents.creator_iid` = the owner, and a
     `member` membership with no position and no inviter (`joined_at` = the agent's `created_at`).
     The owner therefore manages them as their creator (POS-03); nobody else does.
  3. **Groups**: `rooms.org_id` and `rooms.creator_iid` for rooms of kind `group`. A DM keeps both
     NULL (ORG-02).
  4. **Connections**: `connectors.org_id` = the owner's organization. Operators are not touched:
     an operator was always an AI the owner owns (CID-01), so it already belongs to the
     connection's organization (ORG-07).
  5. **Grantees**: every user with a grant (any level, any of group, AI or connection) on a
     resource becomes a `member` with no position of the **resource owner's** organization;
     `joined_at` is the earliest such grant. A user granted `post` or `manage` on a group also
     becomes a `user` room member (`added_by` = the granter, `created_at` = the grant's). A `view`
     grantee does not become a room member and loses the read-only view (design R8). Not converted:
     grants to everyone (`*`), grants on a DM, grants whose resource or user no longer exists, and
     the owner's own grant.
  6. **Group members**: every user and AI member of a group that is not a member of the group's
     organization joins it as `member` (`joined_at` = the earliest membership). An AI keeps its own
     home organization too. Group memberships are never removed.
  7. **Grants**: `DELETE FROM grants`. The table stays until 0020 (T52) and stays writable, but
     nothing in it means anything after the upgrade.
- **Slug and name helpers** are in `internal/handles` (the store cannot import `orgs`):
  - `handles.OrgSlug(name)` is `handles.Slug` (lower case, accents folded, runs of other characters
    become one `-`, trimmed, at most 36 bytes) or `org` when nothing is left. A slug may start with
    a digit.
  - `handles.AllocateSlug(taken, base)` is the first free slug among `base`, `base-2`, `base-3`, …
    (stem cut so the result stays within 40). No word is reserved (`all`, `partyline` are fine).
  - T5 adds the validation of a slug an admin types.
- **Not done on purpose**: no history is touched (messages, attachments, cursors, people, DMs); the
  owner of a group is **not** added as a room member (the creator manages and posts, T22); a user
  without an intelligence fails the upgrade on a foreign key (impossible after 0015: accounts and
  intelligences are created together).
- **Existing tests adapted** (the repository's own isolation pattern, `MigrateBefore`): the tests
  whose subject is an older migration now stop before 0019 because 0019 consumes grants and adds
  room members (`TestConnectionProxyUpgradeKeepsHistoryAndDropsTheRest`,
  `TestPlatformQueueMigrationGivesExistingConnectorsTheDefaults` stop at 0018,
  `TestProjectsBecomeAgentMembers` at 0016). `TestUpgradeV03KeepsEveryRow` still runs the whole
  upgrade; its only change is that the grants table is empty afterwards.

## T3: Chart rules (POS-02, POS-03, POS-05)

- **Package** `internal/orgs` (`chart.go`), pure: no I/O, no dependency on another partyline package.
  `Permission` is `admin|member|guest` (`PermissionAdmin`, `PermissionMember`, `PermissionGuest`,
  `Permission.Valid()`).
- **Types**, as in the design: `Member{IID, Permission, PositionID, CreatorIID, InviterIID}` (empty
  string = none), `Position{ID, Name, ParentID}` and
  `Chart{OrgID, Positions map[string]Position, Members map[string]Member}`. A `Chart` is a snapshot;
  nothing in it changes after it is built.
- **`(*Chart).Manages(a, b)`**: false when `a == b`, when `a` is not a member, or when `b` is not.
  Otherwise it walks `b` and then every member `b` answers to, transitively, and is true as soon as
  `a` is one of those (not `b`) or `a`'s position is **strictly above** that member's. A member
  answers to:
  - its **creator**, only while it holds **no position**;
  - its **inviter**, only while its permission is `guest` (a stale inviter on a member or admin is
    ignored).
  The walk follows members only (a creator or inviter who left the organization ends the chain
  there) and visits each member once (a creator or inviter cycle ends it). The `admin` permission
  gives no management.
- **`(*Chart).Above(pa, pb)`**: `pa` is a proper ancestor of `pb`. Empty ids, unknown ids and
  `pa == pb` are false. The walk is bounded, so a corrupt parent cycle ends it.
- **`(*Chart).HasCycle(positionID, newParentID)`**: true when the new parent is the position
  itself or one of its descendants; an empty `newParentID` (a root) is false. Callers load the chart
  in the transaction that changes the parent (POS-02).
- **`(*Chart).CanDelete(a, b)`**: false for `a == b` or when either is not a member; true when `a`
  is an `admin`, or when `b` is a `guest` and `Manages(a, b)`. Which means "delete" removes is the
  caller's business (POS-06).
- **`(*Chart).ManagersOf(b) []string`** (identity ids): exactly the `a` with `Manages(a, b)`,
  **nearest first**. Distance = steps from `b`: a creator or inviter is one step from the member it
  answers to, and the holders of the position above a member's are one step further, and so on
  up the chart. The same manager reached two ways counts once at the shorter distance. Ties break by
  id (ids are UUIDv7, so the older identity first). A root holder, a stranger, or a member only
  admins reach has none. The welcome frame (T33) uses the first 10.
- **`(*Chart).ManagedBy(a) map[string]bool`**: the set of `b` with `Manages(a, b)` (only true entries;
  empty for a non-member). `Manages`, `ManagersOf` and `ManagedBy` are pinned to agree for every pair
  by `TestManagesManagersOfAndManagedByAgree`.
- **`(*Chart).IsAdmin(iid)`** and **`(*Chart).Member(iid)`** are plain lookups.

## T4: Chart loader and Authz facade (POS-03, POS-10, CHART-02)

- **Deviation from the design (import direction)**: the design has `Authz` reuse `access.ErrNotFound`
  and `access.ErrForbidden`. Phase 5 (T22) makes `access` ask `orgs.Authz`, so `orgs` cannot import
  `access` (a cycle). The sentinels now **live in `orgs`** (`orgs.ErrNotFound`, `orgs.ErrForbidden`)
  and `access.ErrNotFound` / `access.ErrForbidden` are the same values (`access` imports `orgs`;
  `TestAccessErrorsAreTheOrganizationErrors`). Every existing `errors.Is(err, access.ErrNotFound)`
  and the API's 404/403 mapping keep working, and a service may return either name. `orgs` must
  never import `access`, `intelligences`, `rooms`, `users`, `agents`, `conversations`,
  `connectors`, `delivery` or `live`.
- **`orgs.Load(ctx, q *db.Queries, orgID) (*Chart, error)`**: two queries (`ListOrgPositions`,
  `ListOrgChartMembers`, which joins `agents.creator_iid`). `q` may belong to a transaction, so a
  rule is checked on the state of that transaction (POS-02, OMEM-05). An organization that does not
  exist loads as an empty chart (no error); callers that must tell the difference ask the table.
- **`orgs.NewAuthz(st *store.Store) *Authz`**, built by the app as `app.App.Authz`. Methods (all
  take `ctx` first and return `error` last):
  - `Chart(orgID) (*Chart, error)`: reads the chart in one read snapshot, every time.
  - `Member(orgID, iid) (Member, bool)`: one row, with the creator of an AI.
  - `RequireMember(orgID, iid)`: `ErrNotFound` unless a member with any permission (guests included).
  - `RequireAdmin(orgID, iid)`: `ErrNotFound` for an outsider, `ErrForbidden` for a member or guest.
  - `Manages(actor, target, orgID) (bool)`, `CanDelete(actor, target, orgID) (bool)`: the chart rules
    of one organization; false without a query when either id is empty or they are equal.
  - `ManagesAnywhere(actor, target) (bool)`: true when `Manages` holds in any organization both
    belong to.
  - `Trusts(authorIID, agentID) (bool)`: `Manages(author, agent)` in the AI's **home** organization
    (`agents.org_id`) and in no other; false for an empty id, an unknown AI or an AI with no home.
  - `SharesOrg(a, b) (bool)`: a common organization with any permission.
  - `Session() *Session`: the same questions plus `Chart`, memoized for one operation.
  - `Loads() int64`: how many charts the facade has read, sessions included. This is the "test
    hook" of the design: a test reads it before and after an operation to prove how many charts the
    operation loaded.
- **`Session`** remembers the chart of each organization and the home organization of each AI it is
  asked about, for its whole life, and is safe for concurrent use. It is a snapshot: a change made
  after it read something is not seen by it (a new session sees it). Never keep one across requests;
  there is no cross-request cache.
- **`authn.Principal.IID()`**: `AgentID` for an agent principal (possibly `""`: an agent token that
  names no AI acts as nobody, never as its host user), `UserID` otherwise.
- **`events.OrgChanged{OrgID}`**: published after the commit of any change to an organization (T5
  onward). The live hub ignores it until T32.
- **Queries** added to `queries/orgs.sql`: `ListOrgPositions`, `ListOrgChartMembers`,
  `GetOrgChartMember`, `ListSharedOrgIDs`, `CountSharedOrgs`, `GetAgentOrg`, and the placement
  setters `SetAgentHome` and `SetAgentCreator` (test and testkit support).

## T5: Organization service (ORG-01, ORG-06)

- **`orgs.NewService(orgs.Deps{Store, Authz, Bus, Audit, Blobs, Now})`**, built by the app as
  `app.App.Orgs` (`Authz`, `Audit` and `Now` default; `Bus` is optional; `Blobs` is needed for
  avatars). `Service.Authz()` returns the facade it asks. T6-T8 add the chart, member and invitation
  methods to the same type.
- **Acting identity**: every method takes the IID it acts as first (a user, or an AI acting as
  itself) and authorizes it through `Authz`: `ErrNotFound` for an outsider, `ErrForbidden` for a
  member or guest who is not an admin. Authorization comes **before** validation (a member who
  sends a bad name is told 403, not 422). Checks that guard a write are repeated inside the write
  transaction, so a demotion cannot interleave.
- **`Membership`** = `db.Organization` (embedded: `ID`, `Name`, `Slug`, `AvatarSha256`, `CreatedBy`,
  `CreatedAt`, `UpdatedAt` in unix ms) + the viewer's `Permission` and `PositionID` (`""` = none). It
  is what `Create`, `Get`, `ListMine`, `Update` and `SetAvatar` return.
- **Methods**:
  - `Create(creatorIID, name) (Membership, error)`: the creator must be an **enabled user**
    (an AI, an unknown or disabled identity is `ErrForbidden`). The name is cleaned (control
    characters and invalid UTF-8 dropped, trimmed) and must have 1-100 **code points**, else a
    `*validation.Error` on field `name`. The slug is the first free one derived from the name
    (`handles.AllocateSlug`: `acme`, `acme-2`, …; `org` when nothing Latin is left). The creator
    becomes `admin` holding a new root position `Admin`. The returned `Membership` is the creator's
    own, so `PositionID` is the root position. Audit `org.create` `{name, slug}`, then `OrgChanged`.
  - `Get(viewerIID, orgID) (Membership, error)`: any member, guests included; otherwise
    `ErrNotFound` (also for an organization that does not exist).
  - `Resolve(ref) (db.Organization, error)`: an id first, then a slug ignoring case and surrounding
    spaces; `ErrNotFound` when neither matches. It says nothing about visibility: callers follow it
    with `Get` or an `Authz` check. This is what `--org` and the `{org}` path segments take.
  - `ListMine(iid) ([]Membership, error)`: every membership, guests included, by name (case
    insensitive) then id.
  - `Update(actorIID, orgID, UpdateInput{Name *string, Slug *string}) (Membership, error)`: admin
    only. Invalid fields are one `*validation.Error` naming `name` and/or `slug` and change nothing.
    A slug is lower-cased and must be 1-40 characters of `a-z0-9` in groups joined by single hyphens
    (`handles.ValidateSlug`). An address another organization has is `ErrSlugTaken` (409 `conflict`).
    An update that changes nothing writes, audits and announces nothing. Audit `org.update` with the
    changed fields.
  - `SetAvatar(actorIID, orgID, io.Reader) (Membership, error)`, `ClearAvatar(actorIID, orgID)
    error`, `Avatar(viewerIID, orgID) (*os.File, sha string, error)`: admin only to change; any member
    to read (`ErrNotFound` for an outsider or when there is no avatar). The image is processed by
    `avatars.Process` (PNG/JPEG/WebP up to 2 MiB → 256x256 PNG; `avatars.ErrInvalid` otherwise and the
    old avatar stays). Setting or clearing audits `org.update` `{avatar: true|false}` and announces; a
    clear of a missing avatar is silent.
  - `Delete(actorIID, orgID) error`: admin only. While an AI has the organization as its **home**
    (`agents.org_id`), or a connection or a group belongs to it, it returns a `*NotEmptyError{Agents,
    Connectors, Groups int64}` that matches `ErrNotEmpty` (409 `organization_not_empty`, counts in the
    details). DMs belong to no organization and never block. Otherwise the organization goes with its
    positions, members and invitations, the users' `default_org_id` that pointed at it is cleared, and
    `org.delete` `{name, slug}` is audited.
- **Errors**: `ErrNotEmpty`, `ErrSlugTaken` (new); `ErrNotFound`, `ErrForbidden` (T4).
- **Audit constants** (`internal/audit`): `ActionOrgCreate`, `ActionOrgUpdate`, `ActionOrgDelete` and
  the target kind `TargetOrganization = "organization"`; the entry's actor is the acting IID.
- **Events**: `events.OrgChanged{OrgID}` after every committed change (create, update, avatar,
  delete).
- **Retention**: an organization's avatar is a blob like an intelligence's. `rooms.blobReferenced`
  also asks `CountOrganizationAvatar`, so the sweep keeps it (`TestSweepKeepsTheAvatarOfAnOrganization`).
- **Helpers other tasks reuse**: `createOrg(ctx, q, orgParams)` (the organization, its root position
  and its admin, in the caller's transaction; T9 builds personal organizations with it),
  `requireAdmin(ctx, q, orgID, iid)` and `memberOf(ctx, q, orgID, iid)` (checks with a transaction's
  queries), `handles.ValidateSlug`.
- **Queries** added to `queries/orgs.sql`: `GetOrgMembership`, `ListOrgsOfMember`,
  `UpdateOrganization`, `SetOrganizationAvatar`, `CountOrganizationAvatar`, `CountOrganizationSlug`,
  `DeleteOrganization`, `CountOrgAgents`, `CountOrgConnectors`, `CountOrgGroups`.

## T6: Positions service (POS-01, POS-02, POS-09)

- **Methods on `*orgs.Service`** (`positions.go`); all are admin-only (`ErrNotFound` for an outsider,
  `ErrForbidden` for a member, guest or AI that is not an admin; authorization comes before
  validation) and announce `OrgChanged` after the commit:
  - `CreatePosition(actorIID, orgID, name, parentID) (Position, error)`: `parentID == ""` makes a
    root. The name is cleaned like an organization name and must have 1-60 **code points**
    (`*validation.Error` on `name`). It is unique in the organization **ignoring case**
    (`ErrPositionNameTaken`, 409 `conflict`); another organization may reuse it. A parent that is not
    a position of this organization (another organization's, or unknown) is a `*validation.Error` on
    `parent_id` (422). Audit `org.position_create` `{position_id, name, parent_id}`.
  - `UpdatePosition(actorIID, orgID, positionID, PositionUpdate{Name *string, ParentID *string})
    (Position, error)`: nil leaves a field alone; `ParentID` pointing at `""` makes a root. A
    rename to the same name in other letters is a rename (the position's own name is not a duplicate
    of itself); the same exact name is nothing. A move that would make the position its own
    ancestor (itself or a descendant as the parent) is `ErrCycle` (409 `position_cycle`), decided on
    the chart loaded **inside the transaction that moves** (`Load` with the transaction's queries), so
    two moves that are fine alone cannot both commit (`TestTwoMovesThatTogetherMakeACycleCannotBothSucceed`).
    A change that alters nothing writes, audits and announces nothing. A position of another
    organization, or one that does not exist, is `ErrNotFound`. Audit `org.position_update`
    `{position_id, name?, parent_id?}` with the changed fields.
  - `DeletePosition(actorIID, orgID, positionID) error` (POS-09): in one transaction its **holders**,
    its **child positions** and the **pending invitations** that carry it move to its parent
    position, or to none (holders) and to roots (children) when it was a root; then it is deleted.
    Audit `org.position_delete` `{position_id, name, moved_to, holders, children}` (`moved_to` is `""`
    for a root). The moves change who manages whom at once, since management follows the chart.
- **Errors**: `ErrCycle`, `ErrPositionNameTaken` (new).
- **Constants**: `MaxPositionNameLength = 60`; audit actions `ActionOrgPositionCreate`,
  `ActionOrgPositionUpdate`, `ActionOrgPositionDelete` (target kind `organization`, target id the
  organization, the position in the detail).
- **Queries** added to `queries/orgs.sql`: `GetPosition` (id + org), `GetPositionByName`,
  `UpdatePosition`, `MovePositionChildren`, `MovePositionHolders`, `MovePositionInvitations`,
  `DeletePosition`.

## T7: Membership service (OMEM-03, OMEM-04, OMEM-05, OMEM-06, OMEM-08, POS-04)

- **`orgs.AddMember(ctx, q *db.Queries, AddMemberInput{OrgID, IID, Permission, PositionID, InviterIID,
  Now}) error`** is the transaction helper invitations (T8) and placement (T10, T18) use. It refuses
  an identity that already is a member of the organization with any permission (`ErrAlreadyMember`, 409
  `already_member`), a permission that is not `admin|member|guest` (validation error on `permission`)
  and a position that is not the organization's (validation error on `position_id`). `InviterIID` is
  stored for a guest and ignored for any other permission. It audits and announces nothing: the
  caller records `audit.ActionOrgMemberJoin` (`org.member_join`, detail `by`: `invitation` or
  `start`) and publishes `OrgChanged` after its commit. The foreign key on `iid` still refuses an
  identity that does not exist.
- **Methods on `*orgs.Service`** (`members.go`). All return the chart's `Member` or an `error`, check
  their conditions inside the write transaction, and announce `OrgChanged` after the commit:
  - `SetPermission(actorIID, orgID, targetIID, perm) (Member, error)`: admin only; the target must
    be a member (`ErrNotFound`); a value that is not a permission is a validation error on
    `permission` (told to admins only: authorization first). **`ErrLastAdmin`** (409 `last_admin`,
    OMEM-05) when the target is the only admin and `perm` is not `admin`. A guest that becomes `member`
    or `admin` loses its inviter. The same permission again is nothing (no audit, no event). Audit
    `org.member_permission` `{iid, from, to}`.
  - `SetPosition(actorIID, orgID, targetIID, positionID) (Member, error)` (POS-04): `positionID ==
    ""` clears. An **admin** gives anyone any position of the organization or none; a position that is
    not the organization's is a validation error on `position_id`. **Anyone else** needs
    `Manages(actor, target)` and a position **strictly below the actor's own** (`Chart.Above`):
    clearing, the actor's own level, a higher or another branch's position, or a target it does not
    manage - itself, a peer, a superior - is `ErrForbidden`. An actor without a position (an inviter
    with no position) therefore positions nobody. An outsider actor or a target that is not a member is
    `ErrNotFound`. The same position again is nothing. Audit `org.member_position` `{iid, from, to}`.
  - `Remove(actorIID, orgID, targetIID) error` and `Leave(iid, orgID) error`. `Remove` with
    `actor == target` **is** `Leave`. Order of the checks: the actor is a member (`ErrNotFound`), the
    target is a member (`ErrNotFound`), actor may delete target (`Chart.CanDelete`, POS-05) or it is a
    leave (`ErrForbidden`), then **`ErrHomeOrganization`** (409 `home_organization`) when the target is
    an AI whose home (`agents.org_id`) is this organization - such an AI is deleted for good, never
    removed, and T21 routes it to `Registry.Delete` - then **`ErrLastAdmin`** for the only admin.
    Effects, in one transaction: the target leaves **only this organization's groups** (rooms of kind
    `group` with this `org_id`; DMs and other organizations' groups stay) and its membership row with
    its position; its `default_org_id` is cleared when it was this organization; the AIs of the
    organization and the people and AIs it created stay (their creator link is kept, but a creator who
    is not a member manages nothing, so only admins reach them); the guests it invited stay (the chain
    breaks). **OMEM-08 guard**: connections of this organization the target operated lose their operator
    (`connectors.operator_iid = NULL`), the target's platform items for them are deleted, and
    `events.ConnectorsOrphaned{IDs}` is published so the connector manager stops them (unreachable in
    normal use: operators are home AIs, which never leave; deleting an operator AI goes through
    `Registry.Delete`). Audit `org.member_leave` (self) or `org.member_remove` (by another) `{iid,
    groups, operator_of?}`. After the commit: `events.MembersChanged{RoomID}` per group left,
    `ConnectorsOrphaned` if any, `OrgChanged`.
- **Errors** (new): `ErrLastAdmin`, `ErrHomeOrganization`, `ErrAlreadyMember`.
- **Audit constants**: `ActionOrgMemberJoin`, `ActionOrgMemberPermission`, `ActionOrgMemberPosition`,
  `ActionOrgMemberLeave`, `ActionOrgMemberRemove`.
- **Note for T22/T32**: no `events.GrantsChanged` is published by these methods; the live hub learns of a
  membership change from `MembersChanged` and `OrgChanged` (T32 makes it re-evaluate visibility).
- **Queries** added to `queries/orgs.sql`: `UpdateOrgMemberPermission`, `UpdateOrgMemberPosition`,
  `DeleteOrgMember`, `DeleteOrgGroupMemberships`, `ClearOrgOperator`,
  `DeletePlatformItemsOfAgentOnConnector`, `ClearUserDefaultOrg`.

## T8: Invitations service (OMEM-01, OMEM-02, OMEM-04, OMEM-07)

- **Deviation (names)**: tasks.md calls the two list methods `ListForOrg` and `ListMine`, but T5 already
  owns `Service.ListMine` (organizations of an identity). The invitation lists are
  **`ListInvitations(actor, orgID)`** and **`MyInvitations(actor)`**; everything else keeps its name.
- **`Invitation`** (the view a client shows): `ID`, `OrgID`, `OrgName`, `OrgSlug`, `InviteeIID`,
  `InviteeHandle`, `InviteeName`, `InviteeKind` (`"user"` or `"ai"`), `Permission`, `PositionID`,
  `PositionName`, `InvitedByIID`, `InvitedByHandle`, `InvitedByName`, `CreatedAt`, `ExpiresAt`
  (`time.Time`, UTC).
- **Constants and errors**: `InvitationTTL = 14 * 24h`, `InvitationsPerHour = 30`; new sentinels
  `ErrContactNotFound` (404 `contact_not_found`), `ErrRateLimited` (429 `rate_limited`) and `ErrGone`
  (410 `gone`); `ErrAlreadyMember` (T7) is 409 `already_member`.
- **`Invite(actor, orgID, handle, perm, positionID) (Invitation, error)`**:
  - The actor must be a member, any permission, an AI included (an outsider is `ErrNotFound`).
    **Admin**: any permission and any position of the organization; an invalid permission or
    position is a validation error on `permission` or `position_id`. **Anyone else**: only
    `guest` with no position; anything else - another permission, a position, a value that is not a
    permission - is `ErrForbidden` (authorization before validation).
  - `handle` takes a leading `@`, any case and surrounding spaces. Only a **user** (enabled) or an
    **AI** is invited; a person (an external account), a disabled user, an empty or unknown handle is
    `ErrContactNotFound`. An identity that already is a member (any permission) is
    `ErrAlreadyMember`.
  - **One pending row per (organization, invitee)**: inviting again updates it in place (the id stays;
    permission, position, inviter, creation time and the 14 days start over). Someone who may only
    invite guests cannot replace a pending invitation for another permission (`ErrForbidden`); once
    that invitation has expired it no longer protects anything.
  - **Rate limit (30 per hour per organization)**: the count of `org.invite` audit entries with target
    the organization in the last hour (`created_at > now - 1h`: an entry exactly one hour old is out).
    The count, the invitation and its audit entry are one write transaction (a parallel burst of 45
    yields exactly 30), and being audit-backed it survives a restart. Guest invitations and
    re-invitations count; refused attempts do not. **It applies to invitations only**: an AI started
    with `--parent` joins through `AddMember` (T10, T18) and never counts.
  - Audit `org.invite` `{invitation, invitee, permission, position_id}`; after the commit
    `events.InvitationReceived{OrgID, InvitationID, InviteeIID}` (T15 turns it into the live
    `invitation_received` event for the invitee and, for an AI, its managers) and `OrgChanged`.
- **Answering** - `Accept(actor, invitationID) (Membership, error)`, `Decline(actor, invitationID)
  error`: the answerer is the invited **user** itself, or for an invited **AI** whoever manages it in
  its home organization (`Chart.Manages` there); an AI cannot answer for itself. Any other caller, and
  an unknown id, get `ErrNotFound` (404, nothing is revealed). `ErrGone` once `now >= expires_at`
  (accepted at 14 days minus 1 ms, gone at 14 days); the expired row stays until the retention job.
  - Accept becomes a member through `AddMember` with the invitation's permission and position; for a
    guest the **inviter is the one who invited** (`invited_by`), or none when that account no longer
    exists. A position deleted meanwhile has already moved the invitation to its parent (T6). The
    invitation is deleted and the audit has `org.invite_accept` and `org.member_join` (`by:
    "invitation"`, actor = whoever accepted). If the invitee became a member another way meanwhile,
    the stale invitation is deleted and the answer is `ErrAlreadyMember`. It returns the invitee's
    `Membership`.
  - Decline deletes it; audit `org.invite_decline`.
- **`Revoke(actor, orgID, invitationID) error`**: admin only (outsider `ErrNotFound`, member
  `ErrForbidden`); an invitation of another organization is `ErrNotFound`. Audit `org.invite_revoke`.
- **Lists** (expired ones are hidden, newest first): `ListInvitations(actor, orgID)` is admin only;
  `MyInvitations(actor)` is the actor's own plus those of **AIs the actor manages in their home
  organization**.
- **`PurgeExpiredInvitations(ctx) (int64, error)`** deletes the invitations with `expires_at <= now`
  (so exactly when they expire), audits each as `org.invite_expire`, announces `OrgChanged` per
  organization and returns the count. The app registers it as the retention task `org_invitations`
  (`rooms.RetentionTask`), which runs when the app starts and then daily.
- **Audit constants**: `ActionOrgInvite`, `ActionOrgInviteAccept`, `ActionOrgInviteDecline`,
  `ActionOrgInviteRevoke`, `ActionOrgInviteExpire`.
- **Queries** added to `queries/orgs.sql` and `queries/audit.sql`: `GetOrgInvitation`,
  `GetOrgInvitationOf`, `UpsertOrgInvitation`, `ListOrgInvitations`, `ListInvitationsForInvitee`,
  `ListInvitationsForAIsAround`, `DeleteOrgInvitation`, `DeleteExpiredOrgInvitations`,
  `CountAuditEntriesSince`.

## T9: Personal organization for new accounts and the default organization (ORG-04)

- **Every new account has a personal organization (AD-021)**, created in the same transaction as the
  account and its intelligence by all three paths: the first-run `Setup`, `AcceptInvite` and the admin's
  `CreateUser` (`users.Service.insertAccount`, now a method). A failure after the organization was
  written (the audit entry, for instance) leaves no account, no intelligence and no organization
  (`TestAnAccountAndItsPersonalOrganizationAreOneTransaction`).
  - **`orgs.CreatePersonal(ctx, q, PersonalParams{UserID, Name, Username, Now}) (Membership, error)`**:
    the organization is named `handles.PersonalOrgName(display name, username)` - the display name of the
    intelligence, else the username, control characters and invalid UTF-8 dropped, trimmed, cut to 100
    code points (the upgrade of T2 uses the same function, so a new account and an upgraded one are
    named alike) - at the first free address derived from the **username** (`bob`, `bob-2`), with the
    account as `admin` holding a new root position `Admin`, and it becomes `users.default_org_id`.
  - Audit: `org.create` `{name, slug, personal: true}`, target the organization, actor the new
    account, recorded by the users service (so a new account adds one more audit entry than before).
- **`orgs.DefaultFor(ctx, q, iid) (string, error)`**: the organization a new AI joins when `--org` is
  not given; `""` when there is none.
  - **A user**: the `default_org_id` they chose **if they still belong to it**; else the organization
    where they have been an **admin** the longest; else the one they have belonged to the longest, any
    permission (guest included). "Longest" is the smallest `joined_at`, ties by organization id.
  - **An AI**: its **home organization** (`agents.org_id`) and nothing else; an AI with no home has none,
    it is not guessed from its memberships.
  - An identity that is neither (a stranger) has none.
- **`(*Service).SetDefault(ctx, userIID, orgID) error`**: the user must be an **enabled user** and a
  **member of the organization with any permission**; anything else - not a member, no such organization,
  an empty id, an AI, an unknown identity - is `ErrForbidden` (T17 answers 403 and tells nothing about
  organizations the user is not in). No audit entry, no event: it is a setting of the account (T17 returns
  it in `me`). `Leave`/`Remove` (T7) clear `default_org_id` when the user leaves that organization, and
  deleting the organization (T5) clears it by the foreign key.
- **Existing tests changed**: `TestAuditPagination` expects one more audit entry (the first account's
  `org.create`). Any test that counts audit entries or organizations after creating accounts must now
  account for one personal organization per account (T11's testkit offers `PersonalOrg()`).
- **Queries** added to `queries/orgs.sql`: `SetUserDefaultOrg`, `GetUserDefaultOrg`, `GetOldestOrgOf`.

## T10: Default placement of new AIs (ORG-02, ORG-04)

- **Where**: `agents.Registry.createFolderAgent` (`internal/agents/folder.go`), the only place a folder
  AI is created, for the live WebSocket and for tools-only calls alike (both resolve the folder through
  `ResolveFolder`). In the creation transaction a new AI gets:
  - `agents.org_id` = `orgs.DefaultFor(host)` (the host's chosen default if they still belong to it, else
    the organization they have administered the longest, else the one they belong to longest, T9) and
    `agents.creator_iid` = the host (`agents.owner_id` stays the host user, AD-019);
  - an `org_members` row through `orgs.AddMember`: permission `member`, **no position**, no inviter, so
    the host manages it as its creator (POS-03);
  - an audit entry `org.member_join` `{iid, by: "start", permission: "member"}`, target the organization,
    actor the host (recorded when the registry has an audit log, which the app always gives it).
  After the commit the registry publishes `events.OrgChanged{OrgID}`.
- **No organization at all**: a host whose `DefaultFor` is empty (they deleted or left every
  organization) cannot start an AI. `agents.ErrNoOrganization` is a `*httpx.Error`: **422
  `organization_required`**, field `org` (message "you belong to no organization: create or join one
  first"). It is rendered as is by the WebSocket handshake and by every REST route that resolves the
  folder, and nothing is created (no AI row, no profile, no membership). **`dto.ErrorCodeOrganizationRequired`
  is defined here**; T18 reuses it for an unknown `--org`.
- **Idempotence**: a second session in a folder that has an AI resolves it and adds nothing (no second
  membership, the first `joined_at` stands, no second audit entry). Adopting a v0.3 identity (MIG-02)
  places nothing: it already has its organization from the upgrade.
- **What T18/T19 add on top**: the `--org`, `--position` and `--parent` headers, `guest` for an AI started
  by an AI, and the checks of ORG-05/POS-07. This task fixes the defaults (`member`, the host as creator,
  no position) and the plumbing; T18 replaces the `DefaultFor` call by its placement decision. Placement
  never goes through invitations, so the 30-per-hour limit (T8) does not apply to AIs started with
  `--parent`.
- **Query**: `InsertFolderAgent` now takes `OrgID` and `CreatorIid`.
- **Test fixtures**: the `agents` package fixture now creates accounts the way the app does (intelligence
  with the handle `user-<name>`, plus the personal organization), because an AI needs a host with an
  organization. `testkit.NewLegacyAI` still creates an AI without an organization (a v0.3 identity as
  the upgrade leaves it is placed by the upgrade, not here); T11's helpers can put one in an organization.

## T11: Test harness for organizations (OMEM-04, POS-05, CHART-02)

`internal/testkit/orgs.go`. Phases 3-6 build their authorization matrices on these.

- **`(*Client).PersonalOrg() db.Organization`**: the first organization the account created, its
  personal one (AD-021), not whatever its default is now.
- **Store-level setup, no authorization, all announce `OrgChanged` where it matters**:
  - `env.AddPosition(org, name, parent) string` returns the position id (`parent == ""` for a root).
  - `env.AddMember(org, iid, permission, position)` and `env.AddGuest(org, iid, inviter, position)` go
    through `orgs.AddMember`, so an identity that already is a member, a bad permission or a foreign
    position fails the test. Use them for users and AIs alike.
  - `env.SetCreator(agent, iid)` re-points the creator of an AI.
  - `env.NewAIIn(owner, org, name) string` creates the owner's AI of `FolderOf(name)` with `org` as its
    home (the owner must be a member): it makes `org` the owner's default for the creation and puts the
    owner's default back afterwards. Idempotent per (owner, name), like `NewAI`: an AI that exists keeps its
    home. `NewAI` itself still goes to the owner's default organization (T10).
- **`env.OrgRoles(name) *OrgRoles`**: an organization called `name` (lower case, up to 20 characters: it
  prefixes the usernames) with a logged-in client in every role of the design's matrices; it can be called
  once per name in an environment (`acme`, then `globex`).

  | Field | Who | Position | Relations |
  | --- | --- | --- | --- |
  | `Admin` | `admin` of the organization, created it | `Admin` (root) | manages everyone positioned below it, i.e. `Manager`, `Target`, `Peer` and, through `Manager`, `TargetAI` |
  | `Manager` | `member` | `Manager`, child of `Admin` | manages `Target`, `Peer` (both below it) and `TargetAI` (it created it); manages none of `Admin`, `Member`, `Inviter`, `Guest` |
  | `Target`, `Peer` | `member` | both `Worker`, child of `Manager` | peers: neither manages the other, nor `Manager`; `Manager` and `Admin` manage both |
  | `Member` | `member` | none | no relation to anyone |
  | `Inviter` | `member` | none | invited `Guest`, so manages it |
  | `Guest` | `guest` | none | `InviterIID` = `Inviter` |
  | `Outsider` | ordinary user, **not** a member | - | has its own personal organization |
  | `InstanceAdmin` | instance admin account `<name>-root`, **not** a member | - | an admin of the instance gets no access (AD-016) |

  Also `TargetAI` (id of an AI whose home is the organization: `member`, no position, created and hosted
  by `Manager`), `Org` (`db.Organization`) and the position ids `AdminPosition`, `ManagerPosition`,
  `WorkerPosition`. `r.All()` lists `RoleClient{Name, Client}` for `admin`, `manager`, `target`, `peer`,
  `member`, `inviter`, `guest`, `outsider` and `instance_admin`, for tests that loop over every role. An AI
  acts as itself with `r.Manager.Token(dto.TokenKindAgent, "t").AsAgent(name+"-ai")` (the folder of
  `TargetAI`); `Manager` is its host.
- **Accounts created by the kit now have a personal organization** (T9), and `NewAI` places AIs (T10); tests
  of earlier phases that count audit entries, organizations or memberships must expect that.
- **Where the sentinels end up** (T12-T17 map them; listed here so the statuses agree):

  | Go error | HTTP |
  | --- | --- |
  | `orgs.ErrNotFound` (= `access.ErrNotFound`) | 404 `not_found` |
  | `orgs.ErrForbidden` (= `access.ErrForbidden`) | 403 `forbidden` |
  | `*NotEmptyError` / `ErrNotEmpty` | 409 `organization_not_empty`, counts in `details` |
  | `ErrLastAdmin` | 409 `last_admin` |
  | `ErrHomeOrganization` | 409 `home_organization` |
  | `ErrAlreadyMember` | 409 `already_member` |
  | `ErrCycle` | 409 `position_cycle` |
  | `ErrSlugTaken`, `ErrPositionNameTaken` | 409 `conflict` |
  | `ErrGone` | 410 `gone` |
  | `ErrRateLimited` | 429 `rate_limited` |
  | `ErrContactNotFound` | 404 `contact_not_found` |
  | `*validation.Error` (`name`, `slug`, `permission`, `position_id`, `parent_id`) | 422 `invalid` with `fields` |
  | `agents.ErrNoOrganization` (an `*httpx.Error`) | 422 `organization_required` |
- **Query** added: `GetPersonalOrg`.

## Fixes after the T1-T11 review

- **A guest host's AI is a guest.** When the host of a new folder AI is only a guest of the AI's home organization, the AI joins as a `guest` with the host as its inviter, never as a `member` (which only an admin could delete). See `internal/agents/folder.go` and `TestAGuestHostsNewAIJoinsAsTheirGuest`.
- **Admin invitations lapse with the inviter's admin right.** On accept, an invitation for `member` or `admin` needs its inviter to still be an admin of the organization. Otherwise the invitation is deleted and the answer is `ErrGone`. A guest invitation whose inviter left still stands, and the guest then has no inviter.
- **Re-inviting a guest keeps the design's rule.** The latest inviter of a pending guest invitation becomes responsible for the guest. The invitee sees who invites them before accepting, so this is not a takeover. The review's third finding is declined.

## T18: Placement headers on creation (ORG-04, ORG-05, POS-07, POS-08)

- **Headers** (`dto.HeaderOrg`, `HeaderPosition`, `HeaderParent`): `X-Partyline-Org` (a slug or an id),
  `X-Partyline-Position` (the position's name, percent-encoded with `url.PathEscape`) and
  `X-Partyline-Parent` (an `@handle`, a handle or an id). `agents.FolderID` gains `Org`, `Position` and
  `Parent`; `FolderIDFromHeader` trims them and a blank value is no value. A position that is not valid
  percent-encoding is **422 `position_unknown`** (field `position`), like a folder that is not is
  `folder_invalid`. Every route that reads the folder headers reads these too (WebSocket, tools-only calls,
  `GET /agent/session`, `POST /agent/register`).
- **`agents.place(ctx, q, host, id)`** (`internal/agents/placement.go`) decides where a *new* AI goes, in the
  creation transaction (`createFolderAgent`, so it runs for the live WebSocket and for tools-only calls alike).
  Order of the decisions, and the refusal each can give (all `*httpx.Error`, so the WebSocket handshake renders
  them like the REST routes):
  1. **Creator (POS-08).** `Parent` names an identity by id first, then by handle (case-insensitive, `@`
     optional). It counts only when it is an **AI hosted by the token holder** (`agents.owner_id` = host); any
     other parent (unknown, a user - the host included -, a person, an AI hosted by someone else) is ignored
     without a word and the token holder is the creator.
  2. **Home organization (ORG-04).** `Org` when given (`orgs.ResolveRef`: id first, then slug ignoring case);
     one that does not exist is **422 `organization_required`** (field `org`). Else `orgs.DefaultFor(creator)`:
     the host's default, or an AI parent's home organization; none at all is the T10 `ErrNoOrganization`.
  3. **ORG-05.** The *creator* must be a member of the home organization - the parent AI when there is one,
     whatever the host is - else **403 `forbidden`**.
  4. **Position (POS-07).** `Position` is looked up in the home organization, ignoring case (the NOCASE
     unique index decides what "the same name" is); a position that does not exist there, including one that
     exists only in another organization, is **422 `position_unknown`** (field `position`). It is allowed when
     the creator is an `admin` there or the creator's own position is **strictly above** it (`Chart.Above`);
     otherwise **403 `forbidden`**. The **creator's** standing counts, never the host's: an admin host whose
     parent AI sits lower cannot place the worker above the parent. A creator without a position (and not
     admin), and a guest creator, therefore cannot hold a position on a new AI.
  5. **Permission (ORG-04).** The AI joins as a **`guest` with `inviter_iid` = its creator** when the creator is
     an AI *or* is itself only a guest of the organization (the T10 fix), else as a `member`. It holds the
     requested position in either case. Nothing is created when any step refuses (no AI row, profile,
     membership or audit entry; `TestStartInAnOrganizationTheCreatorIsNotInIsRefused` and friends count them).
- **Audit.** `org.member_join` `{iid, by: "start", permission}` - `permission` is now what the AI really joined as
  (T10 wrote `member` even for a guest host's AI) - plus `creator` when an AI parent started it and
  `position_id` when it holds one. The actor is the token holder.
- **v0.5 CLIs** send none of the headers: the defaults of T10 apply unchanged (pinned by
  `TestSessionsWithoutPlacementHeadersKeepTheDefaults`).
- **Helpers.** `orgs.ResolveRef(ctx, q, ref)` is the transaction-friendly form of `Service.Resolve` (which now
  delegates to it). `testkit.Folder` gains `Org`, `Position` and `Parent` and sends them as the CLI would.
- **Error codes.** `position_unknown` is declared in the one `ErrorCode` group of `internal/api/dto/common.go`,
  next to the folder codes (a second group would make tygo emit a second `ErrorCode` union). Each later task of
  Phase 4 adds its own codes to that group.

## T19: Start rules for an existing AI (ORG-04, POS-07)

- **Where.** `agents.Registry.ResolveFolder` applies them to an AI that already exists - found before any
  transaction, found inside the creating transaction (it was created while this call waited) or adopted (MIG-02)
  - through `checkStart` (decides, writes nothing), `restart` (the fast path) and `startExisting` (inside a
  transaction), all in `internal/agents/placement.go`. A refusal changes nothing, an adoption included: the rules
  run in the adopting transaction. `LookupFolder` (`GET /agent/session`) still changes nothing and applies no
  rules.
- **`--org`** may only name the AI's home organization (id or slug, any case). Any other value - another
  organization of the host, one the host is not in, one that does not exist - is **409
  `organization_mismatch`** with `Details` `organization` (the home slug, what `--org` takes) and
  `organization_name` (`dto.MismatchDetailOrganization`, `MismatchDetailName`), and a message that names it. An
  unknown `--org` on an existing AI is therefore a mismatch, not `organization_required` (that is for creation).
- **`--parent`** is ignored: the creator is fixed when the AI is created and the token holder acts for
  themselves (design R6, "`--parent` counts at creation only"). The design's sentence "the creator rule of step 1
  holds for the starter" is read that way too: the starter is the token holder. An admin host therefore can move
  an AI whatever AI they name as parent.
- **`--position`**, when it names a position other than the one the AI holds, moves it iff the token holder is an
  **admin** of the home organization, or **manages the AI** there *and* holds a position **strictly above** the
  new one - the rule of `orgs.Service.SetPosition` (POS-04), which the registry cannot call inside the starting
  transaction. Otherwise **403 `forbidden`** and the position stays; a position the organization does not have is
  **422 `position_unknown`** (also for a token holder who may not move anything). The position the AI already
  holds is nothing, whoever asks (no write, no audit, no event); a start without `--position` never clears it.
  A move writes `org_members.position_id`, audits `org.member_position` `{iid, from, to, by: "start"}` (the actor is
  the token holder) and publishes `OrgChanged` after the commit.
- **Cost.** A start without `--org` and `--position` runs no query for these rules. With them, the rules are
  decided on a read snapshot (`GetOrganization`, and the chart for a position); only a move opens a write
  transaction, which decides it again under the write lock.
- **An AI without a home organization** (a v0.3 identity no upgrade placed - only test fixtures) has no rules.
- **Test hook.** `Registry.afterFolderMiss` (`agents.SetAfterFolderMiss` in `export_test.go`) lets a test hold a
  first session after it found no AI, so another session can create it first.
- **Error code and detail keys** are in `dto/common.go`: `ErrorCodeOrganizationMismatch` and
  `MismatchDetailOrganization`/`MismatchDetailName`.

