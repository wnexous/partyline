-- name: CreateOrganization :exec
INSERT INTO organizations (id, name, slug, created_by, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?);

-- name: GetOrganization :one
SELECT * FROM organizations WHERE id = ?;

-- name: GetOrganizationBySlug :one
-- The slug column ignores case, so Acme and acme are the same address.
SELECT * FROM organizations WHERE slug = ?;

-- name: CreatePosition :exec
INSERT INTO positions (id, org_id, name, parent_id, created_at) VALUES (?, ?, ?, ?, ?);

-- name: CountPositionName :one
-- Positions of the organization with this name, ignoring case.
SELECT COUNT(*) FROM positions WHERE org_id = ? AND name = ?;

-- name: CreateOrgMember :exec
INSERT INTO org_members (org_id, iid, permission, position_id, inviter_iid, joined_at)
VALUES (?, ?, ?, ?, ?, ?);

-- name: GetOrgMember :one
SELECT * FROM org_members WHERE org_id = ? AND iid = ?;

-- name: ListOrgPositions :many
-- The positions of an organization; parent_id is empty for a root.
SELECT id, name, CAST(COALESCE(parent_id, '') AS TEXT) AS parent_id FROM positions WHERE org_id = ?;

-- name: ListOrgChartMembers :many
-- The members of an organization with what the rules of the chart read (AD-017): permission,
-- position, a guest's inviter and, for an AI, its creator.
SELECT m.iid, m.permission,
       CAST(COALESCE(m.position_id, '') AS TEXT) AS position_id,
       CAST(COALESCE(m.inviter_iid, '') AS TEXT) AS inviter_iid,
       CAST(COALESCE(a.creator_iid, '') AS TEXT) AS creator_iid
FROM org_members m LEFT JOIN agents a ON a.id = m.iid
WHERE m.org_id = ?;

-- name: GetOrgChartMember :one
-- One member of ListOrgChartMembers.
SELECT m.iid, m.permission,
       CAST(COALESCE(m.position_id, '') AS TEXT) AS position_id,
       CAST(COALESCE(m.inviter_iid, '') AS TEXT) AS inviter_iid,
       CAST(COALESCE(a.creator_iid, '') AS TEXT) AS creator_iid
FROM org_members m LEFT JOIN agents a ON a.id = m.iid
WHERE m.org_id = ? AND m.iid = ?;

-- name: ListSharedOrgIDs :many
-- The organizations both identities are members of, oldest membership of the first one first.
SELECT x.org_id FROM org_members x JOIN org_members y ON y.org_id = x.org_id
WHERE x.iid = sqlc.arg(first_iid) AND y.iid = sqlc.arg(second_iid)
ORDER BY x.joined_at, x.org_id;

-- name: CountSharedOrgs :one
SELECT COUNT(*) FROM org_members x JOIN org_members y ON y.org_id = x.org_id
WHERE x.iid = sqlc.arg(first_iid) AND y.iid = sqlc.arg(second_iid);

-- name: GetAgentOrg :one
-- The home organization of an AI; empty when it has none.
SELECT CAST(COALESCE(org_id, '') AS TEXT) AS org_id FROM agents WHERE id = ?;

-- name: SetAgentHome :exec
UPDATE agents SET org_id = ? WHERE id = ?;

-- name: SetAgentCreator :exec
UPDATE agents SET creator_iid = ? WHERE id = ?;

-- name: ListOrgMembers :many
-- The members of an organization with the identity, its position name and, for a guest, the identity
-- that invited it: what the members table shows (OMEM-01, OMEM-03).
SELECT m.iid, m.permission, m.joined_at,
       CAST(COALESCE(m.position_id, '') AS TEXT) AS position_id,
       CAST(COALESCE(p.name, '') AS TEXT) AS position_name,
       i.kind, i.display_name, i.handle, i.avatar_sha256,
       CAST(COALESCE(m.inviter_iid, '') AS TEXT) AS inviter_iid,
       CAST(COALESCE(inv.kind, '') AS TEXT) AS inviter_kind,
       CAST(COALESCE(inv.display_name, '') AS TEXT) AS inviter_display_name,
       CAST(COALESCE(inv.handle, '') AS TEXT) AS inviter_handle,
       CAST(COALESCE(inv.avatar_sha256, '') AS TEXT) AS inviter_avatar_sha256
