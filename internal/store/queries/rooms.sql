-- name: CreateRoom :exec
INSERT INTO rooms (id, owner_id, name, description, instructions, instructions_by, bridge_chats, retention_days,
                   created_at, updated_at, updated_by)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: GetRoom :one
SELECT * FROM rooms WHERE id = ?;

-- name: CountRoomMember :one
SELECT COUNT(*) FROM room_members WHERE room_id = ? AND kind = ? AND member_id = ?;

-- name: CreateRoomMember :exec
INSERT INTO room_members (id, room_id, kind, member_id, outbound, added_by, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?);

-- name: GetRoomWithOwner :one
SELECT sqlc.embed(r), u.username AS owner_username, u.display_name AS owner_display_name,
       CAST(COALESCE(oi.kind, '') AS TEXT) AS owner_ikind,
       CAST(COALESCE(oi.display_name, '') AS TEXT) AS owner_iname,
       CAST(COALESCE(oi.handle, '') AS TEXT) AS owner_handle,
       CAST(COALESCE(oi.avatar_sha256, '') AS TEXT) AS owner_avatar,
       (SELECT COUNT(*) FROM room_members rm WHERE rm.room_id = r.id) AS member_count
FROM rooms r JOIN users u ON u.id = r.owner_id
LEFT JOIN intelligences oi ON oi.id = r.owner_id
WHERE r.id = ?;

-- name: ListRoomsForUser :many
-- The conversations a user may have in a list: the groups they own, are granted or are a member
-- of, and the DMs they take part in. The DMs they only view through an AI are left out.
SELECT sqlc.embed(r), u.username AS owner_username, u.display_name AS owner_display_name,
       CAST(COALESCE(oi.kind, '') AS TEXT) AS owner_ikind,
       CAST(COALESCE(oi.display_name, '') AS TEXT) AS owner_iname,
       CAST(COALESCE(oi.handle, '') AS TEXT) AS owner_handle,
       CAST(COALESCE(oi.avatar_sha256, '') AS TEXT) AS owner_avatar,
       (SELECT COUNT(*) FROM room_members rm WHERE rm.room_id = r.id) AS member_count
FROM rooms r JOIN users u ON u.id = r.owner_id
LEFT JOIN intelligences oi ON oi.id = r.owner_id
WHERE (r.kind = 'group' AND r.owner_id = sqlc.arg(user_id))
   OR (r.kind = 'group' AND r.id IN (SELECT g.resource_id FROM grants g
                                     WHERE g.resource_kind = 'room' AND g.subject IN (sqlc.arg(user_id), '*')))
   OR (r.kind = 'group' AND r.id IN (SELECT m.room_id FROM room_members m
                                     WHERE m.kind = 'user' AND m.member_id = sqlc.arg(user_id)))
   OR (r.kind = 'dm' AND (r.dm_a = sqlc.arg(user_id) OR r.dm_b = sqlc.arg(user_id)))
ORDER BY r.name COLLATE NOCASE, r.id;

-- name: UpdateRoom :exec
UPDATE rooms SET name = ?, description = ?, instructions = ?, instructions_by = ?, bridge_chats = ?, broadcast = ?,
                 retention_days = ?, updated_at = ?, updated_by = ?
WHERE id = ?;

-- name: DeleteRoom :exec
DELETE FROM rooms WHERE id = ?;

-- name: ListRoomMembers :many
-- Agent and user members are named by their intelligence (PROF-03); the v0.3 names are the
-- fallback for rows that have none.
SELECT sqlc.embed(m),
       CAST(CASE m.kind
            WHEN 'agent' THEN COALESCE(i.display_name, NULLIF(a.display_name, ''), a.name, '')
            WHEN 'chat' THEN COALESCE(c.title, '')
            ELSE COALESCE(i.display_name, NULLIF(u.display_name, ''), u.username, '')
            END AS TEXT) AS name,
       CAST(COALESCE(ao.username, '') AS TEXT) AS agent_owner,
       CAST(COALESCE(cn.kind, '') AS TEXT) AS connector_kind,
       CAST(COALESCE(c.kind, '') AS TEXT) AS chat_kind,
       CAST(COALESCE(i.id, '') AS TEXT) AS intelligence_id,
       CAST(COALESCE(i.kind, '') AS TEXT) AS intelligence_kind,
       CAST(COALESCE(i.display_name, '') AS TEXT) AS intelligence_name,
       CAST(COALESCE(i.handle, '') AS TEXT) AS intelligence_handle,
       CAST(COALESCE(i.avatar_sha256, '') AS TEXT) AS intelligence_avatar
FROM room_members m
LEFT JOIN intelligences i ON m.kind IN ('agent', 'user') AND i.id = m.member_id
LEFT JOIN agents a ON m.kind = 'agent' AND a.id = m.member_id
LEFT JOIN users ao ON ao.id = a.owner_id
LEFT JOIN chats c ON m.kind = 'chat' AND c.id = m.member_id
LEFT JOIN connectors cn ON cn.id = c.connector_id
LEFT JOIN users u ON m.kind = 'user' AND u.id = m.member_id
WHERE m.room_id = ?
ORDER BY m.created_at, m.id;

-- name: GetRoomMember :one
SELECT * FROM room_members WHERE id = ? AND room_id = ?;

-- name: UpdateRoomMemberSettings :exec
UPDATE room_members SET outbound = ?, display_name = ?, sign_outbound = ?, delivery = ?, keywords_json = ?
WHERE id = ?;

-- name: DeleteRoomMember :exec
DELETE FROM room_members WHERE id = ?;

-- name: GetRoomMemberByMember :one
SELECT * FROM room_members WHERE room_id = ? AND kind = ? AND member_id = ?;

-- name: CountCatchAllConnectors :one
-- Connectors whose catch-all room is this room (ROUTE-07).
SELECT COUNT(*) FROM connectors WHERE auto_route_room_id = ?;

-- name: SetRoomCatchAllSafety :exec
-- A room chosen as a catch-all room stops broadcasting and bridging (ROUTE-07).
UPDATE rooms SET broadcast = 0, bridge_chats = 0, updated_at = ? WHERE id = ?;

-- name: CountRoomChatMembers :one
SELECT COUNT(*) FROM room_members WHERE room_id = ? AND kind = 'chat';
