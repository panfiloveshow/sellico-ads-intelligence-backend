-- name: CreateExport :one
INSERT INTO exports (workspace_id, user_id, entity_type, format, filters)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: GetExportByID :one
SELECT * FROM exports WHERE id = $1;

-- name: ListExportsByWorkspace :many
SELECT * FROM exports
WHERE workspace_id = $1
  AND (sqlc.narg('user_id_filter')::uuid IS NULL OR user_id = sqlc.narg('user_id_filter')::uuid)
  AND (sqlc.narg('entity_type_filter')::text IS NULL OR entity_type = sqlc.narg('entity_type_filter')::text)
  AND (sqlc.narg('format_filter')::text IS NULL OR format = sqlc.narg('format_filter')::text)
  AND (sqlc.narg('status_filter')::text IS NULL OR status = sqlc.narg('status_filter')::text)
ORDER BY created_at DESC
LIMIT $2 OFFSET $3;

-- name: UpdateExportStatus :one
UPDATE exports
SET status = $2, file_path = $3, error_message = $4, updated_at = now()
WHERE id = $1
RETURNING *;
