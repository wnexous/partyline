-- name: CreateIntelligence :exec
INSERT INTO intelligences (id, kind, display_name, handle, avatar_sha256, bio, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?);

-- name: GetIntelligence :one
SELECT * FROM intelligences WHERE id = ?;

-- name: GetIntelligenceByHandle :one
SELECT * FROM intelligences WHERE handle = ?;

-- name: CountIntelligenceHandle :one
-- Handles compare case-insensitively (the column collates NOCASE).
SELECT COUNT(*) FROM intelligences WHERE handle = ?;

-- name: ListIntelligencesByIDs :many
SELECT * FROM intelligences WHERE id IN (sqlc.slice('ids'));

-- name: UpdateIntelligenceProfile :exec
UPDATE intelligences SET display_name = ?, handle = ?, bio = ?, updated_at = ? WHERE id = ?;

-- name: SetIntelligenceAvatar :exec
UPDATE intelligences SET avatar_sha256 = ?, updated_at = ? WHERE id = ?;

-- name: CountIntelligenceAvatar :one
SELECT COUNT(*) FROM intelligences WHERE avatar_sha256 = ?;

-- name: DeleteIntelligence :exec
DELETE FROM intelligences WHERE id = ?;

-- name: GetIntelligenceWithRole :one
-- The intelligence with the subtype row it shares its id with: role is user, ai or person
-- ('' for an orphan), plus the AI's owner and the person's connector.
SELECT sqlc.embed(i),
       CAST(CASE WHEN u.id IS NOT NULL THEN 'user' WHEN a.id IS NOT NULL THEN 'ai'
                 WHEN p.id IS NOT NULL THEN 'person' ELSE '' END AS TEXT) AS role,
       CAST(COALESCE(a.owner_id, '') AS TEXT) AS agent_owner_id,
       CAST(COALESCE(p.connector_id, '') AS TEXT) AS person_connector_id
FROM intelligences i
LEFT JOIN users u ON u.id = i.id
LEFT JOIN agents a ON a.id = i.id
LEFT JOIN people p ON p.id = i.id
WHERE i.id = ?;

-- name: SetIntelligenceDisplayName :exec
UPDATE intelligences SET display_name = ?, updated_at = ? WHERE id = ?;

-- name: ListMemberRoomIDs :many
-- The rooms the intelligence is a direct member of (an AI or a user member).
SELECT DISTINCT room_id FROM room_members WHERE kind IN ('agent', 'user') AND member_id = ?;

-- name: ListIntelligenceRoomIDs :many
-- The rooms where the intelligence is a member or wrote a message: the conversations it can
-- share with a caller (CanSee).
SELECT room_id FROM room_members WHERE kind IN ('agent', 'user') AND member_id = sqlc.arg(id)
UNION
SELECT room_id FROM messages WHERE author_iid = sqlc.arg(id) AND author_iid <> '';
