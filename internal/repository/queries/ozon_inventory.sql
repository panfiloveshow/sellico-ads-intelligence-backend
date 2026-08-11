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

-- --- «ДРР от общего оборота»: cabinet-wide turnover vs cabinet-wide ad spend ---
--
-- Both sides are cabinet-scoped on purpose. A SKU can sit in several
-- campaigns, so splitting the turnover per campaign needs an attribution rule
-- that does not exist yet; the cabinet aggregate has no double counting.

-- AggregateOzonCabinetTotalSalesSince sums the cabinet's whole turnover over a
-- lookback window. last_date is the newest day that actually has data — the
-- caller uses it to tell fresh numbers from a stalled ozon:sync_analytics.
-- name: AggregateOzonCabinetTotalSalesSince :one
SELECT COALESCE(SUM(revenue_rub), 0)::numeric AS revenue_rub,
       MAX(date)::date                        AS last_date
FROM ozon_sales_daily
WHERE seller_cabinet_id = sqlc.arg('seller_cabinet_id')
  AND date >= sqlc.arg('since');

-- AggregateOzonCabinetAdSpendSince sums the ad spend of every campaign of the
-- cabinet over the same window — the numerator of the total ДРР.
-- name: AggregateOzonCabinetAdSpendSince :one
SELECT COALESCE(SUM(s.spend_rub), 0)::numeric AS spend_rub
FROM ozon_campaign_stats s
JOIN ozon_campaigns c ON c.id = s.campaign_id
WHERE c.seller_cabinet_id = sqlc.arg('seller_cabinet_id')
  AND s.date >= sqlc.arg('since');

-- GetOzonCabinetSalesWindowTotals is the closed-window variant used by the AI
-- impact sweep (before/after comparison around an applied decision).
-- name: GetOzonCabinetSalesWindowTotals :one
SELECT COALESCE(SUM(revenue_rub), 0)::numeric   AS revenue_rub,
       COALESCE(SUM(ordered_units), 0)::bigint  AS ordered_units,
       COUNT(*)::bigint                         AS days
FROM ozon_sales_daily
WHERE seller_cabinet_id = sqlc.arg('seller_cabinet_id')
  AND date BETWEEN sqlc.arg('date_from') AND sqlc.arg('date_to');

-- OzonCampaignAttributedTurnoverByCabinet splits the cabinet's turnover across
-- its campaigns so a per-campaign «ДРР от общего оборота» can be computed
-- without double counting.
--
-- A SKU advertised by several campaigns has its turnover divided between them
-- in proportion to what each campaign spent over the window:
--
--   attributed(C) = Σ over SKUs of C:  turnover(sku) × spend(C) / spend_on(sku)
--
-- Summed over every campaign that advertises a SKU this returns exactly that
-- SKU's turnover — never more. A SKU with no ad spend anywhere contributes
-- nothing: NULLIF drops it rather than dividing by zero.
--
-- revenue_shared flags campaigns whose SKUs are also advertised elsewhere, so
-- a decision context can say whether the denominator was split or whole.
-- name: OzonCampaignAttributedTurnoverByCabinet :many
WITH campaign_spend AS (
    SELECT c.id AS campaign_id,
           COALESCE(SUM(s.spend_rub), 0)::numeric AS spend_rub
    FROM ozon_campaigns c
    LEFT JOIN ozon_campaign_stats s
           ON s.campaign_id = c.id AND s.date >= sqlc.arg('since')
    WHERE c.seller_cabinet_id = sqlc.arg('seller_cabinet_id')
    GROUP BY c.id
),
sku_spend AS (
    SELECT cp.sku, SUM(cs.spend_rub)::numeric AS spend_rub
    FROM ozon_campaign_products cp
    JOIN campaign_spend cs ON cs.campaign_id = cp.campaign_id
    GROUP BY cp.sku
),
sku_turnover AS (
    SELECT sku,
           COALESCE(SUM(revenue_rub), 0)::numeric AS revenue_rub,
           MAX(date) AS last_date
    FROM ozon_sales_daily
    WHERE seller_cabinet_id = sqlc.arg('seller_cabinet_id')
      AND date >= sqlc.arg('since')
    GROUP BY sku
)
SELECT cp.campaign_id,
       COALESCE(SUM(st.revenue_rub * cs.spend_rub / NULLIF(ss.spend_rub, 0)), 0)::numeric AS revenue_rub,
       COALESCE(BOOL_OR(ss.spend_rub > cs.spend_rub), FALSE)::boolean                     AS revenue_shared,
       MAX(st.last_date)::date                                                            AS last_date
FROM ozon_campaign_products cp
JOIN campaign_spend cs ON cs.campaign_id = cp.campaign_id
JOIN sku_spend ss ON ss.sku = cp.sku
JOIN sku_turnover st ON st.sku = cp.sku
GROUP BY cp.campaign_id;

-- GetOzonCabinetAdSpendWindowTotals mirrors the above for the spend side.
-- name: GetOzonCabinetAdSpendWindowTotals :one
SELECT COALESCE(SUM(s.spend_rub), 0)::numeric AS spend_rub,
       COUNT(*)::bigint                       AS days
FROM ozon_campaign_stats s
JOIN ozon_campaigns c ON c.id = s.campaign_id
WHERE c.seller_cabinet_id = sqlc.arg('seller_cabinet_id')
  AND s.date BETWEEN sqlc.arg('date_from') AND sqlc.arg('date_to');
