-- Ozon module phase 1 queries (campaigns, products, stats, prices, sync state).

-- name: UpsertOzonCampaign :one
INSERT INTO ozon_campaigns (
    seller_cabinet_id, ozon_campaign_id, title, adv_object_type, state,
    placement, autopilot_strategy, daily_budget_rub, weekly_budget_rub,
    from_date, to_date
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
ON CONFLICT (seller_cabinet_id, ozon_campaign_id) DO UPDATE SET
    title = EXCLUDED.title,
    adv_object_type = EXCLUDED.adv_object_type,
    state = EXCLUDED.state,
    placement = EXCLUDED.placement,
    autopilot_strategy = EXCLUDED.autopilot_strategy,
    daily_budget_rub = EXCLUDED.daily_budget_rub,
    weekly_budget_rub = EXCLUDED.weekly_budget_rub,
    from_date = EXCLUDED.from_date,
    to_date = EXCLUDED.to_date,
    updated_at = now()
RETURNING *;

-- name: GetOzonCampaignByID :one
SELECT * FROM ozon_campaigns WHERE id = $1;

-- name: ListOzonCampaignsByCabinet :many
SELECT * FROM ozon_campaigns
WHERE seller_cabinet_id = $1
ORDER BY ozon_campaign_id DESC
LIMIT $2 OFFSET $3;

-- name: CountOzonCampaignsByCabinet :one
SELECT COUNT(*) FROM ozon_campaigns WHERE seller_cabinet_id = $1;

-- name: ListOzonCampaignRefsByCabinet :many
SELECT id, ozon_campaign_id FROM ozon_campaigns
WHERE seller_cabinet_id = $1;

-- name: UpsertOzonCampaignProduct :exec
INSERT INTO ozon_campaign_products (campaign_id, sku, bid_rub, target_cir, top_position, is_active)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (campaign_id, sku) DO UPDATE SET
    bid_rub = EXCLUDED.bid_rub,
    target_cir = EXCLUDED.target_cir,
    top_position = EXCLUDED.top_position,
    is_active = EXCLUDED.is_active,
    updated_at = now();

-- name: ListOzonCampaignProducts :many
SELECT * FROM ozon_campaign_products
WHERE campaign_id = $1
ORDER BY sku;

-- name: UpsertOzonCampaignStat :exec
INSERT INTO ozon_campaign_stats (campaign_id, date, views, clicks, spend_rub, orders, revenue_rub)
VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT (campaign_id, date) DO UPDATE SET
    views = EXCLUDED.views,
    clicks = EXCLUDED.clicks,
    spend_rub = EXCLUDED.spend_rub,
    orders = EXCLUDED.orders,
    revenue_rub = EXCLUDED.revenue_rub;

-- name: ListOzonCampaignStats :many
SELECT * FROM ozon_campaign_stats
WHERE campaign_id = $1 AND date BETWEEN $2 AND $3
ORDER BY date;

-- name: AggregateOzonCampaignStatsByCabinet :many
SELECT
    s.campaign_id,
    COALESCE(SUM(s.views), 0)::bigint AS views,
    COALESCE(SUM(s.clicks), 0)::bigint AS clicks,
    COALESCE(SUM(s.spend_rub), 0)::numeric AS spend_rub,
    COALESCE(SUM(s.orders), 0)::bigint AS orders,
    COALESCE(SUM(s.revenue_rub), 0)::numeric AS revenue_rub
FROM ozon_campaign_stats s
JOIN ozon_campaigns c ON c.id = s.campaign_id
WHERE c.seller_cabinet_id = $1 AND s.date >= $2
GROUP BY s.campaign_id;

-- name: UpsertOzonProductPrice :exec
INSERT INTO ozon_product_prices (
    seller_cabinet_id, sku, offer_id, name, price_rub, old_price_rub,
    min_price_rub, net_price_rub, marketing_seller_price_rub, color_index,
    commission_fbo_pct, commission_fbs_pct, acquiring_pct, synced_at
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, now())
ON CONFLICT (seller_cabinet_id, sku) DO UPDATE SET
    offer_id = EXCLUDED.offer_id,
    name = COALESCE(EXCLUDED.name, ozon_product_prices.name),
    price_rub = EXCLUDED.price_rub,
    old_price_rub = EXCLUDED.old_price_rub,
    min_price_rub = EXCLUDED.min_price_rub,
    net_price_rub = EXCLUDED.net_price_rub,
    marketing_seller_price_rub = EXCLUDED.marketing_seller_price_rub,
    color_index = EXCLUDED.color_index,
    commission_fbo_pct = EXCLUDED.commission_fbo_pct,
    commission_fbs_pct = EXCLUDED.commission_fbs_pct,
    acquiring_pct = EXCLUDED.acquiring_pct,
    synced_at = now();

-- name: ListOzonProductPrices :many
SELECT * FROM ozon_product_prices
WHERE seller_cabinet_id = $1
  AND (
    sqlc.narg('search')::text IS NULL
    OR name ILIKE '%' || sqlc.narg('search')::text || '%'
    OR offer_id ILIKE '%' || sqlc.narg('search')::text || '%'
    OR sku::text = sqlc.narg('search')::text
  )
ORDER BY offer_id, sku
LIMIT $2 OFFSET $3;

-- name: CountOzonProductPrices :one
SELECT COUNT(*) FROM ozon_product_prices
WHERE seller_cabinet_id = $1
  AND (
    sqlc.narg('search')::text IS NULL
    OR name ILIKE '%' || sqlc.narg('search')::text || '%'
    OR offer_id ILIKE '%' || sqlc.narg('search')::text || '%'
    OR sku::text = sqlc.narg('search')::text
  );

-- name: TouchOzonSyncState :exec
INSERT INTO ozon_sync_states (
    seller_cabinet_id, campaigns_synced_at, stats_synced_at, prices_synced_at, last_error, updated_at
)
VALUES (
    $1,
    sqlc.narg('campaigns_synced_at'),
    sqlc.narg('stats_synced_at'),
    sqlc.narg('prices_synced_at'),
    sqlc.narg('last_error'),
    now()
)
ON CONFLICT (seller_cabinet_id) DO UPDATE SET
    campaigns_synced_at = COALESCE(EXCLUDED.campaigns_synced_at, ozon_sync_states.campaigns_synced_at),
    stats_synced_at = COALESCE(EXCLUDED.stats_synced_at, ozon_sync_states.stats_synced_at),
    prices_synced_at = COALESCE(EXCLUDED.prices_synced_at, ozon_sync_states.prices_synced_at),
    last_error = EXCLUDED.last_error,
    updated_at = now();

-- name: GetOzonSyncState :one
SELECT * FROM ozon_sync_states WHERE seller_cabinet_id = $1;