FROM org_members m
JOIN intelligences i ON i.id = m.iid
LEFT JOIN positions p ON p.id = m.position_id AND p.org_id = m.org_id
LEFT JOIN intelligences inv ON inv.id = m.inviter_iid
WHERE m.org_id = ?
ORDER BY i.display_name COLLATE NOCASE, m.iid;

-- name: ListOrgHomeAgentIDs :many
-- The AIs whose home is this organization: deleting one of them here deletes it for good, while any other
-- member is only removed (POS-06).
SELECT id FROM agents WHERE org_id = ? ORDER BY id;

-- name: GetOrgMemberView :one
-- One row of ListOrgMembers.
SELECT m.iid, m.permission, m.joined_at,
       CAST(COALESCE(m.position_id, '') AS TEXT) AS position_id,
       CAST(COALESCE(p.name, '') AS TEXT) AS position_name,
       i.kind, i.display_name, i.handle, i.avatar_sha256,
       CAST(COALESCE(m.inviter_iid, '') AS TEXT) AS inviter_iid,
       CAST(COALESCE(inv.kind, '') AS TEXT) AS inviter_kind,
       CAST(COALESCE(inv.display_name, '') AS TEXT) AS inviter_display_name,
       CAST(COALESCE(inv.handle, '') AS TEXT) AS inviter_handle,
       CAST(COALESCE(inv.avatar_sha256, '') AS TEXT) AS inviter_avatar_sha256
FROM org_members m
JOIN intelligences i ON i.id = m.iid
LEFT JOIN positions p ON p.id = m.position_id AND p.org_id = m.org_id
LEFT JOIN intelligences inv ON inv.id = m.inviter_iid
WHERE m.org_id = sqlc.arg(org_id) AND m.iid = sqlc.arg(iid);

-- name: GetOrgMembership :one
-- An organization with the permission and position of one of its members.
SELECT sqlc.embed(o), m.permission, CAST(COALESCE(m.position_id, '') AS TEXT) AS position_id
FROM org_members m JOIN organizations o ON o.id = m.org_id
WHERE m.org_id = ? AND m.iid = ?;

-- name: ListOrgsOfMember :many
-- The organizations an identity belongs to, with its permission and position, by name.
SELECT sqlc.embed(o), m.permission, CAST(COALESCE(m.position_id, '') AS TEXT) AS position_id
FROM org_members m JOIN organizations o ON o.id = m.org_id
WHERE m.iid = ?
ORDER BY o.name COLLATE NOCASE, o.id;

-- name: UpdateOrganization :exec
UPDATE organizations SET name = ?, slug = ?, updated_at = ? WHERE id = ?;

-- name: SetOrganizationAvatar :exec
UPDATE organizations SET avatar_sha256 = ?, updated_at = ? WHERE id = ?;

-- name: CountOrganizationAvatar :one
SELECT COUNT(*) FROM organizations WHERE avatar_sha256 = ?;

-- name: DeleteOrganization :exec
DELETE FROM organizations WHERE id = ?;

-- name: CountOrgAgents :one
-- The AIs whose home is the organization.
SELECT COUNT(*) FROM agents WHERE org_id = ?;

-- name: CountOrgConnectors :one
SELECT COUNT(*) FROM connectors WHERE org_id = ?;

-- name: CountOrgGroups :one
SELECT COUNT(*) FROM rooms WHERE org_id = ? AND kind = 'group';

-- name: CountOrganizationSlug :one
SELECT COUNT(*) FROM organizations WHERE slug = ?;

-- name: GetPosition :one
SELECT * FROM positions WHERE id = sqlc.arg(id) AND org_id = sqlc.arg(org_id);

-- name: GetPositionByName :one
-- The position of the organization with this name, ignoring case.
SELECT * FROM positions WHERE org_id = ? AND name = ?;

-- name: UpdatePosition :exec
UPDATE positions SET name = ?, parent_id = ? WHERE id = ? AND org_id = ?;

-- name: MovePositionChildren :exec
UPDATE positions SET parent_id = sqlc.narg(to_position) WHERE org_id = sqlc.arg(org_id) AND parent_id = sqlc.arg(from_position);

-- name: MovePositionHolders :exec
UPDATE org_members SET position_id = sqlc.narg(to_position) WHERE org_id = sqlc.arg(org_id) AND position_id = sqlc.arg(from_position);

-- name: MovePositionInvitations :exec
UPDATE org_invitations SET position_id = sqlc.narg(to_position) WHERE org_id = sqlc.arg(org_id) AND position_id = sqlc.arg(from_position);

-- name: DeletePosition :exec
DELETE FROM positions WHERE id = ? AND org_id = ?;

