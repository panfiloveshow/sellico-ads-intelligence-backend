-- name: CreateWorkspaceMember :one
INSERT INTO workspace_members (workspace_id, user_id, role)
VALUES ($1, $2, $3)
RETURNING *;

-- name: GetWorkspaceMember :one
SELECT * FROM workspace_members
WHERE workspace_id = $1 AND user_id = $2;

-- name: GetWorkspaceMemberByID :one
SELECT * FROM workspace_members WHERE id = $1;

-- name: ListWorkspaceMembers :many
SELECT * FROM workspace_members
WHERE workspace_id = $1
ORDER BY created_at ASC
LIMIT $2 OFFSET $3;

-- name: UpdateWorkspaceMemberRole :one
UPDATE workspace_members
SET role = $2, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: DeleteWorkspaceMember :exec
DELETE FROM workspace_members WHERE id = $1;
