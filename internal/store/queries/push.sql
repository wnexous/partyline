-- name: UpsertPushSubscription :one
-- One row per endpoint (PUSH-03): a second registration moves it to the latest account and session,
-- replaces the keys, keeps the first created_at and resets last_ok_at.
INSERT INTO push_subscriptions (id, user_id, session_id, endpoint, p256dh, auth, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (endpoint) DO UPDATE SET
    user_id = excluded.user_id,
    session_id = excluded.session_id,
    p256dh = excluded.p256dh,
    auth = excluded.auth,
    last_ok_at = NULL
RETURNING *;

-- name: DeletePushSubscription :execrows
-- The ownership check is in the SQL, so another account's endpoint is not found (PUSH-05).
DELETE FROM push_subscriptions WHERE endpoint = ? AND user_id = ?;

-- name: DeletePushSubscriptionByID :execrows
-- The push service answered 404 or 410: the row is gone for good (PUSH-12).
DELETE FROM push_subscriptions WHERE id = ?;

-- name: ListPushSubscriptionsByUser :many
-- Every device of the user, for the test notification (PUSH-15).
SELECT * FROM push_subscriptions WHERE user_id = ? ORDER BY created_at, id;

-- name: MarkPushSubscriptionDelivered :exec
-- Only a 2xx from the push service writes this time (PUSH-12).
UPDATE push_subscriptions SET last_ok_at = ? WHERE id = ?;

-- name: ListGroupPushTargets :many
-- One row per (enabled user member, subscription) of a group, with the handle for mentions and the
-- account's group setting (PUSH-08). unread follows CountUnreadByRoom: after the user's cursor and
-- the room's baseline (PUSH-09).
SELECT s.user_id, i.handle, u.push_groups, s.id AS subscription_id,
       s.endpoint, s.p256dh, s.auth,
       CAST(CASE WHEN sqlc.arg(seq) > COALESCE(c.seq, 0) AND sqlc.arg(seq) > r.read_baseline
                 THEN 1 ELSE 0 END AS INTEGER) AS unread
FROM rooms r
JOIN room_members m ON m.room_id = r.id AND m.kind = 'user'
JOIN users u ON u.id = m.member_id AND u.disabled_at IS NULL
JOIN intelligences i ON i.id = u.id
JOIN push_subscriptions s ON s.user_id = u.id
LEFT JOIN read_cursors c ON c.room_id = r.id AND c.user_id = u.id
WHERE r.id = sqlc.arg(room_id) AND r.kind = 'group'
ORDER BY s.user_id, s.id;

-- name: ListDMPushTargets :many
-- One row per (user participant, subscription) of a DM (PUSH-08): an AI's DM yields only its user,
-- a DM between two AIs yields none. The shape and the unread rule are the group ones (PUSH-09).
SELECT s.user_id, i.handle, u.push_groups, s.id AS subscription_id,
       s.endpoint, s.p256dh, s.auth,
       CAST(CASE WHEN sqlc.arg(seq) > COALESCE(c.seq, 0) AND sqlc.arg(seq) > r.read_baseline
                 THEN 1 ELSE 0 END AS INTEGER) AS unread
FROM rooms r
JOIN intelligences i ON i.id IN (r.dm_a, r.dm_b)
JOIN users u ON u.id = i.id AND u.disabled_at IS NULL
JOIN push_subscriptions s ON s.user_id = u.id
LEFT JOIN read_cursors c ON c.room_id = r.id AND c.user_id = u.id
WHERE r.id = sqlc.arg(room_id) AND r.kind = 'dm'
ORDER BY s.user_id, s.id;
