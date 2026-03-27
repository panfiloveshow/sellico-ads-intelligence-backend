-- name: CreateSellerCabinet :one
INSERT INTO seller_cabinets (workspace_id, name, encrypted_token)
VALUES ($1, $2, $3)
RETURNING *;

-- name: GetSellerCabinetByID :one
SELECT * FROM seller_cabinets
WHERE id = $1 AND deleted_at IS NULL;

-- name: ListSellerCabinetsByWorkspace :many
SELECT * FROM seller_cabinets
WHERE workspace_id = $1 AND deleted_at IS NULL
ORDER BY created_at DESC
LIMIT $2 OFFSET $3;

-- name: ListActiveSellerCabinets :many
SELECT * FROM seller_cabinets
WHERE status = 'active' AND deleted_at IS NULL
ORDER BY created_at DESC;

-- name: ListActiveSellerCabinetsByWorkspace :many
SELECT * FROM seller_cabinets
WHERE workspace_id = $1 AND status = 'active' AND deleted_at IS NULL
ORDER BY created_at DESC;

-- name: UpdateSellerCabinetStatus :exec
UPDATE seller_cabinets
SET status = $2, updated_at = now()
WHERE id = $1;

-- name: UpdateSellerCabinetLastSynced :exec
UPDATE seller_cabinets
SET last_synced_at = now(), updated_at = now()
WHERE id = $1;

-- name: SoftDeleteSellerCabinet :exec
UPDATE seller_cabinets
SET deleted_at = now()
WHERE id = $1;
