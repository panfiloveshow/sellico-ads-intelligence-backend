-- Ozon module phase 2 queries (campaign actions, bid audit, CPO, strategies).

-- name: InsertOzonBidChange :one
INSERT INTO ozon_bid_changes (
    campaign_id, seller_cabinet_id, kind, sku, old_bid_rub, new_bid_rub,
    reason, source, status, decision_context
)
VALUES (
    sqlc.narg('campaign_id'), sqlc.narg('seller_cabinet_id'), sqlc.arg('kind'),
    sqlc.narg('sku'), sqlc.narg('old_bid_rub'), sqlc.narg('new_bid_rub'),
    sqlc.narg('reason'), sqlc.arg('source'), sqlc.arg('status'),
    sqlc.narg('decision_context')
)
RETURNING *;

-- name: MarkOzonBidChangeResult :exec
UPDATE ozon_bid_changes
SET status = sqlc.arg('status'),
    error = sqlc.narg('error'),
    applied_at = CASE WHEN sqlc.arg('status')::text = 'applied' THEN now() ELSE applied_at END
WHERE id = sqlc.arg('id');

-- name: ListOzonBidChangesByCabinet :many
SELECT bc.* FROM ozon_bid_changes bc
LEFT JOIN ozon_campaigns c ON c.id = bc.campaign_id
WHERE COALESCE(bc.seller_cabinet_id, c.seller_cabinet_id) = sqlc.arg('seller_cabinet_id')
  AND (sqlc.narg('campaign_id')::uuid IS NULL OR bc.campaign_id = sqlc.narg('campaign_id')::uuid)
ORDER BY bc.created_at DESC
LIMIT sqlc.arg('limit') OFFSET sqlc.arg('offset');

-- name: CountOzonBidChangesByCabinet :one
SELECT COUNT(*) FROM ozon_bid_changes bc
LEFT JOIN ozon_campaigns c ON c.id = bc.campaign_id
WHERE COALESCE(bc.seller_cabinet_id, c.seller_cabinet_id) = sqlc.arg('seller_cabinet_id')
  AND (sqlc.narg('campaign_id')::uuid IS NULL OR bc.campaign_id = sqlc.narg('campaign_id')::uuid);

-- GetOzonStrategyGuardState feeds the per-(campaign, sku) cooldown and
-- daily-cap guardrails. The deterministic strategy scales every SKU of a
-- campaign uniformly, so the worst (max) per-SKU counter is the campaign
-- guard value. Shadow rows count too: promoting a strategy to live must not
-- bypass a cooldown the shadow run just started.
-- name: GetOzonStrategyGuardState :one
SELECT
    COALESCE(MAX(per_sku.changes_today), 0)::bigint AS max_changes_per_sku_today,
    MAX(per_sku.last_change_at)::timestamptz AS last_change_at
FROM (
    SELECT
        sku,
        COUNT(*) FILTER (WHERE created_at >= sqlc.arg('day_start')) AS changes_today,
        MAX(created_at) AS last_change_at
    FROM ozon_bid_changes
    WHERE campaign_id = sqlc.arg('campaign_id')
      AND source = 'strategy'
      AND status IN ('pending', 'applied', 'shadow')
    GROUP BY sku
) per_sku;

-- name: UpdateOzonCampaignState :exec
UPDATE ozon_campaigns SET state = $2, updated_at = now() WHERE id = $1;

-- name: UpdateOzonCampaignBudgets :exec
UPDATE ozon_campaigns SET
    daily_budget_rub = COALESCE(sqlc.narg('daily_budget_rub'), daily_budget_rub),
    weekly_budget_rub = COALESCE(sqlc.narg('weekly_budget_rub'), weekly_budget_rub),
    updated_at = now()
WHERE id = sqlc.arg('id');

-- name: UpdateOzonCampaignProductBid :exec
UPDATE ozon_campaign_products SET
    bid_rub = sqlc.arg('bid_rub'),
    target_cir = COALESCE(sqlc.narg('target_cir'), target_cir),
    updated_at = now()
WHERE campaign_id = sqlc.arg('campaign_id') AND sku = sqlc.arg('sku');

