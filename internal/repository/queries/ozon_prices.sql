-- Ozon module phase 4 queries (repricer: price change audit + guardrails).

-- name: InsertOzonPriceChange :one
INSERT INTO ozon_price_changes (
    seller_cabinet_id, sku, offer_id, old_price_rub, new_price_rub,
    old_old_price_rub, new_old_price_rub, min_price_rub, floor_rub,
    reason, source, strategy_id, status, decision_context
)
VALUES (
    sqlc.arg('seller_cabinet_id'), sqlc.arg('sku'), sqlc.narg('offer_id'),
    sqlc.narg('old_price_rub'), sqlc.arg('new_price_rub'),
    sqlc.narg('old_old_price_rub'), sqlc.narg('new_old_price_rub'),
    sqlc.narg('min_price_rub'), sqlc.narg('floor_rub'),
    sqlc.narg('reason'), sqlc.arg('source'), sqlc.narg('strategy_id'),
    sqlc.arg('status'), sqlc.narg('decision_context')
)
RETURNING *;

-- name: MarkOzonPriceChangeResult :exec
UPDATE ozon_price_changes
SET status = sqlc.arg('status'),
    error = sqlc.narg('error'),
    applied_at = CASE WHEN sqlc.arg('status')::text = 'applied' THEN now() ELSE applied_at END
WHERE id = sqlc.arg('id');

-- name: GetOzonPriceChangeByID :one
SELECT * FROM ozon_price_changes WHERE id = $1;

-- name: ListOzonPriceChangesByCabinet :many
SELECT * FROM ozon_price_changes
WHERE seller_cabinet_id = sqlc.arg('seller_cabinet_id')
  AND (sqlc.narg('sku')::bigint IS NULL OR sku = sqlc.narg('sku')::bigint)
ORDER BY created_at DESC
LIMIT sqlc.arg('limit') OFFSET sqlc.arg('offset');

-- name: CountOzonPriceChangesByCabinet :one
SELECT COUNT(*) FROM ozon_price_changes
WHERE seller_cabinet_id = sqlc.arg('seller_cabinet_id')
  AND (sqlc.narg('sku')::bigint IS NULL OR sku = sqlc.narg('sku')::bigint);

-- CountOzonPriceWritesBySkuSince feeds the hard hourly write limit (Ozon
-- rejects more than 10 price changes per product per hour). Only rows that
-- did (or may still) reach Ozon count: pending + applied + rolled_back
-- (a rollback is a real write too).
-- name: CountOzonPriceWritesBySkuSince :one
SELECT COUNT(*) FROM ozon_price_changes
WHERE seller_cabinet_id = sqlc.arg('seller_cabinet_id')
  AND sku = sqlc.arg('sku')
  AND status IN ('pending', 'applied', 'rolled_back')
  AND created_at >= sqlc.arg('since');

-- CountOzonStrategyDecisionsBySkuSince feeds the strategy cooldown and daily
-- cap. Shadow rows count too: promoting a strategy from dry_run to auto must
-- not bypass a cooldown the shadow run just started.
-- name: CountOzonStrategyDecisionsBySkuSince :one
SELECT COUNT(*) FROM ozon_price_changes
WHERE seller_cabinet_id = sqlc.arg('seller_cabinet_id')
  AND sku = sqlc.arg('sku')
  AND source = 'strategy'
  AND status IN ('pending', 'applied', 'shadow')
  AND created_at >= sqlc.arg('since');

-- name: UpdateOzonProductPriceMirror :exec
UPDATE ozon_product_prices SET
    price_rub = sqlc.arg('price_rub'),
    old_price_rub = COALESCE(sqlc.narg('old_price_rub'), old_price_rub),
    min_price_rub = COALESCE(sqlc.narg('min_price_rub'), min_price_rub)
WHERE seller_cabinet_id = sqlc.arg('seller_cabinet_id') AND sku = sqlc.arg('sku');

-- name: GetOzonProductPriceBySku :one
SELECT * FROM ozon_product_prices
WHERE seller_cabinet_id = $1 AND sku = $2;
