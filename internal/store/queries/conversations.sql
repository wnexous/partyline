-- name: CreateDMRoom :exec
-- dm_a and dm_b are the participants' intelligence ids, sorted (dm_a < dm_b).
INSERT INTO rooms (id, owner_id, name, kind, dm_a, dm_b, created_at, updated_at)
VALUES (?, ?, ?, 'dm', ?, ?, ?, ?);

-- name: GetDMRoom :one
SELECT * FROM rooms WHERE kind = 'dm' AND dm_a = ? AND dm_b = ?;

-- name: SetRoomReadBaseline :exec
UPDATE rooms SET read_baseline = ? WHERE id = ?;

-- name: UpsertReadCursor :exec
-- A read cursor only moves forward.
INSERT INTO read_cursors (user_id, room_id, seq) VALUES (?, ?, ?)
ON CONFLICT (user_id, room_id) DO UPDATE SET seq = MAX(read_cursors.seq, excluded.seq);

-- name: GetReadCursor :one
SELECT seq FROM read_cursors WHERE user_id = ? AND room_id = ?;

-- name: DeleteReadCursor :exec
DELETE FROM read_cursors WHERE user_id = ? AND room_id = ?;

-- name: InsertDMRoom :execrows
-- Creates the DM of a pair unless the pair has one (rooms_dm_pair): 1 row when created, 0 when it
-- existed. dm_a and dm_b are the sorted participants (dm_a < dm_b).
INSERT INTO rooms (id, owner_id, name, kind, dm_a, dm_b, created_at, updated_at)
VALUES (?, ?, '', 'dm', ?, ?, ?, ?)
ON CONFLICT DO NOTHING;

-- name: ListRoomParticipants :many
-- The two intelligences of a DM, by id, each with the subtype row it shares its id with (role:
-- user, ai or person) and, for a person, the kind of its connector.
SELECT sqlc.embed(i),
       CAST(CASE WHEN u.id IS NOT NULL THEN 'user' WHEN a.id IS NOT NULL THEN 'ai'
                 WHEN p.id IS NOT NULL THEN 'person' ELSE '' END AS TEXT) AS role,
       CAST(COALESCE(c.kind, '') AS TEXT) AS platform
FROM rooms r
JOIN intelligences i ON i.id = r.dm_a OR i.id = r.dm_b
LEFT JOIN users u ON u.id = i.id
LEFT JOIN agents a ON a.id = i.id
LEFT JOIN people p ON p.id = i.id
LEFT JOIN connectors c ON c.id = p.connector_id
WHERE r.id = ? AND r.kind = 'dm'
ORDER BY i.id;
