-- CPO («Оплата за заказ») promoted orders mirrored from the async
-- all_sku_promo orders report (migration 000059).

-- name: UpsertOzonCpoOrder :exec
INSERT INTO ozon_cpo_orders (
    seller_cabinet_id, date, order_id, order_number, sku, adv_sku,
    vendor_code, name, quantity, price_rub, sale_price_rub,
    bid_pct, bid_rub, spend_rub
)
VALUES (
    sqlc.arg('seller_cabinet_id'), sqlc.arg('date'), sqlc.arg('order_id'),
    sqlc.narg('order_number'), sqlc.narg('sku'), sqlc.narg('adv_sku'),
    sqlc.narg('vendor_code'), sqlc.narg('name'), sqlc.narg('quantity'),
    sqlc.narg('price_rub'), sqlc.narg('sale_price_rub'),
    sqlc.narg('bid_pct'), sqlc.narg('bid_rub'), sqlc.narg('spend_rub')
)
ON CONFLICT (seller_cabinet_id, order_id, sku) DO UPDATE SET
    date = EXCLUDED.date,
    order_number = COALESCE(EXCLUDED.order_number, ozon_cpo_orders.order_number),
    adv_sku = COALESCE(EXCLUDED.adv_sku, ozon_cpo_orders.adv_sku),
    vendor_code = COALESCE(EXCLUDED.vendor_code, ozon_cpo_orders.vendor_code),
    name = COALESCE(EXCLUDED.name, ozon_cpo_orders.name),
    quantity = COALESCE(EXCLUDED.quantity, ozon_cpo_orders.quantity),
    price_rub = COALESCE(EXCLUDED.price_rub, ozon_cpo_orders.price_rub),
    sale_price_rub = COALESCE(EXCLUDED.sale_price_rub, ozon_cpo_orders.sale_price_rub),
    bid_pct = COALESCE(EXCLUDED.bid_pct, ozon_cpo_orders.bid_pct),
    bid_rub = COALESCE(EXCLUDED.bid_rub, ozon_cpo_orders.bid_rub),
    spend_rub = COALESCE(EXCLUDED.spend_rub, ozon_cpo_orders.spend_rub),
    updated_at = now();

-- name: ListOzonCpoOrders :many
SELECT * FROM ozon_cpo_orders
WHERE seller_cabinet_id = $1 AND date >= $2
ORDER BY date DESC, order_id DESC, sku
LIMIT $3 OFFSET $4;

-- name: CountOzonCpoOrders :one
SELECT COUNT(*) FROM ozon_cpo_orders
WHERE seller_cabinet_id = $1 AND date >= $2;

-- name: AggregateOzonCpoOrders :one
-- Window aggregate for the CPO overview, sourced from the promoted-orders
-- report (actual orders — unlike ozon_campaign_stats, which carries the
-- campaign statistics counters).
SELECT
    COUNT(DISTINCT o.order_id)::bigint AS orders_count,
    COALESCE(SUM(o.quantity), 0)::bigint AS sold_units,
    COALESCE(SUM(o.sale_price_rub * o.quantity), 0)::numeric AS revenue_rub,
    COALESCE(SUM(o.spend_rub), 0)::numeric AS spend_rub,
    AVG(o.bid_pct)::numeric AS avg_bid_pct
FROM ozon_cpo_orders o
WHERE o.seller_cabinet_id = $1 AND o.date >= $2;
