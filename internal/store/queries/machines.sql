-- name: UpsertMachine :exec
INSERT INTO machines (owner_id, hostname, home, last_seen_at) VALUES (?, ?, ?, ?)
ON CONFLICT (owner_id, hostname) DO UPDATE SET home = excluded.home, last_seen_at = excluded.last_seen_at;

-- name: GetMachine :one
SELECT * FROM machines WHERE owner_id = ? AND hostname = ?;

-- name: ListMachines :many
SELECT * FROM machines WHERE owner_id = ? ORDER BY hostname;

-- name: SetMachineLabel :exec
UPDATE machines SET label = ? WHERE owner_id = ? AND hostname = ?;

-- name: DeleteMachine :exec
DELETE FROM machines WHERE owner_id = ? AND hostname = ?;
