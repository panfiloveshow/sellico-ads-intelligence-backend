CREATE TABLE IF NOT EXISTS product_sales_daily (
    id                UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id      UUID        NOT NULL REFERENCES workspaces(id),
    seller_cabinet_id UUID        NOT NULL REFERENCES seller_cabinets(id),
    product_id        UUID        NULL REFERENCES products(id) ON DELETE SET NULL,
    wb_product_id     BIGINT      NOT NULL,
    date              DATE        NOT NULL,
    orders            BIGINT      NOT NULL DEFAULT 0,
    canceled_orders   BIGINT      NOT NULL DEFAULT 0,
    sales             BIGINT      NOT NULL DEFAULT 0,
    returns           BIGINT      NOT NULL DEFAULT 0,
    ordered_revenue   BIGINT      NOT NULL DEFAULT 0,
    sold_revenue      BIGINT      NOT NULL DEFAULT 0,
    returned_revenue  BIGINT      NOT NULL DEFAULT 0,
    source            VARCHAR(32) NOT NULL DEFAULT 'wb_statistics',
    captured_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (seller_cabinet_id, wb_product_id, date)
);

CREATE INDEX IF NOT EXISTS idx_product_sales_daily_workspace_date
    ON product_sales_daily (workspace_id, date DESC);

CREATE INDEX IF NOT EXISTS idx_product_sales_daily_product_date
    ON product_sales_daily (product_id, date DESC);
