-- name: DeleteRevokedToken :execrows
DELETE FROM api_tokens WHERE id = ? AND user_id = ? AND revoked_at IS NOT NULL;

-- name: DeleteTokensRevokedBefore :execrows
DELETE FROM api_tokens WHERE revoked_at IS NOT NULL AND revoked_at < ?;
