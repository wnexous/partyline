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
