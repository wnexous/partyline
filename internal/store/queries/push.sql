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

-- name: CountPushSubscriptionsByUser :one
-- How many devices the account has stored, to keep it under the cap (PUSH-03).
SELECT COUNT(*) FROM push_subscriptions WHERE user_id = ?;

-- name: HasPushSubscriptionForUserEndpoint :one
-- Whether the endpoint is already one of the account's rows: re-registering it updates the row
-- and does not count against the cap (PUSH-03).
SELECT EXISTS (
    SELECT 1 FROM push_subscriptions WHERE user_id = ? AND endpoint = ?
);

-- name: DeleteOldestPushSubscriptionOfUser :execrows
-- The account registered one device too many: its oldest row makes room (PUSH-03).
DELETE FROM push_subscriptions
WHERE id = (
    SELECT s.id FROM push_subscriptions s WHERE s.user_id = ? ORDER BY s.created_at, s.id LIMIT 1
);

-- name: DeleteAllPushSubscriptions :execrows
-- A new VAPID pair invalidates every stored subscription (PUSH-26).
DELETE FROM push_subscriptions;

-- name: ListPushSubscriptionsByUser :many
-- Every device of the user, for the test notification (PUSH-15).
SELECT * FROM push_subscriptions WHERE user_id = ? ORDER BY created_at, id;

-- name: GetPushRecipientLocale :one
-- The account's language, which picks the language of the test notification's fixed text (PUSH-15).
SELECT locale FROM users WHERE id = ?;

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

-- name: ListConnectorAlertTargets :many
-- The accounts responsible for a connection and their devices (ALERT-02): the admins of the owning
-- organization and the host of the AI that operates it, each distinct account once (UNION), skipping
-- a disabled account. users.push_groups is not read: an alert follows the devices (ALERT-13);
-- users.locale is, because the alert's fixed title and reason follow the recipient's language
-- (ALERT-07, ALERT-08).
WITH responsible AS (
    SELECT m.iid AS user_id
    FROM connectors c
    JOIN org_members m ON m.org_id = c.org_id AND m.permission = 'admin'
    WHERE c.id = sqlc.arg(connector_id)
    UNION
    SELECT a.owner_id AS user_id
    FROM connectors c
    JOIN agents a ON a.id = c.operator_iid
    WHERE c.id = sqlc.arg(connector_id)
)
SELECT r.user_id, u.locale, s.id AS subscription_id, s.endpoint, s.p256dh, s.auth
FROM responsible r
JOIN users u ON u.id = r.user_id AND u.disabled_at IS NULL
JOIN push_subscriptions s ON s.user_id = r.user_id
ORDER BY r.user_id, s.created_at, s.id;

-- name: ListConnectorStatuses :many
-- The stored status of every connection, the alert consumer's start state (ALERT-10).
SELECT id, status FROM connectors ORDER BY id;
