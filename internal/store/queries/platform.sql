-- name: InsertPlatformItem :one
-- CID-26: a platform message of one chat of one connection queues once; a repeat conflicts with
-- the dedupe index and returns no row. A message without a platform id never conflicts.
INSERT INTO platform_items (id, agent_id, connector_id, chat_id, external_id, sender_id, sender_name,
                            trusted, from_self, kind, text, received_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (connector_id, chat_id, external_id) WHERE external_id <> '' DO NOTHING
RETURNING seq;

-- name: InsertPlatformAttachment :exec
INSERT INTO platform_attachments (id, item_id, sha256, name, mime, size, status)
VALUES (?, ?, ?, ?, ?, ?, ?);

-- name: CountUnackedPlatformItems :one
SELECT COUNT(*) FROM platform_items WHERE agent_id = ? AND acked_at IS NULL;

-- name: DeleteUnackedPlatformItemsOverCap :execrows
-- CID-11: an AI keeps its newest cap unacked items; the older ones are deleted and the caller
-- counts them as dropped.
DELETE FROM platform_items WHERE id IN (
    SELECT o.id FROM platform_items o
    WHERE o.agent_id = sqlc.arg(agent_id) AND o.acked_at IS NULL
      AND o.seq <= COALESCE((SELECT k.seq FROM platform_items k
                             WHERE k.agent_id = sqlc.arg(agent_id) AND k.acked_at IS NULL
                             ORDER BY k.seq DESC LIMIT 1 OFFSET sqlc.arg(cap)), 0)
);

-- name: AddPlatformDropped :exec
UPDATE agents SET platform_dropped = platform_dropped + sqlc.arg(n) WHERE id = sqlc.arg(agent_id);

-- name: GetPlatformDropped :one
SELECT platform_dropped FROM agents WHERE id = ?;

-- name: ResetPlatformDropped :exec
UPDATE agents SET platform_dropped = 0 WHERE id = ?;

-- name: ListUnackedPlatformItems :many
-- CID-08: the unacknowledged items of an AI after a seq, oldest first, with the chat and the
-- platform they came from.
SELECT i.seq, i.id, i.connector_id, i.chat_id, i.external_id, i.sender_id, i.sender_name,
       i.trusted, i.from_self, i.kind, i.text, i.received_at,
       c.title AS chat_title, c.kind AS chat_kind, k.kind AS platform
FROM platform_items i
JOIN chats c ON c.id = i.chat_id
JOIN connectors k ON k.id = i.connector_id
WHERE i.agent_id = sqlc.arg(agent_id) AND i.acked_at IS NULL AND i.seq > sqlc.arg(after_seq)
ORDER BY i.seq
LIMIT sqlc.arg(limit);

-- name: ListPlatformAttachmentsForItems :many
SELECT * FROM platform_attachments WHERE item_id IN (sqlc.slice(item_ids)) ORDER BY item_id, id;

-- name: AckPlatformItem :execrows
-- CID-09: the text goes at once; the row and its files stay until the purge. Only the item's own
-- AI acknowledges it, and only once.
UPDATE platform_items SET acked_at = sqlc.arg(acked_at), text = ''
WHERE id = sqlc.arg(id) AND agent_id = sqlc.arg(agent_id) AND acked_at IS NULL;

-- name: MovePlatformItems :execrows
-- CID-05: every item of the connection, acknowledged or not, goes to the new operator.
UPDATE platform_items SET agent_id = sqlc.arg(agent_id)
WHERE connector_id = sqlc.arg(connector_id) AND agent_id <> sqlc.arg(agent_id);
