-- name: CreateStrategy :one
INSERT INTO strategies (workspace_id, seller_cabinet_id, name, type, params, is_active)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: GetStrategyByID :one
SELECT * FROM strategies WHERE id = $1;

-- name: ListStrategiesByWorkspace :many
SELECT * FROM strategies
WHERE workspace_id = $1
ORDER BY created_at DESC
LIMIT $2 OFFSET $3;

-- name: ListActiveStrategiesByWorkspace :many
SELECT * FROM strategies
WHERE workspace_id = $1 AND is_active = true
ORDER BY created_at DESC;

-- name: UpdateStrategy :one
UPDATE strategies
SET name = $2, type = $3, params = $4, is_active = $5, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: DeleteStrategy :exec
DELETE FROM strategies WHERE id = $1;

-- name: CreateStrategyBinding :one
INSERT INTO strategy_bindings (strategy_id, campaign_id, product_id)
VALUES ($1, $2, $3)
RETURNING *;

-- name: ListStrategyBindings :many
SELECT * FROM strategy_bindings WHERE strategy_id = $1;

-- name: DeleteStrategyBinding :exec
DELETE FROM strategy_bindings WHERE id = $1;

-- name: CreateBidChange :one
INSERT INTO bid_changes (
    workspace_id, seller_cabinet_id, campaign_id, product_id, phrase_id,
    strategy_id, recommendation_id, placement, old_bid, new_bid,
    reason, source, acos, roas, wb_status
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)
RETURNING *;

-- name: ListBidChangesByCampaign :many
SELECT * FROM bid_changes
WHERE campaign_id = $1
ORDER BY created_at DESC
LIMIT $2 OFFSET $3;

-- name: ListBidChangesByWorkspace :many
SELECT * FROM bid_changes
WHERE workspace_id = $1
ORDER BY created_at DESC
LIMIT $2 OFFSET $3;

-- name: CreateMinusPhrase :one
INSERT INTO campaign_minus_phrases (campaign_id, phrase)
VALUES ($1, $2)
ON CONFLICT (campaign_id, phrase) DO NOTHING
RETURNING *;

-- name: ListMinusPhrases :many
SELECT * FROM campaign_minus_phrases WHERE campaign_id = $1 ORDER BY created_at DESC;

-- name: DeleteMinusPhrase :exec
DELETE FROM campaign_minus_phrases WHERE id = $1;

-- name: CreatePlusPhrase :one
INSERT INTO campaign_plus_phrases (campaign_id, phrase)
VALUES ($1, $2)
ON CONFLICT (campaign_id, phrase) DO NOTHING
RETURNING *;

-- name: ListPlusPhrases :many
SELECT * FROM campaign_plus_phrases WHERE campaign_id = $1 ORDER BY created_at DESC;

-- name: DeletePlusPhrase :exec
DELETE FROM campaign_plus_phrases WHERE id = $1;
