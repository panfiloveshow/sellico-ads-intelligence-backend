-- Ozon module phase 4 (repricer).

-- 1. Audit trail for every Ozon price write (strategy, manual, AI), mirroring
--    the WB price_changes shape. status='shadow' rows are dry-run decisions
--    that never reached Ozon.
CREATE TABLE ozon_price_changes (
    id                UUID           PRIMARY KEY DEFAULT gen_random_uuid(),
    seller_cabinet_id UUID           NOT NULL REFERENCES seller_cabinets(id) ON DELETE CASCADE,
    sku               BIGINT         NOT NULL,
    offer_id          TEXT,
    old_price_rub     NUMERIC(12,2),
    new_price_rub     NUMERIC(12,2)  NOT NULL,
    old_old_price_rub NUMERIC(12,2),
    new_old_price_rub NUMERIC(12,2),
    min_price_rub     NUMERIC(12,2),
    floor_rub         NUMERIC(12,2),
    reason            TEXT,
    source            TEXT           NOT NULL CHECK (source IN ('strategy', 'manual', 'ai')),
    strategy_id       UUID           REFERENCES strategies(id) ON DELETE SET NULL,
    status            TEXT           NOT NULL DEFAULT 'pending'
                      CHECK (status IN ('pending', 'applied', 'failed', 'rolled_back', 'shadow')),
    decision_context  JSONB,
    error             TEXT,
    created_at        TIMESTAMPTZ    DEFAULT now(),
    applied_at        TIMESTAMPTZ
);

CREATE INDEX idx_ozon_price_changes_cabinet
    ON ozon_price_changes (seller_cabinet_id, created_at DESC);
CREATE INDEX idx_ozon_price_changes_sku
    ON ozon_price_changes (sku);

-- 2. Competitor minimum prices from /v5/product/info/prices price_indexes:
--    Ozon (other sellers on Ozon), external marketplaces, and the seller's own
--    other marketplaces. Feed the ozon_price_competitor_follow strategy.
ALTER TABLE ozon_product_prices
    ADD COLUMN ozon_index_min_price_rub     NUMERIC(12,2),
    ADD COLUMN external_index_min_price_rub NUMERIC(12,2),
    ADD COLUMN self_index_min_price_rub     NUMERIC(12,2);

-- 3. strategies.type: recreate with the full 000050 list + the two Ozon
--    repricer strategy types.
ALTER TABLE strategies DROP CONSTRAINT IF EXISTS strategies_type_check;
ALTER TABLE strategies ADD CONSTRAINT strategies_type_check CHECK (type IN (
    'acos', 'roas', 'anti_sliv', 'dayparting', 'recommendation', 'search_playbook',
    'price_margin_floor', 'price_inventory_demand', 'price_ad_linked', 'price_peak_hours',
    'price_competitor_follow',
    'ozon_cpc_target_drr',
    'ozon_ai_autopilot',
    'ozon_price_margin_floor',
    'ozon_price_competitor_follow'
));
