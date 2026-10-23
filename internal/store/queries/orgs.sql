-- name: CreateOrganization :exec
INSERT INTO organizations (id, name, slug, created_by, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?);

-- name: GetOrganization :one
SELECT * FROM organizations WHERE id = ?;

-- name: GetOrganizationBySlug :one
-- The slug column ignores case, so Acme and acme are the same address.
SELECT * FROM organizations WHERE slug = ?;

-- name: CreatePosition :exec
INSERT INTO positions (id, org_id, name, parent_id, created_at) VALUES (?, ?, ?, ?, ?);

-- name: CountPositionName :one
-- Positions of the organization with this name, ignoring case.
SELECT COUNT(*) FROM positions WHERE org_id = ? AND name = ?;

-- name: CreateOrgMember :exec
INSERT INTO org_members (org_id, iid, permission, position_id, inviter_iid, joined_at)
VALUES (?, ?, ?, ?, ?, ?);

-- name: GetOrgMember :one
SELECT * FROM org_members WHERE org_id = ? AND iid = ?;
