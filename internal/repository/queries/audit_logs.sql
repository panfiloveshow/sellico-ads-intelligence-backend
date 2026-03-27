-- name: CreateAuditLog :one
INSERT INTO audit_logs (workspace_id, user_id, action, entity_type, entity_id, metadata)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: ListAuditLogsByWorkspace :many
SELECT * FROM audit_logs
WHERE workspace_id = $1
ORDER BY created_at DESC
LIMIT $2 OFFSET $3;

-- name: ListAuditLogsFiltered :many
SELECT * FROM audit_logs
WHERE workspace_id = $1
  AND (sqlc.narg('action_filter')::text IS NULL OR action = sqlc.narg('action_filter')::text)
  AND (sqlc.narg('entity_type_filter')::text IS NULL OR entity_type = sqlc.narg('entity_type_filter')::text)
  AND (sqlc.narg('user_id_filter')::uuid IS NULL OR user_id = sqlc.narg('user_id_filter')::uuid)
  AND (sqlc.narg('date_from')::timestamptz IS NULL OR created_at >= sqlc.narg('date_from')::timestamptz)
  AND (sqlc.narg('date_to')::timestamptz IS NULL OR created_at <= sqlc.narg('date_to')::timestamptz)
ORDER BY created_at DESC
LIMIT $2 OFFSET $3;
