-- name: CreateAgent :exec
INSERT INTO agents (id, owner_id, name, description, created_at) VALUES (?, ?, ?, ?, ?);

-- name: GetAgent :one
SELECT * FROM agents WHERE id = ?;

-- name: InsertAgentIfMissing :exec
INSERT INTO agents (id, owner_id, name, description, created_at) VALUES (?, ?, ?, '', ?)
ON CONFLICT (owner_id, name) DO NOTHING;

-- name: GetAgentByName :one
SELECT * FROM agents WHERE owner_id = ? AND name = ?;

-- name: CountAgentsByOwner :one
SELECT COUNT(*) FROM agents WHERE owner_id = ?;

-- name: GetAgentWithOwner :one
SELECT sqlc.embed(a), u.username AS owner_username, u.display_name AS owner_display_name,
       CAST(COALESCE(p.slug, '') AS TEXT) AS project_slug, CAST(COALESCE(p.name, '') AS TEXT) AS project_name
FROM agents a JOIN users u ON u.id = a.owner_id
LEFT JOIN projects p ON p.id = a.project_id
WHERE a.id = ?;

-- name: ListAgentsForUser :many
SELECT sqlc.embed(a), u.username AS owner_username, u.display_name AS owner_display_name,
       CAST(COALESCE(p.slug, '') AS TEXT) AS project_slug, CAST(COALESCE(p.name, '') AS TEXT) AS project_name
FROM agents a JOIN users u ON u.id = a.owner_id
LEFT JOIN projects p ON p.id = a.project_id
WHERE (a.owner_id = sqlc.arg(user_id)
       OR a.id IN (SELECT g.resource_id FROM grants g
                   WHERE g.resource_kind = 'agent' AND g.subject IN (sqlc.arg(user_id), '*')))
  AND (CAST(sqlc.arg(include_archived) AS BOOLEAN) OR a.archived_at IS NULL)
ORDER BY a.name, u.username, a.id;

-- name: UpdateAgentDescription :exec
UPDATE agents SET description = ? WHERE id = ?;

-- name: TouchAgent :exec
UPDATE agents SET last_seen_at = ? WHERE id = ?;

-- name: DeleteAgentMemberships :many
DELETE FROM room_members WHERE kind = 'agent' AND member_id = ?
RETURNING room_id;

-- name: DeleteAgent :exec
DELETE FROM agents WHERE id = ?;

-- name: ListAgentRoomStates :many
SELECT r.id, r.name, r.agents_paused, CAST(COALESCE(c.seq, 0) AS INTEGER) AS cursor,
       m.display_name, m.delivery, m.keywords_json, r.instructions, r.instructions_by
FROM room_members m
JOIN rooms r ON r.id = m.room_id
LEFT JOIN agent_cursors c ON c.agent_id = m.member_id AND c.room_id = m.room_id
WHERE m.kind = 'agent' AND m.member_id = ?
ORDER BY r.name, r.id;

-- name: CountAgentBacklog :one
SELECT COUNT(*) FROM messages
WHERE room_id = sqlc.arg(room_id) AND seq > sqlc.arg(after_seq)
  AND NOT (author_kind = 'agent' AND author_id = sqlc.arg(agent_id));

-- name: ListAgentBacklog :many
SELECT * FROM message_views
WHERE room_id = sqlc.arg(room_id) AND seq > sqlc.arg(after_seq)
  AND NOT (author_kind = 'agent' AND author_id = sqlc.arg(agent_id))
ORDER BY seq DESC
LIMIT sqlc.arg(limit);

-- name: ListAgentPending :many
SELECT * FROM message_views
WHERE room_id = sqlc.arg(room_id) AND seq > sqlc.arg(after_seq)
  AND NOT (author_kind = 'agent' AND author_id = sqlc.arg(agent_id))
ORDER BY seq ASC
LIMIT sqlc.arg(limit);

-- name: GetMessageRoomSeq :one
SELECT room_id, seq FROM messages WHERE id = ?;

