-- Hourly order aggregation for the repricer orders heatmap (day-of-week × hour).
-- Source: WB Statistics /api/v1/supplier/orders — the same response the daily
-- product_sales_daily sync consumes; date/hour are Europe/Moscow.
CREATE TABLE product_orders_hourly (
    id                UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id      UUID        NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    seller_cabinet_id UUID        NOT NULL REFERENCES seller_cabinets(id) ON DELETE CASCADE,
    wb_product_id     BIGINT      NOT NULL,
    date              DATE        NOT NULL,            -- order day (MSK)
    hour              SMALLINT    NOT NULL,             -- 0..23 (MSK)
    orders            INT         NOT NULL DEFAULT 0,
    units             INT         NOT NULL DEFAULT 0,
    revenue_kopecks   BIGINT      NOT NULL DEFAULT 0,   -- ordered revenue, kopecks
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (seller_cabinet_id, wb_product_id, date, hour)
);

CREATE INDEX idx_orders_hourly_cabinet_date ON product_orders_hourly (seller_cabinet_id, date);
