-- name: ListRoomFiles :many
-- Files of a room newest first (FILES-01): attachment ids are UUIDv7, so id order is time
-- order and an id is the page cursor. kind is image, audio, video or document (everything else).
SELECT a.id, a.message_id, a.mime, a.name, a.size, a.status, a.created_at, v.seq AS message_seq,
       v.author_kind, v.author_name, v.author_id, v.author_user_id, v.author_iid, v.author_handle,
       v.author_ikind, v.author_avatar, CAST(COALESCE(k.kind, '') AS TEXT) AS chat_platform,
       CAST(CASE
            WHEN lower(a.mime) LIKE 'image/%' THEN 'image'
            WHEN lower(a.mime) LIKE 'audio/%' THEN 'audio'
            WHEN lower(a.mime) LIKE 'video/%' THEN 'video'
            ELSE 'document'
            END AS TEXT) AS kind
FROM attachments a JOIN message_views v ON v.id = a.message_id
LEFT JOIN chats c ON v.author_kind = 'chat' AND c.id = v.author_id
LEFT JOIN connectors k ON k.id = c.connector_id
WHERE v.room_id = sqlc.arg(room_id) AND a.id < sqlc.arg(before)
  AND (CAST(sqlc.arg(kind) AS TEXT) = '' OR CAST(sqlc.arg(kind) AS TEXT) = CASE
            WHEN lower(a.mime) LIKE 'image/%' THEN 'image'
            WHEN lower(a.mime) LIKE 'audio/%' THEN 'audio'
            WHEN lower(a.mime) LIKE 'video/%' THEN 'video'
            ELSE 'document'
            END)
ORDER BY a.id DESC
LIMIT sqlc.arg(limit);