-- name: AdvanceAgentCursor :exec
INSERT INTO agent_cursors (agent_id, room_id, seq) VALUES (?, ?, ?)
ON CONFLICT (agent_id, room_id) DO UPDATE SET seq = MAX(agent_cursors.seq, excluded.seq);

-- name: GetAgentCursor :one
SELECT seq FROM agent_cursors WHERE agent_id = ? AND room_id = ?;

-- name: CreateProjectIfMissing :exec
INSERT INTO projects (id, owner_id, slug, name, created_at) VALUES (?, ?, ?, ?, ?)
ON CONFLICT (owner_id, slug) DO NOTHING;

-- name: GetProjectBySlug :one
SELECT * FROM projects WHERE owner_id = ? AND slug = ?;

-- name: UpdateAgentIdentity :exec
UPDATE agents
SET project_id = ?, harness = ?, machine = ?, kind = ?, auto_name = ?, session_no = ?, archived_at = NULL
WHERE id = ?;

-- name: MarkAgentConnected :exec
UPDATE agents SET connected_at = ?, archived_at = NULL WHERE id = ?;

-- name: GetProject :one
SELECT * FROM projects WHERE id = ?;

-- name: SetAgentDisplayName :exec
UPDATE agents SET display_name = ? WHERE id = ?;

-- name: PromoteAgent :exec
UPDATE agents SET kind = 'fixed', archived_at = NULL WHERE id = ?;

-- name: ListArchiveCandidates :many
SELECT id FROM agents
WHERE kind = 'temporary' AND archived_at IS NULL AND COALESCE(last_seen_at, created_at) < sqlc.arg(before);

-- name: ArchiveAgent :exec
UPDATE agents SET archived_at = ? WHERE id = ? AND kind = 'temporary' AND archived_at IS NULL;

-- name: ListAgentRoomIDs :many
SELECT room_id FROM room_members WHERE kind = 'agent' AND member_id = ?;

-- name: ListProjectsForUser :many
SELECT sqlc.embed(p), u.username AS owner_username, u.display_name AS owner_display_name
FROM projects p JOIN users u ON u.id = p.owner_id
WHERE p.owner_id = sqlc.arg(user_id)
   OR p.id IN (SELECT g.resource_id FROM grants g
               WHERE g.resource_kind = 'project' AND g.subject IN (sqlc.arg(user_id), '*'))
ORDER BY p.name, u.username, p.id;

-- name: GetProjectWithOwner :one
SELECT sqlc.embed(p), u.username AS owner_username, u.display_name AS owner_display_name
FROM projects p JOIN users u ON u.id = p.owner_id
WHERE p.id = ?;

-- name: UpdateProjectName :exec
UPDATE projects SET name = ? WHERE id = ?;

-- name: ListTemporaryProjectAgents :many
SELECT * FROM agents WHERE project_id = ? AND kind = 'temporary';

-- name: SetAgentAutoName :exec
UPDATE agents SET auto_name = ? WHERE id = ?;

-- name: ListProjectSessions :many
-- A project's agents, most recently connected first (PROJ-07 picks the first one online).
SELECT * FROM agents WHERE project_id = ?
ORDER BY connected_at IS NULL, connected_at DESC, id;

-- name: ListProjectRoomStates :many
-- The rooms of a project member with the project's cursor and member settings (PROJ-07,
-- PROJ-08), leaving out rooms where the agent is a member itself.
SELECT r.id, r.name, r.agents_paused, CAST(COALESCE(c.seq, 0) AS INTEGER) AS cursor,
       m.display_name, m.delivery, m.keywords_json, r.instructions, r.instructions_by
FROM room_members m
JOIN rooms r ON r.id = m.room_id
LEFT JOIN project_cursors c ON c.project_id = m.member_id AND c.room_id = m.room_id
WHERE m.kind = 'project' AND m.member_id = sqlc.arg(project_id)
  AND NOT EXISTS (SELECT 1 FROM room_members d
                  WHERE d.room_id = m.room_id AND d.kind = 'agent' AND d.member_id = sqlc.arg(agent_id))
ORDER BY r.name, r.id;
