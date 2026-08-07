-- Ozon module phase 5 queries (stocks + daily sales + per-SKU ad spend for
-- the inventory-demand and ad-linked repricer strategies).

-- name: UpsertOzonProductStock :exec
INSERT INTO ozon_product_stocks (seller_cabinet_id, sku, offer_id, present, reserved, synced_at)
VALUES ($1, $2, $3, $4, $5, now())
ON CONFLICT (seller_cabinet_id, sku) DO UPDATE SET
    offer_id = EXCLUDED.offer_id,
    present = EXCLUDED.present,
    reserved = EXCLUDED.reserved,
    synced_at = now();

-- name: ListOzonProductStocksByCabinet :many
SELECT * FROM ozon_product_stocks WHERE seller_cabinet_id = $1;

-- name: UpsertOzonSalesDaily :exec
INSERT INTO ozon_sales_daily (seller_cabinet_id, sku, date, ordered_units, revenue_rub)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (seller_cabinet_id, sku, date) DO UPDATE SET
    ordered_units = EXCLUDED.ordered_units,
    revenue_rub = EXCLUDED.revenue_rub;

-- OzonSalesVelocityByCabinet sums ordered units and revenue per sales SKU
-- over a lookback window; the caller divides by the window length to get
-- units/day.
-- name: OzonSalesVelocityByCabinet :many
SELECT sku,
       COALESCE(SUM(ordered_units), 0)::bigint  AS units,
       COALESCE(SUM(revenue_rub), 0)::numeric   AS revenue_rub
FROM ozon_sales_daily
WHERE seller_cabinet_id = $1 AND date >= $2
GROUP BY sku;

-- OzonSkuAdSpendByCabinet attributes campaign-level spend/revenue to every
-- SKU of the campaign (ozon_campaign_stats has no per-SKU split): a SKU in
-- several campaigns aggregates across them, so its ДРР is the blended
-- campaign ДРР of its placements. sku here is the SALES sku.
-- name: OzonSkuAdSpendByCabinet :many
SELECT cp.sku,
       COALESCE(SUM(s.spend_rub), 0)::numeric   AS spend_rub,
       COALESCE(SUM(s.revenue_rub), 0)::numeric AS revenue_rub
FROM ozon_campaign_products cp
JOIN ozon_campaigns c ON c.id = cp.campaign_id
JOIN ozon_campaign_stats s ON s.campaign_id = c.id
WHERE c.seller_cabinet_id = $1 AND s.date >= $2
GROUP BY cp.sku;
