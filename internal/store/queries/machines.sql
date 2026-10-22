-- name: UpsertMachine :exec
-- A sighting of the machine: its home folder when the session sent one (an empty home keeps
-- the stored one) and the time. The owner's label is never touched.
INSERT INTO machines (owner_id, hostname, home, last_seen_at) VALUES (?, ?, ?, ?)
ON CONFLICT (owner_id, hostname) DO UPDATE SET
    home = CASE WHEN excluded.home <> '' THEN excluded.home ELSE machines.home END,
    last_seen_at = excluded.last_seen_at;

-- name: GetMachine :one
SELECT * FROM machines WHERE owner_id = ? AND hostname = ?;

-- name: ListMachines :many
SELECT * FROM machines WHERE owner_id = ? ORDER BY hostname;

-- name: SetMachineLabel :exec
UPDATE machines SET label = ? WHERE owner_id = ? AND hostname = ?;

-- name: DeleteMachine :exec
DELETE FROM machines WHERE owner_id = ? AND hostname = ?;
