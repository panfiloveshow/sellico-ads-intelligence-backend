-- Sellico unit-economics mirror for Ozon cabinets (cost fallback for the
-- margin-floor repricer when Ozon does not report net_price).

-- name: UpsertOzonProductEconomics :exec
INSERT INTO ozon_product_economics (
    seller_cabinet_id, offer_id, sku, cost_price_rub, logistics_cost_rub,
    other_costs_rub, tax_percent, commission_percent, max_allowed_drr, source
)
VALUES (
    sqlc.arg('seller_cabinet_id'), sqlc.arg('offer_id'), sqlc.narg('sku'),
    sqlc.narg('cost_price_rub'), sqlc.narg('logistics_cost_rub'),
    sqlc.narg('other_costs_rub'), sqlc.narg('tax_percent'),
    sqlc.narg('commission_percent'), sqlc.narg('max_allowed_drr'),
    sqlc.arg('source')
)
ON CONFLICT (seller_cabinet_id, offer_id) DO UPDATE SET
    sku = EXCLUDED.sku,
    cost_price_rub = EXCLUDED.cost_price_rub,
    logistics_cost_rub = EXCLUDED.logistics_cost_rub,
    other_costs_rub = EXCLUDED.other_costs_rub,
    tax_percent = EXCLUDED.tax_percent,
    commission_percent = EXCLUDED.commission_percent,
    max_allowed_drr = EXCLUDED.max_allowed_drr,
    source = EXCLUDED.source,
    synced_at = now();

-- name: ListOzonProductEconomicsByCabinet :many
SELECT * FROM ozon_product_economics
WHERE seller_cabinet_id = $1
ORDER BY offer_id;

-- OzonCabinetMarginInputs feeds the derived «ДРР от общего оборота» ceiling:
-- every priced SKU of the cabinet with the turnover it produced over the
-- window. The margin itself is computed in Go so there is exactly one
-- implementation of that formula (ozonSKUMarginPct).
--
-- SKUs with no sales in the window come back with zero turnover and simply
-- carry no weight.
-- name: OzonCabinetMarginInputs :many
SELECT p.sku,
       p.price_rub,
       p.net_price_rub,
       p.commission_fbo_pct,
       p.commission_fbs_pct,
       p.acquiring_pct,
       COALESCE(s.revenue_rub, 0)::numeric AS revenue_rub
FROM ozon_product_prices p
LEFT JOIN (
    SELECT sd.sku, SUM(sd.revenue_rub) AS revenue_rub
    FROM ozon_sales_daily sd
    WHERE sd.seller_cabinet_id = sqlc.arg('seller_cabinet_id')
      AND sd.date >= sqlc.arg('since')
    GROUP BY sd.sku
) s ON s.sku = p.sku
WHERE p.seller_cabinet_id = sqlc.arg('seller_cabinet_id');
