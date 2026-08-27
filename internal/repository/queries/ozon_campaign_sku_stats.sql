-- UpsertOzonCampaignSkuStat writes one day of one campaign's SKU counters
-- from the Performance API campaign report (idempotent re-sync).
-- name: UpsertOzonCampaignSkuStat :exec
INSERT INTO ozon_campaign_sku_stats (
    seller_cabinet_id, ozon_campaign_id, sku, date,
    views, clicks, spend_rub, orders, revenue_rub
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
ON CONFLICT (seller_cabinet_id, ozon_campaign_id, sku, date) DO UPDATE SET
    views       = EXCLUDED.views,
    clicks      = EXCLUDED.clicks,
    spend_rub   = EXCLUDED.spend_rub,
    orders      = EXCLUDED.orders,
    revenue_rub = EXCLUDED.revenue_rub;

-- AggregateOzonCampaignSkuStats sums one campaign's per-SKU counters over a
-- date window — «сколько заказов принесла именно эта кампания этому SKU».
-- name: AggregateOzonCampaignSkuStats :many
SELECT
    sku,
    COALESCE(SUM(views), 0)::BIGINT        AS views,
    COALESCE(SUM(clicks), 0)::BIGINT       AS clicks,
    COALESCE(SUM(spend_rub), 0)::NUMERIC   AS spend_rub,
    COALESCE(SUM(orders), 0)::BIGINT       AS orders,
    COALESCE(SUM(revenue_rub), 0)::NUMERIC AS revenue_rub
FROM ozon_campaign_sku_stats
WHERE seller_cabinet_id = $1
  AND ozon_campaign_id = $2
  AND date >= sqlc.arg(date_from)::DATE
GROUP BY sku;
