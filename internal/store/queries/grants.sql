-- name: ListGrantLevels :many
SELECT level FROM grants
WHERE resource_kind = sqlc.arg(resource_kind) AND resource_id = sqlc.arg(resource_id)
  AND subject IN (sqlc.arg(user_id), '*');

-- name: ListUserRoomLevels :many
-- The levels a user holds on rooms next to owning them: grants (own or everyone) on groups, a user
-- membership of a group (post) and taking part in a DM (post). Grants on DMs are ignored (DM-08).
SELECT g.resource_id AS room_id, CAST(g.level AS TEXT) AS level
FROM grants g JOIN rooms r ON r.id = g.resource_id
WHERE g.resource_kind = 'room' AND r.kind = 'group' AND g.subject IN (sqlc.arg(user_id), '*')
UNION ALL
SELECT m.room_id, CAST('post' AS TEXT)
FROM room_members m JOIN rooms r ON r.id = m.room_id
WHERE m.kind = 'user' AND m.member_id = sqlc.arg(user_id) AND r.kind = 'group'
UNION ALL
SELECT r.id, CAST('post' AS TEXT)
FROM rooms r
WHERE r.kind = 'dm' AND (r.dm_a = sqlc.arg(user_id) OR r.dm_b = sqlc.arg(user_id));

-- name: CountSubjectGrants :one
-- Grants of exactly this subject on a resource; the everyone subject '*' is not counted for a user.
SELECT COUNT(*) FROM grants WHERE resource_kind = ? AND resource_id = ? AND subject = ?;

-- name: ListGrantsWithUsers :many
SELECT g.subject, g.level,
       CAST(COALESCE(u.username, '') AS TEXT) AS username,
       CAST(COALESCE(u.display_name, '') AS TEXT) AS display_name
FROM grants g
LEFT JOIN users u ON u.id = g.subject
WHERE g.resource_kind = ? AND g.resource_id = ?
ORDER BY username, g.subject;

-- name: CreateGrant :exec
INSERT INTO grants (resource_kind, resource_id, subject, level, created_by, created_at)
VALUES (?, ?, ?, ?, ?, ?);

-- name: DeleteGrants :exec
DELETE FROM grants WHERE resource_kind = ? AND resource_id = ?;
