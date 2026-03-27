-- name: CreateExport :one
INSERT INTO exports (workspace_id, user_id, entity_type, format, filters)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: GetExportByID :one
SELECT * FROM exports WHERE id = $1;

-- name: ListExportsByWorkspace :many
SELECT * FROM exports
WHERE workspace_id = $1
ORDER BY created_at DESC
LIMIT $2 OFFSET $3;

-- name: UpdateExportStatus :one
UPDATE exports
SET status = $2, file_path = $3, error_message = $4, updated_at = now()
WHERE id = $1
RETURNING *;
