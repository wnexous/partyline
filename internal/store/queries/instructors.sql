-- name: ListAgentInstructors :many
-- The identities that instruct an AI besides its host (SR2-06), oldest link first.
SELECT sqlc.embed(i), ai.created_by, ai.created_at
FROM agent_instructors ai JOIN intelligences i ON i.id = ai.instructor_iid
WHERE ai.agent_iid = ?
ORDER BY ai.created_at, ai.instructor_iid;

-- name: GetAgentHost :one
-- The user who hosts an AI (agents.owner_id).
SELECT owner_id FROM agents WHERE id = ?;

-- name: ListAgentInstructorKinds :many
-- What decides who instructs and who manages an AI (SR2-07, SR2-13): its instructors with their kinds.
SELECT ai.instructor_iid, i.kind
FROM agent_instructors ai JOIN intelligences i ON i.id = ai.instructor_iid
WHERE ai.agent_iid = ?;

-- name: AddAgentInstructor :execrows
-- Adds an instructor; adding one that is already there changes nothing (0 rows).
INSERT INTO agent_instructors (agent_iid, instructor_iid, created_by, created_at)
VALUES (?, ?, ?, ?)
ON CONFLICT (agent_iid, instructor_iid) DO NOTHING;

-- name: RemoveAgentInstructor :execrows
DELETE FROM agent_instructors WHERE agent_iid = ? AND instructor_iid = ?;

-- name: MergeAgentInstructors :exec
-- Merge: the instructors of the merged AI instruct the survivor too, never the survivor itself.
INSERT INTO agent_instructors (agent_iid, instructor_iid, created_by, created_at)
SELECT sqlc.arg(into_id), src.instructor_iid, src.created_by, src.created_at FROM agent_instructors src
WHERE src.agent_iid = sqlc.arg(from_id) AND src.instructor_iid <> sqlc.arg(into_id)
ON CONFLICT (agent_iid, instructor_iid) DO NOTHING;

-- name: ReassignInstructor :exec
-- Merge: where the merged identity instructed an AI, the survivor does, unless that AI is the survivor.
INSERT INTO agent_instructors (agent_iid, instructor_iid, created_by, created_at)
SELECT src.agent_iid, sqlc.arg(into_id), src.created_by, src.created_at FROM agent_instructors src
WHERE src.instructor_iid = sqlc.arg(from_id) AND src.agent_iid <> sqlc.arg(into_id)
ON CONFLICT (agent_iid, instructor_iid) DO NOTHING;

-- name: ListAgentIDsInstructedBy :many
-- The AIs an identity instructs, by id.
SELECT agent_iid FROM agent_instructors WHERE instructor_iid = ? ORDER BY agent_iid;
