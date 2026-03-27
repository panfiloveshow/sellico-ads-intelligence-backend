-- name: CreateExtensionSession :one
INSERT INTO extension_sessions (user_id, workspace_id, extension_version, last_active_at)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: GetExtensionSession :one
SELECT * FROM extension_sessions
WHERE user_id = $1 AND workspace_id = $2;

-- name: UpdateExtensionSessionActivity :exec
UPDATE extension_sessions
SET last_active_at = now()
WHERE id = $1;

-- name: UpdateExtensionSession :one
UPDATE extension_sessions
SET extension_version = $2,
    last_active_at = $3
WHERE id = $1
RETURNING *;
