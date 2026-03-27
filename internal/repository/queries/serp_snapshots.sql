-- name: CreateSERPSnapshot :one
INSERT INTO serp_snapshots (workspace_id, query, region, total_results, scanned_at)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: GetSERPSnapshotByID :one
SELECT * FROM serp_snapshots WHERE id = $1;

-- name: ListSERPSnapshotsByWorkspace :many
SELECT * FROM serp_snapshots
WHERE workspace_id = $1
ORDER BY scanned_at DESC
LIMIT $2 OFFSET $3;

-- name: ListSERPSnapshotsFiltered :many
SELECT * FROM serp_snapshots
WHERE workspace_id = $1
  AND (sqlc.narg('query_filter')::text IS NULL OR query = sqlc.narg('query_filter')::text)
  AND (sqlc.narg('region_filter')::text IS NULL OR region = sqlc.narg('region_filter')::text)
  AND (sqlc.narg('date_from')::timestamptz IS NULL OR scanned_at >= sqlc.narg('date_from')::timestamptz)
  AND (sqlc.narg('date_to')::timestamptz IS NULL OR scanned_at <= sqlc.narg('date_to')::timestamptz)
ORDER BY scanned_at DESC
LIMIT $2 OFFSET $3;
