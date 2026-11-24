-- name: SetConnectorHistoryDays :exec
-- CH-01: how many days the chat history of the connection is kept: 0 for off, -1 for forever.
UPDATE connectors SET history_days = ?, updated_at = ? WHERE id = ?;

-- name: GetConnectorHistoryDays :one
SELECT history_days FROM connectors WHERE id = ?;

-- name: InsertChatHistory :execrows
-- CH-02: a platform message of a chat is kept once (CH-08 dedupe); a repeat changes nothing.
INSERT INTO chat_history (id, connector_id, chat_id, external_id, sender_id, sender_name, from_self, trusted,
                          kind, text, reply_to, quoted, quoted_from_self, at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT DO NOTHING;

-- name: InsertChatHistoryAttachment :exec
INSERT INTO chat_history_attachments (id, message_id, name, mime, size, status)
VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT DO NOTHING;

-- name: FindChatHistoryByExternalID :one
-- The kept message of the chat of the connection that the platform knows by this id.
SELECT id, text, deleted FROM chat_history
WHERE connector_id = ? AND chat_id = ? AND external_id = ? AND external_id <> '';

-- name: GetChatHistoryMessage :one
SELECT * FROM chat_history WHERE id = ?;

-- name: EditChatHistory :execrows
-- CH-02: an edit shows the new text; a deleted message stays deleted.
UPDATE chat_history SET text = sqlc.arg(text), edited = 1 WHERE id = sqlc.arg(id) AND deleted = 0;

-- name: DeleteChatHistoryContent :execrows
-- CH-02: a delete drops the text, the quote's preview and the files, and the message reads deleted.
UPDATE chat_history SET text = '', quoted = '', deleted = 1 WHERE id = ?;

-- name: DeleteChatHistoryAttachments :exec
DELETE FROM chat_history_attachments WHERE message_id = ?;

-- name: UpsertChatHistoryReaction :exec
-- CH-02: one reaction per sender of a message; the newest replaces the older.
INSERT INTO chat_history_reactions (message_id, sender_id, sender_name, from_self, emoji, at)
VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT (message_id, sender_id) DO UPDATE SET
    sender_name = excluded.sender_name, from_self = excluded.from_self, emoji = excluded.emoji, at = excluded.at;

-- name: DeleteChatHistoryReaction :exec
DELETE FROM chat_history_reactions WHERE message_id = ? AND sender_id = ?;

-- name: GetChatHistoryCursor :one
-- The place of a kept message of the chat, to page before it (CH-05).
SELECT at, seq FROM chat_history WHERE id = ? AND chat_id = ?;

-- name: ListChatHistory :many
-- CH-05: the kept messages of a chat written at or after since and before the cursor (at, seq), newest
-- first; the caller turns the page around.
SELECT * FROM chat_history
WHERE chat_id = sqlc.arg(chat_id) AND at >= sqlc.arg(since)
  AND (at < sqlc.arg(before_at) OR (at = sqlc.arg(before_at) AND seq < sqlc.arg(before_seq)))
ORDER BY at DESC, seq DESC
LIMIT sqlc.arg(limit);

-- name: ListChatHistoryAttachments :many
-- The files of the kept messages; kept tells whether the queue still holds the file under that id.
SELECT a.*, CAST(EXISTS (SELECT 1 FROM platform_attachments p WHERE p.id = a.id AND p.sha256 <> '') AS BOOLEAN) AS kept
FROM chat_history_attachments a
WHERE a.message_id IN (sqlc.slice(message_ids))
ORDER BY a.message_id, a.rowid;

-- name: ListChatHistoryReactions :many
SELECT * FROM chat_history_reactions WHERE message_id IN (sqlc.slice(message_ids)) ORDER BY message_id, at, sender_id;

-- name: GetChatHistoryAnchor :one
-- CH-08: the oldest kept message of the chat with a platform id, the one the platform's past is asked
-- for from.
SELECT external_id, from_self, at FROM chat_history
WHERE chat_id = ? AND external_id <> ''
ORDER BY at, seq
LIMIT 1;

-- name: DeleteExpiredChatHistory :execrows
-- CH-03: the kept messages older than their connection's retention (all of them for a connection whose
-- history is off, none for one that keeps it forever: -1), a batch at a time.
DELETE FROM chat_history WHERE seq IN (
    SELECT h.seq FROM chat_history h JOIN connectors c ON c.id = h.connector_id
    WHERE c.history_days >= 0 AND h.at < sqlc.arg(now) - c.history_days * 86400000
    LIMIT sqlc.arg(limit)
);

-- name: DeleteConnectorChatHistoryBefore :execrows
-- CH-03: the kept messages of the connection written before the cutoff, a batch at a time.
DELETE FROM chat_history WHERE seq IN (
    SELECT o.seq FROM chat_history o WHERE o.connector_id = sqlc.arg(connector_id) AND o.at < sqlc.arg(cutoff)
    LIMIT sqlc.arg(limit)
);
