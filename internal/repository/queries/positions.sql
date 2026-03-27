-- name: CreatePosition :one
INSERT INTO positions (workspace_id, product_id, query, region, position, page, source, checked_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING *;

-- name: ListPositionsByProduct :many
SELECT * FROM positions
WHERE product_id = $1
ORDER BY checked_at DESC
LIMIT $2 OFFSET $3;

-- name: ListPositionsByWorkspace :many
SELECT * FROM positions
WHERE workspace_id = $1
ORDER BY checked_at DESC
LIMIT $2 OFFSET $3;

-- name: ListPositionsFiltered :many
SELECT * FROM positions
WHERE workspace_id = $1
  AND (sqlc.narg('product_id_filter')::uuid IS NULL OR product_id = sqlc.narg('product_id_filter')::uuid)
  AND (sqlc.narg('query_filter')::text IS NULL OR query = sqlc.narg('query_filter')::text)
  AND (sqlc.narg('region_filter')::text IS NULL OR region = sqlc.narg('region_filter')::text)
  AND (sqlc.narg('date_from')::timestamptz IS NULL OR checked_at >= sqlc.narg('date_from')::timestamptz)
  AND (sqlc.narg('date_to')::timestamptz IS NULL OR checked_at <= sqlc.narg('date_to')::timestamptz)
ORDER BY checked_at DESC
LIMIT $2 OFFSET $3;

-- name: GetAveragePosition :one
SELECT COALESCE(AVG(position)::float8, 0) AS avg_position FROM positions
WHERE product_id = $1 AND query = $2 AND region = $3
  AND checked_at BETWEEN $4 AND $5;
