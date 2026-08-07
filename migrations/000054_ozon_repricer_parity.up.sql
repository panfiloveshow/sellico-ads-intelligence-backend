-- Ozon module phase 5 (repricer parity with WB): price calendar with
-- auto-revert, stock mirror + daily sales for the inventory-demand strategy,
-- and the two new strategy types (inventory demand, ad-linked).

-- 1. Price calendar. Per-SKU absolute target prices (Ozon prices are rubles
--    with kopecks, no WB-style discount split): apply scheduled_price_rub at
--    starts_at; when ends_at passes, restore revert_price_rub. sku is the
--    Seller API product_id — the same key space as ozon_product_prices.
CREATE TABLE ozon_price_schedule_entries (
    id                  UUID          PRIMARY KEY DEFAULT gen_random_uuid(),
    seller_cabinet_id   UUID          NOT NULL REFERENCES seller_cabinets(id) ON DELETE CASCADE,
    sku                 BIGINT        NOT NULL,
    offer_id            TEXT,
    scheduled_price_rub NUMERIC(12,2) NOT NULL,
    revert_price_rub    NUMERIC(12,2),
    starts_at           TIMESTAMPTZ   NOT NULL,
    ends_at             TIMESTAMPTZ,
    status              TEXT          NOT NULL DEFAULT 'pending'
                        CHECK (status IN ('pending', 'applied', 'reverted', 'cancelled', 'failed')),
    error               TEXT,
    created_at          TIMESTAMPTZ   NOT NULL DEFAULT now(),
    applied_at          TIMESTAMPTZ,
    reverted_at         TIMESTAMPTZ
);

CREATE INDEX idx_ozon_price_schedules_cabinet
    ON ozon_price_schedule_entries (seller_cabinet_id, starts_at DESC);
CREATE INDEX idx_ozon_price_schedules_pending
    ON ozon_price_schedule_entries (starts_at)
    WHERE status = 'pending';
CREATE INDEX idx_ozon_price_schedules_reverting
    ON ozon_price_schedule_entries (ends_at)
    WHERE status = 'applied' AND ends_at IS NOT NULL;

-- 2. Stock mirror from POST /v4/product/info/stocks. present/reserved are
--    summed over the fulfillment schemes (FBO+FBS). sku is the Seller API
--    product_id (ozon_product_prices key space).
CREATE TABLE ozon_product_stocks (
    id                UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    seller_cabinet_id UUID        NOT NULL REFERENCES seller_cabinets(id) ON DELETE CASCADE,
    sku               BIGINT      NOT NULL,
    offer_id          TEXT,
    present           INTEGER     NOT NULL DEFAULT 0,
    reserved          INTEGER     NOT NULL DEFAULT 0,
    synced_at         TIMESTAMPTZ,
    UNIQUE (seller_cabinet_id, sku)
);

-- 3. Daily sales from POST /v1/analytics/data (dimension sku+day). sku here
--    is the SALES sku (analytics key space) — bridge to product_id via the
--    ozon_products mapping.
CREATE TABLE ozon_sales_daily (
    id                UUID           PRIMARY KEY DEFAULT gen_random_uuid(),
    seller_cabinet_id UUID           NOT NULL REFERENCES seller_cabinets(id) ON DELETE CASCADE,
    sku               BIGINT         NOT NULL,
    date              DATE           NOT NULL,
    ordered_units     INTEGER        NOT NULL DEFAULT 0,
    revenue_rub       NUMERIC(14,2)  DEFAULT 0,
    UNIQUE (seller_cabinet_id, sku, date)
);

CREATE INDEX idx_ozon_sales_daily_cabinet_sku
    ON ozon_sales_daily (seller_cabinet_id, sku, date DESC);

-- seller_cabinets.repricer_paused_until (000036) is marketplace-agnostic and
-- is reused as the Ozon repricer freeze switch — no new column needed.

-- 4. strategies.type: recreate with the full 000051 list + the two new Ozon
--    repricer strategy types.
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
    'ozon_price_ad_linked'
));
