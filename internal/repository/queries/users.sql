-- name: CreateUser :one
INSERT INTO users (email, password_hash, name)
VALUES ($1, $2, $3)
RETURNING *;

-- name: CreateExternalUser :one
INSERT INTO users (email, password_hash, name, external_user_id, auth_source)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: GetUserByID :one
SELECT * FROM users WHERE id = $1;

-- name: GetUserByEmail :one
SELECT * FROM users WHERE email = $1;

-- name: GetUserByExternalUserID :one
SELECT * FROM users WHERE external_user_id = $1;

-- name: UpdateUser :one
UPDATE users
SET name = $2, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: UpdateExternalUser :one
UPDATE users
SET email = $2,
    name = $3,
    external_user_id = $4,
    auth_source = $5,
    updated_at = now()
WHERE id = $1
RETURNING *;

-- name: ListUsers :many
SELECT * FROM users
ORDER BY created_at DESC
LIMIT $1 OFFSET $2;
