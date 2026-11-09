-- name: CreateUpload :exec
INSERT INTO uploads (id, owner_kind, owner_id, name, mime, size, received, sha_state, sha256, created_at, expires_at)
VALUES (?, ?, ?, ?, ?, ?, 0, ?, '', ?, ?);

-- name: GetUpload :one
-- An upload of its owner that has not expired (LF-03, LF-06); another owner's is no row.
SELECT * FROM uploads
WHERE id = sqlc.arg(id) AND owner_kind = sqlc.arg(owner_kind) AND owner_id = sqlc.arg(owner_id)
  AND expires_at > sqlc.arg(now);

-- name: ListOwnUploads :many
-- The uploads of an owner among ids that have not expired, for a message that uses them.
SELECT * FROM uploads
WHERE owner_kind = sqlc.arg(owner_kind) AND owner_id = sqlc.arg(owner_id) AND expires_at > sqlc.arg(now)
  AND id IN (sqlc.slice(ids));

-- name: AdvanceUpload :execrows
-- Records a chunk written at the offset the upload had received (LF-03).
UPDATE uploads SET received = sqlc.arg(received), sha_state = sqlc.arg(sha_state)
WHERE id = sqlc.arg(id) AND received = sqlc.arg(offset) AND sha256 = '';

-- name: CompleteUpload :execrows
-- The whole file arrived and moved into the blob store.
UPDATE uploads SET sha256 = sqlc.arg(sha256), sha_state = x''
WHERE id = sqlc.arg(id) AND received = size AND sha256 = '';

-- name: CountOpenUploads :one
-- The unfinished uploads of an owner, which a new one may not outnumber.
SELECT COUNT(*) FROM uploads
WHERE owner_kind = sqlc.arg(owner_kind) AND owner_id = sqlc.arg(owner_id) AND sha256 = '' AND expires_at > sqlc.arg(now);

-- name: PendingUploadBytes :one
-- The bytes the unfinished uploads may still write on the data disk (LF-07).
SELECT CAST(COALESCE(SUM(size - received), 0) AS INTEGER) FROM uploads WHERE sha256 = '' AND expires_at > ?;

-- name: DeleteOwnUploads :exec
-- A message used the uploads: the rows go and their blobs stay with its attachments.
DELETE FROM uploads
WHERE owner_kind = sqlc.arg(owner_kind) AND owner_id = sqlc.arg(owner_id) AND id IN (sqlc.slice(ids));

-- name: DeleteExpiredUploads :many
-- Uploads past their expiry, finished or not (LF-06); the caller removes their temporary files.
DELETE FROM uploads WHERE expires_at <= ? RETURNING id;

-- name: ListUploadIDs :many
SELECT id FROM uploads ORDER BY id;

-- name: CountUploadsBySHA :one
-- Whether a finished upload no message used yet holds the blob, so the blob sweep keeps it.
SELECT COUNT(*) FROM uploads WHERE sha256 = ?;
