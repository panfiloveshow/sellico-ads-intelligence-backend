-- name: CreateCampaign :one
INSERT INTO campaigns (workspace_id, seller_cabinet_id, wb_campaign_id, name, status, campaign_type, bid_type, payment_type, daily_budget)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
RETURNING *;

-- name: GetCampaignByID :one
SELECT * FROM campaigns WHERE id = $1;

-- name: GetCampaignByWBCampaignID :one
SELECT * FROM campaigns
WHERE workspace_id = $1 AND wb_campaign_id = $2
LIMIT 1;

-- name: UpsertCampaign :one
INSERT INTO campaigns (workspace_id, seller_cabinet_id, wb_campaign_id, name, status, campaign_type, bid_type, payment_type, daily_budget)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
ON CONFLICT (wb_campaign_id, seller_cabinet_id) DO UPDATE SET
    name = EXCLUDED.name,
    status = EXCLUDED.status,
    campaign_type = EXCLUDED.campaign_type,
    bid_type = EXCLUDED.bid_type,
    payment_type = EXCLUDED.payment_type,
    daily_budget = EXCLUDED.daily_budget,
    updated_at = now()
RETURNING *;

-- name: ListCampaignsByWorkspace :many
SELECT * FROM campaigns
WHERE workspace_id = $1
  AND (sqlc.narg('seller_cabinet_id_filter')::uuid IS NULL OR seller_cabinet_id = sqlc.narg('seller_cabinet_id_filter')::uuid)
  AND (sqlc.narg('status_filter')::text IS NULL OR status = sqlc.narg('status_filter')::text)
  AND (sqlc.narg('name_filter')::text IS NULL OR name ILIKE '%' || sqlc.narg('name_filter')::text || '%')
ORDER BY created_at DESC
LIMIT $2 OFFSET $3;

-- name: ListCampaignsBySellerCabinet :many
SELECT * FROM campaigns
WHERE seller_cabinet_id = $1
ORDER BY created_at DESC
LIMIT $2 OFFSET $3;

-- name: UpdateCampaign :one
UPDATE campaigns
SET name = $2, status = $3, bid_type = $4, payment_type = $5, daily_budget = $6, updated_at = now()
WHERE id = $1
RETURNING *;
