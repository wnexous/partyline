-- name: AddMessageReaction :execrows
-- Adding a reaction that is there already changes nothing (RCT-03).
INSERT INTO message_reactions (message_id, intelligence_id, emoji, created_at)
VALUES (?, ?, ?, ?)
ON CONFLICT (message_id, intelligence_id, emoji) DO NOTHING;

-- name: RemoveMessageReaction :execrows
DELETE FROM message_reactions
WHERE message_id = ? AND intelligence_id = ? AND emoji = ?;

-- name: CountMessageEmoji :one
-- How many different emoji a message carries (RCT-04).
SELECT COUNT(DISTINCT emoji) FROM message_reactions WHERE message_id = ?;

-- name: CountMessageEmojiUses :one
-- How many times one emoji is used on a message, by anyone.
SELECT COUNT(*) FROM message_reactions WHERE message_id = ? AND emoji = ?;

-- name: ListReactionsByMessages :many
-- In the order they reacted (RCT-05).
SELECT message_id, emoji, intelligence_id FROM message_reactions
WHERE message_id IN (sqlc.slice(message_ids))
ORDER BY id;
