-- name: ListOrgAgentIDs :many
-- The AIs that are members of an organization, home or not: the ones its members see (AD-016).
SELECT a.id FROM org_members m JOIN agents a ON a.id = m.iid WHERE m.org_id = ? ORDER BY a.id;

-- name: ListOrgConnectorIDs :many
SELECT id FROM connectors WHERE org_id = ? ORDER BY id;

-- name: SetRoomHome :exec
-- Test and testkit support: puts a room in an organization, as creating a group there does.
UPDATE rooms SET org_id = ?, creator_iid = ? WHERE id = ?;

-- name: SetConnectorOrg :exec
-- Test and testkit support: puts a connection in an organization, as creating it there does.
UPDATE connectors SET org_id = ? WHERE id = ?;
