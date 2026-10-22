-- name: CreatePerson :exec
INSERT INTO people (id, connector_id, external_id, alt_external_id, platform_name, name_locked, last_seen_at, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?);

-- name: GetPerson :one
SELECT * FROM people WHERE id = ?;

-- name: GetPersonByExternalID :one
-- A sender id matches a person's external id or alternative id on the same connector; an
-- external id match comes first.
SELECT sqlc.embed(people), CAST(external_id = sqlc.arg(sender_id) AS INTEGER) AS exact FROM people
WHERE connector_id = sqlc.arg(connector_id)
  AND (external_id = sqlc.arg(sender_id) OR (alt_external_id <> '' AND alt_external_id = sqlc.arg(sender_id)))
ORDER BY exact DESC
LIMIT 1;

-- name: ListConnectorPeople :many
SELECT * FROM people WHERE connector_id = ? ORDER BY created_at, id;

-- name: UpdatePersonSeen :exec
UPDATE people SET alt_external_id = ?, platform_name = ?, last_seen_at = ? WHERE id = ?;

-- name: SetPersonNameLocked :exec
UPDATE people SET name_locked = ? WHERE id = ?;

-- name: DeletePerson :exec
DELETE FROM people WHERE id = ?;

-- name: UpsertChatPerson :exec
INSERT INTO chat_people (chat_id, person_id, last_seen_at) VALUES (?, ?, ?)
ON CONFLICT (chat_id, person_id) DO UPDATE SET last_seen_at = MAX(chat_people.last_seen_at, excluded.last_seen_at);

-- name: ListChatPeople :many
SELECT * FROM chat_people WHERE chat_id = ? ORDER BY last_seen_at DESC, person_id;

-- name: SetChatPerson :exec
UPDATE chats SET person_id = ? WHERE id = ?;

-- name: GetChatPerson :one
SELECT * FROM chat_people WHERE chat_id = ? AND person_id = ?;

-- name: ListChatPeopleRecent :many
-- The people most recently seen in a chat, with their intelligence and the connector kind
-- (GRP-02).
SELECT sqlc.embed(i), k.kind AS platform
FROM chat_people cp
JOIN chats c ON c.id = cp.chat_id
JOIN connectors k ON k.id = c.connector_id
JOIN intelligences i ON i.id = cp.person_id
WHERE cp.chat_id = ?
ORDER BY cp.last_seen_at DESC, cp.person_id
LIMIT ?;

-- name: ListPersonRoomIDs :many
-- The rooms that involve a person: those with a chat member where the person was seen, and
-- those where it wrote a message (a chat may have left the room since).
SELECT rm.room_id FROM room_members rm JOIN chat_people cp ON cp.chat_id = rm.member_id
WHERE rm.kind = 'chat' AND cp.person_id = sqlc.arg(id)
UNION
SELECT room_id FROM messages WHERE author_iid = sqlc.arg(id) AND author_iid <> '';

-- name: CountConnectorPeople :many
-- The people of each connector, leaving out those whose account an enabled user verified (the
-- same match as ResolveIdentityUser): they are an app of that user (PEOP-02).
SELECT p.connector_id, COUNT(*) AS people FROM people p
WHERE p.connector_id IN (sqlc.slice('connector_ids'))
  AND NOT EXISTS (
      SELECT 1 FROM identities x JOIN users xu ON xu.id = x.user_id
      WHERE x.connector_id = p.connector_id AND xu.disabled_at IS NULL
        AND (x.external_id IN (p.external_id, CASE WHEN p.alt_external_id <> '' THEN p.alt_external_id ELSE p.external_id END)
             OR (x.alt_external_id <> '' AND x.alt_external_id IN (p.external_id, CASE WHEN p.alt_external_id <> '' THEN p.alt_external_id ELSE p.external_id END))))
GROUP BY p.connector_id;

-- name: GetConnectorContactSortName :one
-- The sort key of a person of the connector, for the cursor of ListConnectorContacts; no row when
-- the id is not a person of the connector.
SELECT CAST(lower(i.display_name) AS TEXT) AS sort_name
FROM people p JOIN intelligences i ON i.id = p.id
WHERE p.connector_id = ? AND p.id = ?;

-- name: ListConnectorContacts :many
-- The people of a connector (leaving out those verified as a user, see CountConnectorPeople) in
-- name order, case-insensitively for ASCII, after the person named by after_id/after_name (both
-- empty for the first page), matching q (lower-case, may be empty) in the name, the handle or the
-- account id.
SELECT sqlc.embed(i) FROM people p JOIN intelligences i ON i.id = p.id
WHERE p.connector_id = sqlc.arg(connector_id)
  AND NOT EXISTS (
      SELECT 1 FROM identities x JOIN users xu ON xu.id = x.user_id
      WHERE x.connector_id = p.connector_id AND xu.disabled_at IS NULL
        AND (x.external_id IN (p.external_id, CASE WHEN p.alt_external_id <> '' THEN p.alt_external_id ELSE p.external_id END)
             OR (x.alt_external_id <> '' AND x.alt_external_id IN (p.external_id, CASE WHEN p.alt_external_id <> '' THEN p.alt_external_id ELSE p.external_id END))))
  AND (CAST(sqlc.arg(q) AS TEXT) = '' OR instr(lower(i.display_name), CAST(sqlc.arg(q) AS TEXT)) > 0
       OR instr(i.handle, CAST(sqlc.arg(q) AS TEXT)) > 0 OR instr(lower(p.external_id), CAST(sqlc.arg(q) AS TEXT)) > 0)
  AND (CAST(sqlc.arg(after_id) AS TEXT) = '' OR lower(i.display_name) > CAST(sqlc.arg(after_name) AS TEXT)
       OR (lower(i.display_name) = CAST(sqlc.arg(after_name) AS TEXT) AND i.id > CAST(sqlc.arg(after_id) AS TEXT)))
ORDER BY lower(i.display_name), i.id
LIMIT sqlc.arg(row_limit);
