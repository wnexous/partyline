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
