-- name: WebhookReplaySeen :one
SELECT COUNT(*) FROM webhook_replays
WHERE connector_id = sqlc.arg(connector_id) AND sig_hash = sqlc.arg(sig_hash) AND expires_at >= sqlc.arg(now);

-- name: DeleteExpiredWebhookReplays :exec
DELETE FROM webhook_replays WHERE expires_at < sqlc.arg(now);

-- name: RecordWebhookReplay :execrows
INSERT INTO webhook_replays (connector_id, sig_hash, expires_at)
VALUES (sqlc.arg(connector_id), sqlc.arg(sig_hash), sqlc.arg(expires_at))
ON CONFLICT (connector_id, sig_hash) DO NOTHING;

-- name: CountWebhookReplays :one
SELECT COUNT(*) FROM webhook_replays;
