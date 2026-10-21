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
