-- name: GetHeirAdmin :one
-- The oldest active instance admin other than the given user: who inherits what an account that is
-- deleted leaves behind (OUPG-02).
SELECT * FROM users WHERE role = 'admin' AND disabled_at IS NULL AND id <> ? ORDER BY created_at, id LIMIT 1;

-- name: ListLastAdminOrgIDs :many
-- The organizations the identity administers alone: no other member has the admin permission.
SELECT m.org_id FROM org_members m
WHERE m.iid = sqlc.arg(iid) AND m.permission = 'admin'
  AND NOT EXISTS (SELECT 1 FROM org_members o WHERE o.org_id = m.org_id AND o.permission = 'admin' AND o.iid <> m.iid)
ORDER BY m.org_id;

-- name: RepointRoomOwners :exec
-- The owner column of a room only anchors its foreign key to users (AD-019): re-pointed before the user
-- goes, the cascade cannot delete a room an organization owns.
UPDATE rooms SET owner_id = sqlc.arg(heir) WHERE owner_id = sqlc.arg(user_id);

-- name: RepointConnectorOwners :exec
UPDATE connectors SET owner_id = sqlc.arg(heir) WHERE owner_id = sqlc.arg(user_id);

-- name: ListHostedAgentIDs :many
-- The AIs hosted on the user's computers.
SELECT id FROM agents WHERE owner_id = ? ORDER BY id;

-- name: ListHostedAgentOrgIDs :many
-- The organizations those AIs are members of: the creator link of a positionless one goes with the
-- account, so who manages it there changes.
SELECT DISTINCT m.org_id FROM org_members m JOIN agents a ON a.id = m.iid WHERE a.owner_id = ? ORDER BY m.org_id;

-- name: RehomeHostedAgents :exec
-- The AIs hosted by a deleted user stay as unlinked identities owned by the heir (AD-019): no folder, so no
-- session ever resolves them again, and the id as their v0.3 name, which no other AI of the heir has.
UPDATE agents SET owner_id = sqlc.arg(heir), folder = '', name = id WHERE owner_id = sqlc.arg(user_id);

-- name: DeleteUserRoomMemberships :many
-- Takes a user out of every room they were a member of, DMs included; the rooms it returns are the ones to
-- announce.
DELETE FROM room_members WHERE kind = 'user' AND member_id = ? RETURNING room_id;

-- name: DeleteUser :exec
-- The sessions, tokens, linked chat accounts, machines and pairing codes of the user cascade, and so does
-- the intelligence (users_intelligence_delete) with its memberships, invitations and DMs.
DELETE FROM users WHERE id = ?;
