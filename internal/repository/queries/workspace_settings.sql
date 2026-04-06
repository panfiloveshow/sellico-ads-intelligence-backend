-- name: GetWorkspaceSettings :one
SELECT settings FROM workspaces WHERE id = $1 AND deleted_at IS NULL;

-- name: UpdateWorkspaceSettings :one
UPDATE workspaces
SET settings = $2, updated_at = now()
WHERE id = $1
RETURNING id, name, slug, created_at, updated_at, deleted_at, settings;
