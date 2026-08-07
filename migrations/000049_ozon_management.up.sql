-- Ozon module phase 2 (management + deterministic strategies).

-- 1. Audit trail for every Ozon bid write (manual and strategy-driven).
--    CPC rows point at a mirrored campaign; CPO (search promo) rows have no
--    campaign on Ozon's side, so they carry seller_cabinet_id + kind='cpo'
--    instead — one table audits both flows.
CREATE TABLE ozon_bid_changes (
    id                UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    campaign_id       UUID        REFERENCES ozon_campaigns(id) ON DELETE CASCADE,
    seller_cabinet_id UUID        REFERENCES seller_cabinets(id) ON DELETE CASCADE,
    kind              TEXT        NOT NULL DEFAULT 'cpc' CHECK (kind IN ('cpc', 'cpo')),
    sku               BIGINT,
    old_bid_rub       NUMERIC(12,2),
    new_bid_rub       NUMERIC(12,2),
    reason            TEXT,
    source            TEXT        NOT NULL CHECK (source IN ('strategy', 'manual')),
    status            TEXT        NOT NULL DEFAULT 'pending'
                      CHECK (status IN ('pending', 'applied', 'failed', 'rolled_back', 'shadow')),
    decision_context  JSONB,
    error             TEXT,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    applied_at        TIMESTAMPTZ,
    CONSTRAINT chk_ozon_bid_change_scope CHECK (campaign_id IS NOT NULL OR seller_cabinet_id IS NOT NULL)
);

CREATE INDEX idx_ozon_bid_changes_campaign
    ON ozon_bid_changes (campaign_id, created_at DESC);
CREATE INDEX idx_ozon_bid_changes_cabinet
    ON ozon_bid_changes (seller_cabinet_id, created_at DESC)
    WHERE seller_cabinet_id IS NOT NULL;

-- 2. CPO (ex-"Продвижение в поиске") per-SKU state, mirrored from
--    search_promo/v2/products. bid_kind: Ozon is migrating percent bids to
--    fixed ruble bids; both are mirrored as reported.
CREATE TABLE ozon_cpo_products (
    id                UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    seller_cabinet_id UUID        NOT NULL REFERENCES seller_cabinets(id) ON DELETE CASCADE,
    sku               BIGINT      NOT NULL,
    enabled           BOOLEAN     NOT NULL DEFAULT FALSE,
    bid               NUMERIC(12,2),
    bid_kind          TEXT        CHECK (bid_kind IN ('percent', 'fixed_rub')),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (seller_cabinet_id, sku)
);

-- 3. Ozon strategies bind to ozon_campaigns. strategy_bindings.campaign_id
--    carries an FK to the WB campaigns table (000011), so Ozon campaigns get
--    their own nullable FK column instead of overloading that one.
ALTER TABLE strategy_bindings
    ADD COLUMN ozon_campaign_id UUID REFERENCES ozon_campaigns(id) ON DELETE CASCADE;

ALTER TABLE strategy_bindings DROP CONSTRAINT IF EXISTS chk_binding_target;
ALTER TABLE strategy_bindings ADD CONSTRAINT chk_binding_target
    CHECK (campaign_id IS NOT NULL OR product_id IS NOT NULL OR ozon_campaign_id IS NOT NULL);

CREATE INDEX idx_strategy_bindings_ozon_campaign
    ON strategy_bindings (ozon_campaign_id)
    WHERE ozon_campaign_id IS NOT NULL;

-- 4. strategies.type: recreate with the FULL list the application writes.
--    000034 defined this last and missed price_competitor_follow (pre-existing
--    bug — creating that strategy type failed at the DB); ozon_cpc_target_drr
--    is new in this phase.
ALTER TABLE strategies DROP CONSTRAINT IF EXISTS strategies_type_check;
ALTER TABLE strategies ADD CONSTRAINT strategies_type_check CHECK (type IN (
    'acos', 'roas', 'anti_sliv', 'dayparting', 'recommendation', 'search_playbook',
    'price_margin_floor', 'price_inventory_demand', 'price_ad_linked', 'price_peak_hours',
    'price_competitor_follow',
    'ozon_cpc_target_drr'
));
