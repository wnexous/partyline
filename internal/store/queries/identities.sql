-- name: GetIdentity :one
SELECT * FROM identities WHERE id = ?;
