-- name: CreateExtensionContextEvent :one
INSERT INTO extension_context_events (session_id, workspace_id, user_id, url, page_type, metadata)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;
