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
