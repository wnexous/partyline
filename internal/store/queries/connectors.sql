-- name: CreateConnector :exec
INSERT INTO connectors (id, owner_id, kind, name, config_json, secrets_enc, enabled, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: GetConnector :one
SELECT * FROM connectors WHERE id = ?;
