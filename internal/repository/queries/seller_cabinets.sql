-- name: CreateSellerCabinet :one
INSERT INTO seller_cabinets (workspace_id, name, encrypted_token)
VALUES ($1, $2, $3)
RETURNING *;

-- name: UpsertSellicoSellerCabinet :one
INSERT INTO seller_cabinets (
    workspace_id,
    name,
    encrypted_token,
    status,
    external_integration_id,
    source,
    integration_type,
    last_sellico_sync_at
)
VALUES ($1, $2, $3, $4, $5, 'sellico', $6, now())
ON CONFLICT (external_integration_id) DO UPDATE SET
    workspace_id = EXCLUDED.workspace_id,
    name = EXCLUDED.name,
    encrypted_token = EXCLUDED.encrypted_token,
    status = EXCLUDED.status,
    source = EXCLUDED.source,
    integration_type = EXCLUDED.integration_type,
    last_sellico_sync_at = now(),
    deleted_at = NULL,
    updated_at = now()
RETURNING *;

-- name: GetSellerCabinetByID :one
SELECT * FROM seller_cabinets
WHERE id = $1 AND deleted_at IS NULL;

-- name: GetSellerCabinetByExternalIntegrationID :one
SELECT * FROM seller_cabinets
WHERE external_integration_id = $1 AND deleted_at IS NULL;

-- name: ListSellerCabinetsByWorkspace :many
SELECT * FROM seller_cabinets
WHERE workspace_id = $1 AND deleted_at IS NULL
  AND (sqlc.narg('status_filter')::text IS NULL OR status = sqlc.narg('status_filter')::text)
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

-- name: UpdateSellerCabinetTokenCache :exec
UPDATE seller_cabinets
SET encrypted_token = $2,
    status = $3,
    source = 'sellico',
    integration_type = $4,
    last_sellico_sync_at = now(),
    updated_at = now()
WHERE id = $1;

-- name: SoftDeleteSellerCabinet :exec
UPDATE seller_cabinets
SET deleted_at = now()
WHERE id = $1;
