-- name: CreateRoom :exec
INSERT INTO rooms (id, owner_id, name, description, bridge_chats, retention_days, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?);

-- name: GetRoom :one
SELECT * FROM rooms WHERE id = ?;

-- name: CountRoomMember :one
SELECT COUNT(*) FROM room_members WHERE room_id = ? AND kind = ? AND member_id = ?;

-- name: CreateRoomMember :exec
INSERT INTO room_members (id, room_id, kind, member_id, outbound, added_by, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?);
