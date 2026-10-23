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
