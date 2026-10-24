-- name: CreateRoom :exec
-- org_id is the organization that owns a group and creator_iid the identity that created it
-- (ORG-02); both are NULL for a row written before organizations.
INSERT INTO rooms (id, owner_id, name, description, instructions, instructions_by, retention_days,
                   created_at, updated_at, updated_by, org_id, creator_iid)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: GetRoom :one
SELECT * FROM rooms WHERE id = ?;

-- name: CountRoomMember :one
SELECT COUNT(*) FROM room_members WHERE room_id = ? AND kind = ? AND member_id = ?;

-- name: CreateRoomMember :exec
INSERT INTO room_members (id, room_id, kind, member_id, added_by, created_at)
VALUES (?, ?, ?, ?, ?, ?);

-- name: GetRoomWithOwner :one
SELECT sqlc.embed(r), u.username AS owner_username,
       CAST(COALESCE(oi.kind, '') AS TEXT) AS owner_ikind,
       CAST(COALESCE(oi.display_name, '') AS TEXT) AS owner_iname,
       CAST(COALESCE(oi.handle, '') AS TEXT) AS owner_handle,
       CAST(COALESCE(oi.avatar_sha256, '') AS TEXT) AS owner_avatar,
       (SELECT COUNT(*) FROM room_members rm WHERE rm.room_id = r.id) AS member_count
FROM rooms r JOIN users u ON u.id = r.owner_id
LEFT JOIN intelligences oi ON oi.id = r.owner_id
WHERE r.id = ?;

-- name: ListRoomsForUser :many
-- The conversations a user may have in a list (AD-016): the groups they are a member of, create or
-- administer, and the DMs they take part in. The DMs they only view as the manager of an AI are left
-- out. It selects the rooms ListUserRoomLevels gives a level.
SELECT sqlc.embed(r), u.username AS owner_username,
       CAST(COALESCE(oi.kind, '') AS TEXT) AS owner_ikind,
       CAST(COALESCE(oi.display_name, '') AS TEXT) AS owner_iname,
       CAST(COALESCE(oi.handle, '') AS TEXT) AS owner_handle,
       CAST(COALESCE(oi.avatar_sha256, '') AS TEXT) AS owner_avatar,
       (SELECT COUNT(*) FROM room_members rm WHERE rm.room_id = r.id) AS member_count
FROM rooms r JOIN users u ON u.id = r.owner_id
LEFT JOIN intelligences oi ON oi.id = r.owner_id
WHERE (r.kind = 'group' AND r.id IN (SELECT m.room_id FROM room_members m
                                     WHERE m.kind = 'user' AND m.member_id = sqlc.arg(user_id)))
   OR (r.kind = 'group' AND EXISTS (SELECT 1 FROM org_members om
                                    WHERE om.org_id = r.org_id AND om.iid = sqlc.arg(user_id)
                                      AND (om.permission = 'admin'
                                           OR (om.permission <> 'guest' AND r.creator_iid = sqlc.arg(user_id)))))
   OR (r.kind = 'dm' AND (r.dm_a = sqlc.arg(user_id) OR r.dm_b = sqlc.arg(user_id)))
ORDER BY r.name COLLATE NOCASE, r.id;

-- name: ListUserRoomLevels :many
-- The levels a user holds on rooms (AD-016): post as a user member of a group, manage as the creator
-- of a group (while a member of its organization other than a guest) or as an admin of its
-- organization, and post as a participant of a DM. The DMs the user only views as the manager of an AI
-- are not here, and neither is anything a grant names.
SELECT m.room_id AS room_id, CAST('post' AS TEXT) AS level
FROM room_members m JOIN rooms r ON r.id = m.room_id
WHERE m.kind = 'user' AND m.member_id = sqlc.arg(user_id) AND r.kind = 'group'
UNION ALL
SELECT r.id, CAST('manage' AS TEXT)
FROM rooms r JOIN org_members om ON om.org_id = r.org_id AND om.iid = sqlc.arg(user_id)
WHERE r.kind = 'group'
  AND (om.permission = 'admin' OR (om.permission <> 'guest' AND r.creator_iid = sqlc.arg(user_id)))
UNION ALL
SELECT r.id, CAST('post' AS TEXT)
FROM rooms r
WHERE r.kind = 'dm' AND (r.dm_a = sqlc.arg(user_id) OR r.dm_b = sqlc.arg(user_id));

-- name: UpdateRoom :exec
UPDATE rooms SET name = ?, description = ?, instructions = ?, instructions_by = ?,
                 retention_days = ?, updated_at = ?, updated_by = ?
WHERE id = ?;

-- name: DeleteRoom :exec
DELETE FROM rooms WHERE id = ?;

-- name: ListRoomMembers :many
-- Agent and user members are named by their intelligence (PROF-03); a chat by its title.
SELECT sqlc.embed(m),
       CAST(CASE m.kind
            WHEN 'chat' THEN COALESCE(c.title, '')
            ELSE COALESCE(i.display_name, '')
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
UPDATE room_members SET display_name = ?, delivery = ?, keywords_json = ?
WHERE id = ?;

-- name: DeleteRoomMember :exec
DELETE FROM room_members WHERE id = ?;

-- name: GetRoomMemberByMember :one
SELECT * FROM room_members WHERE room_id = ? AND kind = ? AND member_id = ?;

-- name: CountRoomChatMembers :one
SELECT COUNT(*) FROM room_members WHERE room_id = ? AND kind = 'chat';
