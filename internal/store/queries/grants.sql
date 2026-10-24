-- name: ListGrantLevels :many
SELECT level FROM grants
WHERE resource_kind = sqlc.arg(resource_kind) AND resource_id = sqlc.arg(resource_id)
  AND subject IN (sqlc.arg(user_id), '*');

-- name: CountSubjectGrants :one
-- Grants of exactly this subject on a resource; the everyone subject '*' is not counted for a user.
SELECT COUNT(*) FROM grants WHERE resource_kind = ? AND resource_id = ? AND subject = ?;

-- name: ListGrantsWithUsers :many
SELECT g.subject, g.level,
       CAST(COALESCE(u.username, '') AS TEXT) AS username,
       CAST(COALESCE(i.display_name, '') AS TEXT) AS display_name
FROM grants g
LEFT JOIN users u ON u.id = g.subject
LEFT JOIN intelligences i ON i.id = u.id
WHERE g.resource_kind = ? AND g.resource_id = ?
ORDER BY username, g.subject;

-- name: CreateGrant :exec
INSERT INTO grants (resource_kind, resource_id, subject, level, created_by, created_at)
VALUES (?, ?, ?, ?, ?, ?);

-- name: DeleteGrants :exec
DELETE FROM grants WHERE resource_kind = ? AND resource_id = ?;
