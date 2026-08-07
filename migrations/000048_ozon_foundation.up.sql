-- Ozon module phase 1 (read-only foundation).
-- seller_cabinets learns which marketplace it belongs to; existing rows stay 'wb'
-- via the column default, so no backfill statement is needed.
ALTER TABLE seller_cabinets
    ADD COLUMN marketplace TEXT NOT NULL DEFAULT 'wb',
    ADD COLUMN encrypted_credentials TEXT NULL;

ALTER TABLE seller_cabinets
    ADD CONSTRAINT seller_cabinets_marketplace_check CHECK (marketplace IN ('wb', 'ozon'));

CREATE INDEX idx_seller_cabinets_marketplace
    ON seller_cabinets (marketplace)
    WHERE deleted_at IS NULL;

-- Ozon Performance campaigns (CPC / search promo / banners) mirrored locally.
-- Budgets arrive from the API in micro-rubles and are converted to integer
-- rubles at the client boundary (money is integer rubles across the schema).
CREATE TABLE ozon_campaigns (
    id                 UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    seller_cabinet_id  UUID        NOT NULL REFERENCES seller_cabinets(id) ON DELETE CASCADE,
    ozon_campaign_id   BIGINT      NOT NULL,
    title              TEXT,
    adv_object_type    TEXT,
    state              TEXT,
    placement          TEXT,
    autopilot_strategy TEXT,
    daily_budget_rub   BIGINT,
    weekly_budget_rub  BIGINT,
    from_date          DATE,
    to_date            DATE,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (seller_cabinet_id, ozon_campaign_id)
);

CREATE INDEX idx_ozon_campaigns_state ON ozon_campaigns (seller_cabinet_id, state);

-- Products (SKUs) inside a campaign, with their bids.
CREATE TABLE ozon_campaign_products (
    id           UUID           PRIMARY KEY DEFAULT gen_random_uuid(),
    campaign_id  UUID           NOT NULL REFERENCES ozon_campaigns(id) ON DELETE CASCADE,
    sku          BIGINT         NOT NULL,
    bid_rub      NUMERIC(12,2),
    target_cir   NUMERIC(6,2),
    top_position INTEGER,
    is_active    BOOLEAN        NOT NULL DEFAULT TRUE,
    updated_at   TIMESTAMPTZ    NOT NULL DEFAULT now(),
    UNIQUE (campaign_id, sku)
);

-- Daily campaign statistics from GET /api/client/statistics/daily.
CREATE TABLE ozon_campaign_stats (
    id          UUID           PRIMARY KEY DEFAULT gen_random_uuid(),
    campaign_id UUID           NOT NULL REFERENCES ozon_campaigns(id) ON DELETE CASCADE,
    date        DATE           NOT NULL,
    views       BIGINT         NOT NULL DEFAULT 0,
    clicks      BIGINT         NOT NULL DEFAULT 0,
    spend_rub   NUMERIC(14,2)  NOT NULL DEFAULT 0,
    orders      BIGINT         NOT NULL DEFAULT 0,
    revenue_rub NUMERIC(14,2)  NOT NULL DEFAULT 0,
    UNIQUE (campaign_id, date)
);

-- Prices + built-in unit economics from POST /v5/product/info/prices.
CREATE TABLE ozon_product_prices (
    id                         UUID           PRIMARY KEY DEFAULT gen_random_uuid(),
    seller_cabinet_id          UUID           NOT NULL REFERENCES seller_cabinets(id) ON DELETE CASCADE,
    sku                        BIGINT         NOT NULL,
    offer_id                   TEXT,
    name                       TEXT,
    price_rub                  NUMERIC(12,2),
    old_price_rub              NUMERIC(12,2),
    min_price_rub              NUMERIC(12,2),
    net_price_rub              NUMERIC(12,2),
    marketing_seller_price_rub NUMERIC(12,2),
    color_index                TEXT,
    commission_fbo_pct         NUMERIC(6,2),
    commission_fbs_pct         NUMERIC(6,2),
    acquiring_pct              NUMERIC(6,2),
    synced_at                  TIMESTAMPTZ,
    UNIQUE (seller_cabinet_id, sku)
);

-- Per-cabinet sync bookkeeping for the ozon worker.
CREATE TABLE ozon_sync_states (
    seller_cabinet_id   UUID        PRIMARY KEY REFERENCES seller_cabinets(id) ON DELETE CASCADE,
    campaigns_synced_at TIMESTAMPTZ,
    stats_synced_at     TIMESTAMPTZ,
    prices_synced_at    TIMESTAMPTZ,
    last_error          TEXT,
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);
