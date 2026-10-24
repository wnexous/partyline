-- name: CreateDMRoom :exec
-- dm_a and dm_b are the participants' intelligence ids, sorted (dm_a < dm_b).
INSERT INTO rooms (id, owner_id, name, kind, dm_a, dm_b, created_at, updated_at)
VALUES (?, ?, ?, 'dm', ?, ?, ?, ?);

-- name: GetDMRoom :one
SELECT * FROM rooms WHERE kind = 'dm' AND dm_a = ? AND dm_b = ?;

-- name: SetRoomReadBaseline :exec
UPDATE rooms SET read_baseline = ? WHERE id = ?;

-- name: UpsertReadCursor :exec
-- A read cursor only moves forward.
INSERT INTO read_cursors (user_id, room_id, seq) VALUES (?, ?, ?)
ON CONFLICT (user_id, room_id) DO UPDATE SET seq = MAX(read_cursors.seq, excluded.seq);

-- name: GetReadCursor :one
SELECT seq FROM read_cursors WHERE user_id = ? AND room_id = ?;

-- name: DeleteReadCursor :exec
DELETE FROM read_cursors WHERE user_id = ? AND room_id = ?;

-- name: InsertDMRoom :execrows
-- Creates the DM of a pair unless the pair has one (rooms_dm_pair): 1 row when created, 0 when it
-- existed. dm_a and dm_b are the sorted participants (dm_a < dm_b).
INSERT INTO rooms (id, owner_id, name, kind, dm_a, dm_b, created_at, updated_at)
VALUES (?, ?, '', 'dm', ?, ?, ?, ?)
ON CONFLICT DO NOTHING;

-- name: ListRoomParticipants :many
-- The two intelligences of a DM, by id, each with the subtype row it shares its id with (role:
-- user, ai or person) and, for a person, the kind of its connector.
SELECT sqlc.embed(i),
       CAST(CASE WHEN u.id IS NOT NULL THEN 'user' WHEN a.id IS NOT NULL THEN 'ai'
                 WHEN p.id IS NOT NULL THEN 'person' ELSE '' END AS TEXT) AS role,
       CAST(COALESCE(c.kind, '') AS TEXT) AS platform
FROM rooms r
JOIN intelligences i ON i.id = r.dm_a OR i.id = r.dm_b
LEFT JOIN users u ON u.id = i.id
LEFT JOIN agents a ON a.id = i.id
LEFT JOIN people p ON p.id = i.id
LEFT JOIN connectors c ON c.id = p.connector_id
WHERE r.id = ? AND r.kind = 'dm'
ORDER BY i.id;

-- name: ListLastMessages :many
-- The newest message that still exists of each room (retention may have deleted the latest ones).
SELECT v.* FROM message_views v
JOIN (SELECT m.room_id, MAX(m.seq) AS seq FROM messages m
      WHERE m.room_id IN (sqlc.slice('room_ids')) GROUP BY m.room_id) newest
  ON newest.room_id = v.room_id AND newest.seq = v.seq;

-- name: CountUnreadByRoom :many
-- Per room, the messages after the user's read position and after the room's baseline (history
-- from before the upgrade counts as read) that the user did not write (DM-06). Rooms without
-- unread messages have no row.
SELECT m.room_id, COUNT(*) AS unread
FROM messages m
JOIN rooms r ON r.id = m.room_id
LEFT JOIN read_cursors c ON c.room_id = m.room_id AND c.user_id = sqlc.arg(user_id)
WHERE m.room_id IN (sqlc.slice('room_ids'))
  AND m.seq > MAX(COALESCE(c.seq, 0), r.read_baseline)
  AND m.author_iid <> sqlc.arg(user_id)
GROUP BY m.room_id;

-- name: ListMemberNames :many
-- The users and AIs that are members of the rooms, in the order they were added.
SELECT m.room_id, i.display_name, i.handle
FROM room_members m JOIN intelligences i ON i.id = m.member_id
WHERE m.kind IN ('agent', 'user') AND m.room_id IN (sqlc.slice('room_ids'))
ORDER BY m.room_id, m.created_at, m.id;

