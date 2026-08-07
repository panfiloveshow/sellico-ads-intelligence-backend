-- Ozon search query statistics (Performance API phrases report mirror).

-- name: UpsertOzonSearchQuery :exec
INSERT INTO ozon_search_queries (
    seller_cabinet_id, sku, query, date, views, clicks, orders, spend_rub, avg_position
)
VALUES (
    sqlc.arg('seller_cabinet_id'), sqlc.arg('sku'), sqlc.arg('query'), sqlc.arg('date'),
    sqlc.arg('views'), sqlc.arg('clicks'), sqlc.arg('orders'),
    sqlc.arg('spend_rub'), sqlc.narg('avg_position')
)
ON CONFLICT (seller_cabinet_id, sku, query, date) DO UPDATE SET
    views = EXCLUDED.views,
    clicks = EXCLUDED.clicks,
    orders = EXCLUDED.orders,
    spend_rub = EXCLUDED.spend_rub,
    avg_position = COALESCE(EXCLUDED.avg_position, ozon_search_queries.avg_position);

-- ListOzonPhraseCampaignRefs picks the campaigns the phrases sync asks Ozon
-- about: SKU / SEARCH_PROMO campaigns currently running.
-- name: ListOzonPhraseCampaignRefs :many
SELECT id, ozon_campaign_id FROM ozon_campaigns
WHERE seller_cabinet_id = $1
  AND state = 'CAMPAIGN_STATE_RUNNING'
  AND adv_object_type IN ('SKU', 'SEARCH_PROMO')
ORDER BY ozon_campaign_id;

-- AggregateOzonSearchQueries powers GET /ozon/search-queries: per-query
-- aggregate over a date window. avg_position is weighted by views over the
-- rows that actually reported a position; top_sku is the highest-views SKU of
-- the query (used for product-name enrichment).
-- name: AggregateOzonSearchQueries :many
SELECT
    q.query,
    SUM(q.views)::bigint AS views,
    SUM(q.clicks)::bigint AS clicks,
    SUM(q.orders)::bigint AS orders,
    COALESCE(SUM(q.spend_rub), 0)::numeric AS spend_rub,
    (SUM(q.avg_position * GREATEST(q.views, 1)) FILTER (WHERE q.avg_position IS NOT NULL)
        / NULLIF(SUM(GREATEST(q.views, 1)) FILTER (WHERE q.avg_position IS NOT NULL), 0))::numeric AS avg_position,
    (ARRAY_AGG(q.sku ORDER BY q.views DESC))[1]::bigint AS top_sku
FROM ozon_search_queries q
WHERE q.seller_cabinet_id = sqlc.arg('seller_cabinet_id')
  AND q.date >= sqlc.arg('date_from')
  AND (sqlc.narg('sku')::bigint IS NULL OR q.sku = sqlc.narg('sku')::bigint)
  AND (sqlc.narg('search')::text IS NULL OR q.query ILIKE '%' || sqlc.narg('search')::text || '%')
GROUP BY q.query
ORDER BY SUM(q.views) DESC, q.query
LIMIT sqlc.arg('limit') OFFSET sqlc.arg('offset');

-- name: CountOzonSearchQueries :one
SELECT COUNT(DISTINCT q.query) FROM ozon_search_queries q
WHERE q.seller_cabinet_id = sqlc.arg('seller_cabinet_id')
  AND q.date >= sqlc.arg('date_from')
  AND (sqlc.narg('sku')::bigint IS NULL OR q.sku = sqlc.narg('sku')::bigint)
  AND (sqlc.narg('search')::text IS NULL OR q.query ILIKE '%' || sqlc.narg('search')::text || '%');

-- ListOzonSearchQueriesBySkus serves the AI context pack / request_data:
-- per-(sku, query) aggregates ordered by views within each SKU. The per-SKU
-- top-N cut happens in Go (sqlc's analyzer cannot resolve ROW_NUMBER()
-- filters); the LIMIT is a defensive bound only.
-- name: ListOzonSearchQueriesBySkus :many
SELECT
    q.sku,
    q.query,
    SUM(q.views)::bigint AS views,
    SUM(q.clicks)::bigint AS clicks,
    SUM(q.orders)::bigint AS orders,
    COALESCE(SUM(q.spend_rub), 0)::numeric AS spend_rub,
    (SUM(q.avg_position * GREATEST(q.views, 1)) FILTER (WHERE q.avg_position IS NOT NULL)
        / NULLIF(SUM(GREATEST(q.views, 1)) FILTER (WHERE q.avg_position IS NOT NULL), 0))::numeric AS avg_position
FROM ozon_search_queries q
WHERE q.seller_cabinet_id = sqlc.arg('seller_cabinet_id')
  AND q.sku = ANY(sqlc.arg('skus')::bigint[])
  AND q.date >= sqlc.arg('date_from')
GROUP BY q.sku, q.query
ORDER BY q.sku, SUM(q.views) DESC, q.query
LIMIT 20000;