-- name: UpdateOrgMemberPermission :exec
UPDATE org_members SET permission = ?, inviter_iid = ? WHERE org_id = ? AND iid = ?;

-- name: UpdateOrgMemberPosition :exec
UPDATE org_members SET position_id = ? WHERE org_id = ? AND iid = ?;

-- name: DeleteOrgMember :exec
DELETE FROM org_members WHERE org_id = ? AND iid = ?;

-- name: DeleteOrgGroupMemberships :many
-- Takes a user or an AI out of the groups of an organization (OMEM-06); DMs and the groups of other
-- organizations are not touched.
DELETE FROM room_members
WHERE member_id = sqlc.arg(iid) AND kind IN ('user', 'agent')
  AND room_id IN (SELECT id FROM rooms WHERE org_id = sqlc.arg(org_id) AND kind = 'group')
RETURNING room_id;

-- name: ClearOrgOperator :many
-- The connections of the organization an identity operated now have no operator (OMEM-08).
UPDATE connectors SET operator_iid = NULL WHERE org_id = sqlc.arg(org_id) AND operator_iid = sqlc.arg(iid) RETURNING id;

-- name: DeletePlatformItemsOfAgentOnConnector :exec
DELETE FROM platform_items WHERE agent_id = ? AND connector_id = ?;

-- name: ClearUserDefaultOrg :exec
-- A user who leaves its default organization has no default.
UPDATE users SET default_org_id = NULL WHERE id = ? AND default_org_id = ?;

-- name: GetOrgInvitation :one
SELECT * FROM org_invitations WHERE id = ?;

-- name: GetOrgInvitationOf :one
SELECT * FROM org_invitations WHERE org_id = ? AND invitee_iid = ?;

-- name: UpsertOrgInvitation :exec
-- Inviting again updates the pending invitation of the invitee (OMEM-01): its permission, position,
-- inviter and the 14 days all start over; the id stays.
INSERT INTO org_invitations (id, org_id, invitee_iid, permission, position_id, invited_by, created_at, expires_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (org_id, invitee_iid) DO UPDATE SET
    permission = excluded.permission, position_id = excluded.position_id, invited_by = excluded.invited_by,
    created_at = excluded.created_at, expires_at = excluded.expires_at;

-- name: ListOrgInvitations :many
-- The invitations of an organization that have not expired, newest first.
SELECT * FROM org_invitations WHERE org_id = sqlc.arg(org_id) AND expires_at > sqlc.arg(now)
ORDER BY created_at DESC, id;

-- name: ListInvitationsForInvitee :many
SELECT * FROM org_invitations WHERE invitee_iid = sqlc.arg(invitee_iid) AND expires_at > sqlc.arg(now)
ORDER BY created_at DESC, id;

-- name: ListInvitationsForAIsAround :many
-- The pending invitations of the AIs whose home organization the identity belongs to: the candidates
-- of the invitations an identity answers for the AIs it manages.
SELECT inv.* FROM org_invitations inv
JOIN agents a ON a.id = inv.invitee_iid
JOIN org_members om ON om.org_id = a.org_id AND om.iid = sqlc.arg(member_iid)
WHERE inv.expires_at > sqlc.arg(now)
ORDER BY inv.created_at DESC, inv.id;

-- name: DeleteOrgInvitation :exec
DELETE FROM org_invitations WHERE id = ?;

-- name: DeleteExpiredOrgInvitations :many
DELETE FROM org_invitations WHERE expires_at <= ? RETURNING *;

-- name: SetUserDefaultOrg :exec
UPDATE users SET default_org_id = ? WHERE id = ?;

-- name: GetUserDefaultOrg :one
-- The default organization of a user, if the user still belongs to it.
SELECT CAST(m.org_id AS TEXT) AS org_id FROM users u
JOIN org_members m ON m.org_id = u.default_org_id AND m.iid = u.id
WHERE u.id = ?;

-- name: GetOldestOrgOf :one
-- The organization an identity belongs to for the longest time (ties by id); a permission narrows it
-- to the memberships with that permission, an empty one takes any.
SELECT org_id FROM org_members
WHERE iid = sqlc.arg(iid) AND (sqlc.arg(permission) = '' OR permission = sqlc.arg(permission))
ORDER BY joined_at, org_id LIMIT 1;

-- name: GetPersonalOrg :one
-- The first organization an account created: its personal one (AD-021), whatever its default is now.
SELECT * FROM organizations WHERE created_by = ? ORDER BY created_at, id LIMIT 1;
