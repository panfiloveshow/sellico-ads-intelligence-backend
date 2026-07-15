-- name: GetWorkspaceSettings :one
SELECT settings FROM workspaces WHERE id = $1 AND deleted_at IS NULL;

-- name: UpdateWorkspaceSettings :one
WITH automation_lock AS MATERIALIZED (
    SELECT pg_advisory_xact_lock(hashtextextended($1::text || ':workspace-daily-bid-actions', 0))
)
UPDATE workspaces w
SET settings = $2, updated_at = now()
FROM automation_lock
WHERE w.id = $1
RETURNING w.id, w.name, w.slug, w.created_at, w.updated_at, w.deleted_at, w.settings;