-- name: DeleteEmptyDMRoom :execrows
-- Deletes a DM nobody has written in: the one a failed first post leaves behind.
DELETE FROM rooms WHERE id = ? AND kind = 'dm' AND next_seq = 1;

-- name: ListRoomActivity :many
-- The name, kind and last activity of rooms: the time of the newest message that still exists,
-- else the room's creation (as the conversation list).
SELECT r.id, r.name, r.kind,
       CAST(COALESCE((SELECT m.created_at FROM messages m WHERE m.room_id = r.id ORDER BY m.seq DESC LIMIT 1), r.created_at) AS INTEGER) AS activity_at
FROM rooms r
WHERE r.id IN (sqlc.slice('ids'));

-- name: ListChatDMRoomIDs :many
-- The DMs a chat is a member of: a platform DM chat sits in the DM with its connector's operator
-- (ACC-02).
SELECT r.id FROM room_members m JOIN rooms r ON r.id = m.room_id
WHERE m.kind = 'chat' AND m.member_id = ? AND r.kind = 'dm'
ORDER BY r.id;

-- name: ListAgentDMRooms :many
-- The DMs an AI takes part in (merge, MRG-01).
SELECT * FROM rooms WHERE kind = 'dm' AND (dm_a = sqlc.arg(id) OR dm_b = sqlc.arg(id)) ORDER BY id;

-- name: DeleteDuplicateAgentGroupMemberships :many
-- Merge: the groups where both AIs are members keep the surviving AI's membership and settings.
DELETE FROM room_members
WHERE room_members.kind = 'agent' AND room_members.member_id = sqlc.arg(from_id)
  AND room_members.room_id IN (SELECT g.id FROM rooms g WHERE g.kind = 'group')
  AND room_members.room_id IN (SELECT o.room_id FROM room_members o WHERE o.kind = 'agent' AND o.member_id = sqlc.arg(into_id))
RETURNING room_id;

-- name: MoveAgentGroupMemberships :many
-- Merge: the groups only the merged AI is a member of become the surviving AI's, settings included.
UPDATE room_members SET member_id = sqlc.arg(into_id)
WHERE room_members.kind = 'agent' AND room_members.member_id = sqlc.arg(from_id)
  AND room_members.room_id IN (SELECT g.id FROM rooms g WHERE g.kind = 'group')
RETURNING room_id;

-- name: MergeAgentGroupCursors :exec
-- Merge: the surviving AI's delivery cursor in each group it is a member of is the larger of the two.
INSERT INTO agent_cursors (agent_id, room_id, seq)
SELECT sqlc.arg(into_id), c.room_id, c.seq
FROM agent_cursors c JOIN rooms r ON r.id = c.room_id
WHERE c.agent_id = sqlc.arg(from_id) AND r.kind = 'group'
  AND EXISTS (SELECT 1 FROM room_members m WHERE m.room_id = c.room_id AND m.kind = 'agent' AND m.member_id = sqlc.arg(into_id))
ON CONFLICT (agent_id, room_id) DO UPDATE SET seq = MAX(agent_cursors.seq, excluded.seq);

-- name: MergeAgentGrants :exec
-- Merge: who may use the merged AI may use the surviving one; where both grant a subject the higher
-- level wins.
INSERT INTO grants (resource_kind, resource_id, subject, level, created_by, created_at)
SELECT g.resource_kind, sqlc.arg(into_id), g.subject, g.level, g.created_by, g.created_at
FROM grants g
WHERE g.resource_kind = 'agent' AND g.resource_id = sqlc.arg(from_id)
ON CONFLICT (resource_kind, resource_id, subject) DO UPDATE SET level =
    CASE WHEN (CASE excluded.level WHEN 'view' THEN 1 WHEN 'post' THEN 2 WHEN 'use' THEN 3 ELSE 4 END)
              > (CASE grants.level WHEN 'view' THEN 1 WHEN 'post' THEN 2 WHEN 'use' THEN 3 ELSE 4 END)
         THEN excluded.level ELSE grants.level END;

-- name: ReassignInviter :exec
-- Merge: the guests the merged identity invited answer to the surviving one.
UPDATE org_members SET inviter_iid = sqlc.arg(into_id) WHERE inviter_iid = sqlc.arg(from_id);

