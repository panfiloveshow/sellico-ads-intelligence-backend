CREATE TABLE IF NOT EXISTS wb_bid_actions (
    id                UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id      UUID        NOT NULL REFERENCES workspaces(id),
    seller_cabinet_id UUID        NOT NULL REFERENCES seller_cabinets(id),
    campaign_id       UUID        NULL REFERENCES campaigns(id) ON DELETE SET NULL,
    product_id        UUID        NULL REFERENCES products(id) ON DELETE SET NULL,
    wb_campaign_id    BIGINT      NOT NULL DEFAULT 0,
    wb_product_id     BIGINT      NOT NULL DEFAULT 0,
    norm_query        TEXT        NULL,
    action_type       VARCHAR(32) NOT NULL,
    old_bid           BIGINT      NULL,
    new_bid           BIGINT      NULL,
    reason            TEXT        NULL,
    status            VARCHAR(32) NOT NULL DEFAULT 'pending',
    wb_response       JSONB       NULL,
    created_by        UUID        NULL,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_wb_bid_actions_workspace_created
    ON wb_bid_actions (workspace_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_wb_bid_actions_campaign_created
    ON wb_bid_actions (campaign_id, created_at DESC);

CREATE TABLE IF NOT EXISTS wb_normquery_clusters (
    id                UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id      UUID        NOT NULL REFERENCES workspaces(id),
    seller_cabinet_id UUID        NOT NULL REFERENCES seller_cabinets(id),
    campaign_id       UUID        NULL REFERENCES campaigns(id) ON DELETE CASCADE,
    product_id        UUID        NULL REFERENCES products(id) ON DELETE SET NULL,
    wb_campaign_id    BIGINT      NOT NULL,
    wb_product_id     BIGINT      NOT NULL DEFAULT 0,
    norm_query        TEXT        NOT NULL,
    state             VARCHAR(24) NOT NULL DEFAULT 'active',
    current_bid       BIGINT      NULL,
    synced_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (seller_cabinet_id, wb_campaign_id, wb_product_id, norm_query)
);

CREATE INDEX IF NOT EXISTS idx_wb_normquery_clusters_campaign
    ON wb_normquery_clusters (campaign_id, state, norm_query);

CREATE TABLE IF NOT EXISTS seller_ad_balances (
    id                UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    seller_cabinet_id UUID        NOT NULL REFERENCES seller_cabinets(id) ON DELETE CASCADE,
    balance           BIGINT      NOT NULL DEFAULT 0,
    net               BIGINT      NOT NULL DEFAULT 0,
    bonus             BIGINT      NOT NULL DEFAULT 0,
    captured_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (seller_cabinet_id, captured_at)
);

CREATE INDEX IF NOT EXISTS idx_seller_ad_balances_latest
    ON seller_ad_balances (seller_cabinet_id, captured_at DESC);

CREATE TABLE IF NOT EXISTS wb_ad_finance_documents (
    id                UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    seller_cabinet_id UUID        NOT NULL REFERENCES seller_cabinets(id) ON DELETE CASCADE,
    external_id       TEXT        NOT NULL,
    document_type     VARCHAR(24) NOT NULL,
    wb_campaign_id    BIGINT      NOT NULL DEFAULT 0,
    amount            BIGINT      NOT NULL DEFAULT 0,
    document_date     TIMESTAMPTZ NULL,
    raw               JSONB       NULL,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (seller_cabinet_id, document_type, external_id)
);

CREATE TABLE IF NOT EXISTS campaign_daily_limits (
    id                  UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id         UUID        NOT NULL REFERENCES workspaces(id),
    seller_cabinet_id    UUID        NOT NULL REFERENCES seller_cabinets(id),
    campaign_id          UUID        NOT NULL REFERENCES campaigns(id) ON DELETE CASCADE,
    daily_limit          BIGINT      NOT NULL,
    enabled              BOOLEAN     NOT NULL DEFAULT true,
    pause_when_reached   BOOLEAN     NOT NULL DEFAULT true,
    resume_next_day      BOOLEAN     NOT NULL DEFAULT true,
    last_checked_at      TIMESTAMPTZ NULL,
    last_action_at       TIMESTAMPTZ NULL,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (campaign_id)
);

CREATE TABLE IF NOT EXISTS product_sales_funnel_periods (
    id                UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id      UUID        NOT NULL REFERENCES workspaces(id),
    seller_cabinet_id UUID        NOT NULL REFERENCES seller_cabinets(id),
    product_id        UUID        NULL REFERENCES products(id) ON DELETE SET NULL,
    wb_product_id     BIGINT      NOT NULL,
    date_from         DATE        NOT NULL,
    date_to           DATE        NOT NULL,
    cart_count        BIGINT      NOT NULL DEFAULT 0,
    source            VARCHAR(64) NOT NULL DEFAULT 'wb_sales_funnel_products_v3',
    captured_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (seller_cabinet_id, wb_product_id, date_from, date_to)
);

CREATE INDEX IF NOT EXISTS idx_product_sales_funnel_periods_workspace
    ON product_sales_funnel_periods (workspace_id, date_to DESC);
