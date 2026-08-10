ALTER TABLE ozon_cpo_products
    DROP COLUMN IF EXISTS prev_bid_pct,
    DROP COLUMN IF EXISTS views_this_week,
    DROP COLUMN IF EXISTS views_prev_week;

DROP TABLE IF EXISTS ozon_cpo_orders;
