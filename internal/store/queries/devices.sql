-- name: DeleteOldDeviceRequests :exec
DELETE FROM device_requests WHERE created_at <= ?;

-- name: CountDeviceRequestsSince :one
SELECT COUNT(*) FROM device_requests WHERE ip_key = ? AND created_at > ?;

-- name: CreateDeviceRequest :exec
-- kind is what the approval creates: an agent token (setup) or a runner token (CA-01).
INSERT INTO device_requests (id, code_hash, poll_secret_hash, machine, kind, created_ip, ip_key, created_at, expires_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: GetDeviceRequestByCode :one
SELECT * FROM device_requests WHERE code_hash = ?;

-- name: GetDeviceRequestByPollSecret :one
SELECT * FROM device_requests WHERE poll_secret_hash = ?;

-- name: ClaimDevicePoll :execrows
UPDATE device_requests SET last_poll_at = sqlc.arg(now)
WHERE id = sqlc.arg(id) AND (last_poll_at IS NULL OR last_poll_at <= sqlc.arg(allowed_before));

-- name: DecideDeviceRequest :exec
UPDATE device_requests SET status = ?, user_id = ?, token_id = ?, token_once_enc = ? WHERE id = ?;

-- name: TakeDeviceToken :exec
UPDATE device_requests SET token_once_enc = NULL WHERE id = ?;

-- name: DeleteOldDeviceCodeAttempts :exec
DELETE FROM device_code_attempts WHERE attempted_at <= ?;

-- name: CountDeviceCodeAttempts :one
SELECT COUNT(*) FROM device_code_attempts WHERE user_id = ? AND attempted_at > ?;

-- name: AddDeviceCodeAttempt :one
INSERT INTO device_code_attempts (user_id, attempted_at) VALUES (?, ?) RETURNING id;

-- name: DeleteDeviceCodeAttempt :exec
DELETE FROM device_code_attempts WHERE id = ?;
