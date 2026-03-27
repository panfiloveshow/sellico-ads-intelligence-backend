-- name: CreateBidSnapshot :one
INSERT INTO bid_snapshots (phrase_id, workspace_id, competitive_bid, leadership_bid, cpm_min, captured_at)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: GetLatestBidSnapshot :one
SELECT * FROM bid_snapshots
WHERE phrase_id = $1
ORDER BY captured_at DESC
LIMIT 1;

-- name: ListBidSnapshotsByPhrase :many
SELECT * FROM bid_snapshots
WHERE phrase_id = $1
  AND (sqlc.narg('date_from')::timestamptz IS NULL OR captured_at >= sqlc.narg('date_from')::timestamptz)
  AND (sqlc.narg('date_to')::timestamptz IS NULL OR captured_at <= sqlc.narg('date_to')::timestamptz)
ORDER BY captured_at DESC
LIMIT $2 OFFSET $3;

-- name: ListBidSnapshotsByWorkspace :many
SELECT * FROM bid_snapshots
WHERE workspace_id = $1
ORDER BY captured_at DESC
LIMIT $2 OFFSET $3;
