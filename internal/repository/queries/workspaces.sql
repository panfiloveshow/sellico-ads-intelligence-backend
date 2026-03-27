-- name: CreateWorkspace :one
INSERT INTO workspaces (name, slug)
VALUES ($1, $2)
RETURNING *;

-- name: GetWorkspaceByID :one
SELECT * FROM workspaces
WHERE id = $1 AND deleted_at IS NULL;

-- name: GetWorkspaceBySlug :one
SELECT * FROM workspaces
WHERE slug = $1 AND deleted_at IS NULL;

-- name: ListWorkspacesByUserID :many
SELECT w.* FROM workspaces w
JOIN workspace_members wm ON w.id = wm.workspace_id
WHERE wm.user_id = $1 AND w.deleted_at IS NULL
ORDER BY w.created_at DESC
LIMIT $2 OFFSET $3;

-- name: ListWorkspaces :many
SELECT * FROM workspaces
WHERE deleted_at IS NULL
ORDER BY created_at DESC
LIMIT $1 OFFSET $2;

-- name: UpdateWorkspace :one
UPDATE workspaces
SET name = $2, slug = $3, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: SoftDeleteWorkspace :exec
UPDATE workspaces SET deleted_at = now() WHERE id = $1;
