-- Ozon module phase 6: 7×24 orders heatmap + «Цена по спросу» strategy.
--
-- 1. Hourly order aggregation from Seller API postings (/v2/posting/fbo/list
--    + /v3/posting/fbs/list). Unlike the WB table (per-date rows), this is a
--    pre-aggregated 7×24 matrix over a rolling 28-day window that the postings
--    sync fully rewrites per cabinet — dow/hour are MSK (UTC+3), matching the
--    WB heatmap convention. dow: 0=Пн .. 6=Вс (ISO day-of-week − 1). sku is
--    the SALES sku (postings key space, same as ozon_sales_daily) — bridge to
--    the ozon_product_prices product_id via the ozon_products mapping.
CREATE TABLE ozon_orders_hourly (
    id                UUID     PRIMARY KEY DEFAULT gen_random_uuid(),
    seller_cabinet_id UUID     NOT NULL REFERENCES seller_cabinets(id) ON DELETE CASCADE,
    sku               BIGINT   NOT NULL,
    dow               SMALLINT NOT NULL CHECK (dow BETWEEN 0 AND 6),
    hour              SMALLINT NOT NULL CHECK (hour BETWEEN 0 AND 23),
    orders            INT      NOT NULL DEFAULT 0,
    quantity          INT      NOT NULL DEFAULT 0,
    UNIQUE (seller_cabinet_id, sku, dow, hour)
);

-- 2. strategies.type: recreate with the full 000054 list + the peak-hours
--    Ozon repricer strategy type.
ALTER TABLE strategies DROP CONSTRAINT IF EXISTS strategies_type_check;
ALTER TABLE strategies ADD CONSTRAINT strategies_type_check CHECK (type IN (
    'acos', 'roas', 'anti_sliv', 'dayparting', 'recommendation', 'search_playbook',
    'price_margin_floor', 'price_inventory_demand', 'price_ad_linked', 'price_peak_hours',
    'price_competitor_follow',
    'ozon_cpc_target_drr',
    'ozon_ai_autopilot',
    'ozon_price_margin_floor',
    'ozon_price_competitor_follow',
    'ozon_price_inventory_demand',
    'ozon_price_ad_linked',
    'ozon_price_peak_hours'
));
