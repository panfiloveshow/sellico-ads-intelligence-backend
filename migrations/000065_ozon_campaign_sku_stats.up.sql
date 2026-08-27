-- Per-campaign per-SKU daily statistics from the Performance API campaign
-- report (POST /api/client/statistics, async UUID flow). This is the only
-- source that attributes orders/revenue to a SPECIFIC campaign's SKU —
-- ozon_sales_daily holds the product's TOTAL sales across all traffic.
-- sku here is the ADVERTISING sku (the one campaigns operate with).
CREATE TABLE ozon_campaign_sku_stats (
    id                UUID          PRIMARY KEY DEFAULT gen_random_uuid(),
    seller_cabinet_id UUID          NOT NULL REFERENCES seller_cabinets(id) ON DELETE CASCADE,
    ozon_campaign_id  BIGINT        NOT NULL,
    sku               BIGINT        NOT NULL,
    date              DATE          NOT NULL,
    views             BIGINT        NOT NULL DEFAULT 0,
    clicks            BIGINT        NOT NULL DEFAULT 0,
    spend_rub         NUMERIC(14,2) NOT NULL DEFAULT 0,
    orders            BIGINT        NOT NULL DEFAULT 0,
    revenue_rub       NUMERIC(14,2) NOT NULL DEFAULT 0,
    UNIQUE (seller_cabinet_id, ozon_campaign_id, sku, date)
);

CREATE INDEX idx_ozon_campaign_sku_stats_campaign_date
    ON ozon_campaign_sku_stats (seller_cabinet_id, ozon_campaign_id, date DESC);
