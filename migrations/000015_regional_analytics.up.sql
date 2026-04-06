-- 000015_regional_analytics.up.sql
-- Regional and logistics analytics.

-- 1. Regional position aggregates
CREATE TABLE regional_position_aggregates (
    id            UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id  UUID        NOT NULL REFERENCES workspaces(id),
    product_id    UUID        NOT NULL REFERENCES products(id),
    query         TEXT        NOT NULL,
    region        VARCHAR(50) NOT NULL,
    avg_position  FLOAT       NOT NULL,
    best_position INT,
    worst_position INT,
    check_count   INT         NOT NULL DEFAULT 1,
    period_start  DATE        NOT NULL,
    period_end    DATE        NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(workspace_id, product_id, query, region, period_start)
);

CREATE INDEX idx_regional_pos_agg ON regional_position_aggregates (workspace_id, product_id, region);

-- 2. Delivery info per product per region
CREATE TABLE delivery_data (
    id            UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id  UUID        NOT NULL REFERENCES workspaces(id),
    product_id    UUID        NOT NULL REFERENCES products(id),
    region        VARCHAR(100) NOT NULL,
    warehouse     VARCHAR(255),
    delivery_days INT,
    delivery_cost BIGINT      DEFAULT 0,
    in_stock      BOOLEAN     DEFAULT true,
    captured_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(workspace_id, product_id, region, captured_at)
);

CREATE INDEX idx_delivery_data_product ON delivery_data (product_id, region, captured_at DESC);

-- 3. Warehouse performance
CREATE TABLE warehouse_analytics (
    id             UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id   UUID        NOT NULL REFERENCES workspaces(id),
    warehouse_name VARCHAR(255) NOT NULL,
    region         VARCHAR(100) NOT NULL,
    products_count INT         DEFAULT 0,
    avg_delivery_days FLOAT,
    stock_coverage_pct FLOAT   DEFAULT 0,
    captured_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_warehouse_analytics ON warehouse_analytics (workspace_id, captured_at DESC);
