-- name: GetInstanceSetting :one
SELECT * FROM instance_settings WHERE key = ?;

-- name: UpsertInstanceSetting :exec
INSERT INTO instance_settings (key, value, updated_at) VALUES (?, ?, ?)
ON CONFLICT (key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at;

-- name: DeleteInstanceSetting :execrows
DELETE FROM instance_settings WHERE key = ?;