-- name: ClearSelfInviter :exec
-- Merge: when the merged identity invited the survivor itself, the survivor answers to whoever invited the
-- merged one in that organization (nobody when it had no inviter), not to itself.
UPDATE org_members SET inviter_iid = (SELECT f.inviter_iid FROM org_members f
                                      WHERE f.org_id = org_members.org_id AND f.iid = sqlc.arg(from_id))
WHERE org_members.iid = sqlc.arg(into_id) AND org_members.inviter_iid = sqlc.arg(into_id);

-- name: ReassignInvitationInviter :exec
-- Merge: a pending invitation the merged identity sent is the surviving one's.
UPDATE org_invitations SET invited_by = sqlc.arg(into_id) WHERE invited_by = sqlc.arg(from_id);

-- name: MoveAgentOrgMemberships :many
-- Merge: the organizations only the merged AI belongs to become the surviving AI's, with the permission,
-- position and inviter it had there. Where both belong, the survivor's membership stays (it wins).
UPDATE org_members SET iid = sqlc.arg(into_id)
WHERE org_members.iid = sqlc.arg(from_id)
  AND org_members.org_id NOT IN (SELECT o.org_id FROM org_members o WHERE o.iid = sqlc.arg(into_id))
RETURNING org_id;

-- name: DeleteAgentOrgMemberships :many
-- Merge: what is left of the merged AI's memberships, the organizations both belong to.
DELETE FROM org_members WHERE iid = ? RETURNING org_id;

-- name: ReassignAgentCreator :exec
-- Merge: the AIs the merged AI created are the survivor's.
UPDATE agents SET creator_iid = sqlc.arg(into_id) WHERE creator_iid = sqlc.arg(from_id);

-- name: ReassignRoomCreator :exec
-- Merge: the groups the merged AI created are the survivor's.
UPDATE rooms SET creator_iid = sqlc.arg(into_id) WHERE creator_iid = sqlc.arg(from_id);

-- name: ListRoomMessageOrder :many
-- A room's messages in the order they were written.
SELECT id, seq FROM messages WHERE room_id = ? ORDER BY seq;

-- name: MoveMessage :exec
UPDATE messages SET room_id = ?, seq = ? WHERE id = ?;

-- name: ListRoomChatMemberRows :many
SELECT * FROM room_members WHERE room_id = ? AND kind = 'chat' ORDER BY created_at, id;

-- name: MoveRoomMember :exec
UPDATE room_members SET room_id = ? WHERE id = ?;

-- name: ListRoomAgentCursors :many
SELECT agent_id, seq FROM agent_cursors WHERE room_id = ?;

-- name: ListRoomReadCursors :many
SELECT user_id, seq FROM read_cursors WHERE room_id = ?;

-- name: SetDMRoomPair :exec
-- Merge: a DM of the merged AI becomes the surviving AI's (dm_a and dm_b are sorted).
UPDATE rooms SET dm_a = ?, dm_b = ?, updated_at = ? WHERE id = ? AND kind = 'dm';

-- name: ReplaceAgentMember :exec
UPDATE room_members SET member_id = sqlc.arg(into_id) WHERE room_id = sqlc.arg(room_id) AND kind = 'agent' AND member_id = sqlc.arg(from_id);

-- name: ReplaceAgentCursor :exec
UPDATE agent_cursors SET agent_id = sqlc.arg(into_id) WHERE room_id = sqlc.arg(room_id) AND agent_id = sqlc.arg(from_id);

-- name: ReassignAgentMessages :exec
-- Merge: the messages the merged AI wrote are the surviving AI's (author_id for agent messages,
-- author_iid for those of any kind, such as the ones typed on the platform account it operated).
UPDATE messages SET author_id = sqlc.arg(into_id) WHERE author_kind = 'agent' AND author_id = sqlc.arg(from_id);

-- name: ReassignAuthorIntelligence :exec
UPDATE messages SET author_iid = sqlc.arg(into_id) WHERE author_iid = sqlc.arg(from_id);

-- name: ReassignConnectorOperator :many
UPDATE connectors SET operator_iid = sqlc.arg(into_id) WHERE operator_iid = sqlc.arg(from_id)
RETURNING id;