-- name: UpsertOzonCpoProduct :exec
-- The enriched display fields (offer_id/name/price_rub/bid_price_rub/image_url/
-- visibility_index) are COALESCE-preserved so a bare toggle/bid upsert (which
-- passes them as NULL) never wipes values captured by the product refresh.
INSERT INTO ozon_cpo_products (
    seller_cabinet_id, sku, enabled, bid, bid_kind,
    offer_id, name, price_rub, bid_price_rub, image_url, visibility_index
)
VALUES (
    sqlc.arg('seller_cabinet_id'), sqlc.arg('sku'), sqlc.arg('enabled'),
    sqlc.narg('bid'), sqlc.narg('bid_kind'),
    sqlc.narg('offer_id'), sqlc.narg('name'), sqlc.narg('price_rub'),
    sqlc.narg('bid_price_rub'), sqlc.narg('image_url'), sqlc.narg('visibility_index')
)
ON CONFLICT (seller_cabinet_id, sku) DO UPDATE SET
    enabled = EXCLUDED.enabled,
    bid = COALESCE(EXCLUDED.bid, ozon_cpo_products.bid),
    bid_kind = COALESCE(EXCLUDED.bid_kind, ozon_cpo_products.bid_kind),
    offer_id = COALESCE(EXCLUDED.offer_id, ozon_cpo_products.offer_id),
    name = COALESCE(EXCLUDED.name, ozon_cpo_products.name),
    price_rub = COALESCE(EXCLUDED.price_rub, ozon_cpo_products.price_rub),
    bid_price_rub = COALESCE(EXCLUDED.bid_price_rub, ozon_cpo_products.bid_price_rub),
    image_url = COALESCE(EXCLUDED.image_url, ozon_cpo_products.image_url),
    visibility_index = COALESCE(EXCLUDED.visibility_index, ozon_cpo_products.visibility_index),
    updated_at = now();

-- name: SetOzonCpoProductsEnabled :exec
UPDATE ozon_cpo_products SET enabled = sqlc.arg('enabled'), updated_at = now()
WHERE seller_cabinet_id = sqlc.arg('seller_cabinet_id')
  AND sku = ANY(sqlc.arg('skus')::bigint[]);

-- name: UpdateOzonCpoProductBid :exec
UPDATE ozon_cpo_products SET
    bid = sqlc.arg('bid'),
    bid_kind = COALESCE(sqlc.narg('bid_kind'), bid_kind),
    updated_at = now()
WHERE seller_cabinet_id = sqlc.arg('seller_cabinet_id') AND sku = sqlc.arg('sku');

-- name: ListOzonCpoProducts :many
SELECT * FROM ozon_cpo_products
WHERE seller_cabinet_id = $1
ORDER BY sku
LIMIT $2 OFFSET $3;

-- name: CountOzonCpoProducts :one
SELECT COUNT(*) FROM ozon_cpo_products WHERE seller_cabinet_id = $1;

-- CreateOzonStrategyBindingInWorkspace inserts an Ozon binding only when the
-- strategy belongs to the workspace AND the ozon campaign belongs to the
-- strategy's seller cabinet (tenancy in one statement, mirroring the WB
-- CreateStrategyBindingInWorkspace).
-- name: CreateOzonStrategyBindingInWorkspace :one
INSERT INTO strategy_bindings (strategy_id, ozon_campaign_id)
SELECT s.id, sqlc.arg('ozon_campaign_id')::uuid
FROM strategies s
WHERE s.id = sqlc.arg('strategy_id') AND s.workspace_id = sqlc.arg('workspace_id')
  AND EXISTS (
      SELECT 1 FROM ozon_campaigns oc
      WHERE oc.id = sqlc.arg('ozon_campaign_id')::uuid
        AND oc.seller_cabinet_id = s.seller_cabinet_id
  )
RETURNING id, strategy_id, ozon_campaign_id, created_at;

-- name: ListOzonCampaignBindingsByStrategy :many
SELECT
    b.id AS binding_id,
    b.created_at AS binding_created_at,
    c.id, c.seller_cabinet_id, c.ozon_campaign_id, c.title, c.adv_object_type,
    c.state, c.placement, c.autopilot_strategy, c.daily_budget_rub,
    c.weekly_budget_rub, c.from_date, c.to_date, c.created_at, c.updated_at
FROM strategy_bindings b
JOIN ozon_campaigns c ON c.id = b.ozon_campaign_id
WHERE b.strategy_id = $1
ORDER BY b.created_at;

-- name: AggregateOzonCampaignStatsSince :one
SELECT
    COALESCE(SUM(views), 0)::bigint AS views,
    COALESCE(SUM(clicks), 0)::bigint AS clicks,
    COALESCE(SUM(spend_rub), 0)::numeric AS spend_rub,
    COALESCE(SUM(orders), 0)::bigint AS orders,
    COALESCE(SUM(revenue_rub), 0)::numeric AS revenue_rub
FROM ozon_campaign_stats
WHERE campaign_id = $1 AND date >= $2;
