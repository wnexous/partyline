-- name: CreateAuditEntry :exec
INSERT INTO audit_log (id, actor_id, action, target_kind, target_id, detail_json, ip, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?);

-- name: ListAuditEntries :many
SELECT a.id, a.actor_id, a.action, a.target_kind, a.target_id, a.detail_json, a.ip, a.created_at,
       CAST(COALESCE(u.username, '') AS TEXT) AS actor_username,
       CAST(COALESCE(i.display_name, '') AS TEXT) AS actor_display_name
FROM audit_log a LEFT JOIN users u ON u.id = a.actor_id
LEFT JOIN intelligences i ON i.id = u.id
WHERE a.id < sqlc.arg(before)
ORDER BY a.id DESC
LIMIT sqlc.arg(limit);

-- name: CountAuditEntriesSince :one
-- The entries of one action about one target recorded after a time: what the invitation rate limit
-- counts, so it survives a restart.
SELECT COUNT(*) FROM audit_log
WHERE action = sqlc.arg(action) AND target_kind = sqlc.arg(target_kind) AND target_id = sqlc.arg(target_id)
  AND created_at > sqlc.arg(since);
