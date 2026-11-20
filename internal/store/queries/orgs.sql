-- name: CreateOrganization :exec
INSERT INTO organizations (id, name, slug, created_by, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?);

-- name: GetOrganization :one
SELECT * FROM organizations WHERE id = ?;

-- name: GetOrganizationBySlug :one
-- The slug column ignores case, so Acme and acme are the same address.
SELECT * FROM organizations WHERE slug = ?;

-- name: CreateOrgMember :exec
INSERT INTO org_members (org_id, iid, permission, joined_at)
VALUES (?, ?, ?, ?);

-- name: GetOrgMember :one
SELECT * FROM org_members WHERE org_id = ? AND iid = ?;

-- name: ListOrgRosterMembers :many
-- The members of an organization with their permission: what the membership rules read (AD-017).
SELECT m.iid, m.permission FROM org_members m WHERE m.org_id = ?;

-- name: GetOrgRosterMember :one
-- One member of ListOrgRosterMembers.
SELECT m.iid, m.permission FROM org_members m WHERE m.org_id = ? AND m.iid = ?;

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
-- The members of an organization with the identity: what the members table shows (OMEM-03).
SELECT m.iid, m.permission, m.joined_at, i.kind, i.display_name, i.handle, i.avatar_sha256
FROM org_members m
JOIN intelligences i ON i.id = m.iid
WHERE m.org_id = ?
ORDER BY i.display_name COLLATE NOCASE, m.iid;

-- name: ListOrgHomeAgents :many
-- The AIs whose home is this organization, with their hosts: deleting one of them here deletes it for good,
-- while any other member is only removed (POS-06), and its host may delete it (SR2-12).
SELECT id, owner_id FROM agents WHERE org_id = ? ORDER BY id;

-- name: GetOrgMemberView :one
-- One row of ListOrgMembers.
SELECT m.iid, m.permission, m.joined_at, i.kind, i.display_name, i.handle, i.avatar_sha256
FROM org_members m
JOIN intelligences i ON i.id = m.iid
WHERE m.org_id = sqlc.arg(org_id) AND m.iid = sqlc.arg(iid);

-- name: GetOrgMembership :one
-- An organization with the permission of one of its members.
SELECT sqlc.embed(o), m.permission
FROM org_members m JOIN organizations o ON o.id = m.org_id
WHERE m.org_id = ? AND m.iid = ?;

-- name: ListOrgsOfMember :many
-- The organizations an identity belongs to, with its permission, by name.
SELECT sqlc.embed(o), m.permission
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

-- name: UpdateOrgMemberPermission :exec
UPDATE org_members SET permission = ? WHERE org_id = ? AND iid = ?;

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
-- Inviting again updates the pending invitation of the invitee (OMEM-01): its permission, inviter and
-- the 14 days all start over; the id stays.
INSERT INTO org_invitations (id, org_id, invitee_iid, permission, invited_by, created_at, expires_at)
VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (org_id, invitee_iid) DO UPDATE SET
    permission = excluded.permission, invited_by = excluded.invited_by,
    created_at = excluded.created_at, expires_at = excluded.expires_at;

-- name: ListOrgInvitations :many
-- The invitations of an organization that have not expired, newest first.
SELECT * FROM org_invitations WHERE org_id = sqlc.arg(org_id) AND expires_at > sqlc.arg(now)
ORDER BY created_at DESC, id;

-- name: ListInvitationsForInvitee :many
SELECT * FROM org_invitations WHERE invitee_iid = sqlc.arg(invitee_iid) AND expires_at > sqlc.arg(now)
ORDER BY created_at DESC, id;

-- name: ListInvitationsForManagedAIs :many
-- The pending invitations of the AIs the identity manages (SR2-13): the AIs it hosts and those it instructs
-- as a user.
SELECT inv.* FROM org_invitations inv
JOIN agents a ON a.id = inv.invitee_iid
WHERE inv.expires_at > sqlc.arg(now)
  AND (a.owner_id = sqlc.arg(manager_iid)
       OR EXISTS (SELECT 1 FROM agent_instructors ai JOIN intelligences i ON i.id = ai.instructor_iid
                  WHERE ai.agent_iid = a.id AND ai.instructor_iid = sqlc.arg(manager_iid) AND i.kind = 'human'))
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
