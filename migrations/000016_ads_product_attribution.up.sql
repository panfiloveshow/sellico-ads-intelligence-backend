CREATE TABLE campaign_products (
    campaign_id       UUID        NOT NULL REFERENCES campaigns(id) ON DELETE CASCADE,
    product_id        UUID        NOT NULL REFERENCES products(id) ON DELETE CASCADE,
    workspace_id      UUID        NOT NULL REFERENCES workspaces(id),
    seller_cabinet_id UUID        NOT NULL REFERENCES seller_cabinets(id),
    wb_campaign_id    BIGINT      NOT NULL,
    wb_product_id     BIGINT      NOT NULL,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (campaign_id, product_id)
);

CREATE INDEX idx_campaign_products_workspace_id ON campaign_products (workspace_id);
CREATE INDEX idx_campaign_products_product_id ON campaign_products (product_id);
CREATE INDEX idx_campaign_products_wb_campaign_id ON campaign_products (seller_cabinet_id, wb_campaign_id);
CREATE INDEX idx_campaign_products_wb_product_id ON campaign_products (seller_cabinet_id, wb_product_id);

CREATE TABLE product_stats (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    product_id  UUID        NOT NULL REFERENCES products(id) ON DELETE CASCADE,
    campaign_id UUID        NOT NULL REFERENCES campaigns(id) ON DELETE CASCADE,
    date        DATE        NOT NULL,
    impressions BIGINT      NOT NULL DEFAULT 0,
    clicks      BIGINT      NOT NULL DEFAULT 0,
    spend       BIGINT      NOT NULL DEFAULT 0,
    orders      BIGINT      NULL,
    revenue     BIGINT      NULL,
    atbs        BIGINT      NULL,
    canceled    BIGINT      NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX idx_product_stats_product_campaign_date ON product_stats (product_id, campaign_id, date);
CREATE INDEX idx_product_stats_date ON product_stats (date);
CREATE INDEX idx_product_stats_campaign_id ON product_stats (campaign_id);
