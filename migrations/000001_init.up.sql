-- 000001_init.up.sql
-- Sellico Ads Intelligence Backend — initial schema

-- 1. users
CREATE TABLE users (
    id            UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    email         VARCHAR(255) UNIQUE NOT NULL,
    password_hash TEXT        NOT NULL,
    name          VARCHAR(255) NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX idx_users_email ON users (email);

-- 2. refresh_tokens
CREATE TABLE refresh_tokens (
    id         UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id    UUID        NOT NULL REFERENCES users(id),
    token_hash TEXT        NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    revoked    BOOLEAN     NOT NULL DEFAULT false,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_refresh_tokens_user_id ON refresh_tokens (user_id);
CREATE UNIQUE INDEX idx_refresh_tokens_token_hash ON refresh_tokens (token_hash);

-- 3. workspaces
CREATE TABLE workspaces (
    id         UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    name       VARCHAR(255) NOT NULL,
    slug       VARCHAR(100) UNIQUE NOT NULL,
    created_at TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ  NOT NULL DEFAULT now(),
    deleted_at TIMESTAMPTZ  NULL
);

CREATE UNIQUE INDEX idx_workspaces_slug ON workspaces (slug) WHERE deleted_at IS NULL;

-- 4. workspace_members
CREATE TABLE workspace_members (
    id           UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID        NOT NULL REFERENCES workspaces(id),
    user_id      UUID        NOT NULL REFERENCES users(id),
    role         VARCHAR(20) NOT NULL CHECK (role IN ('owner','manager','analyst','viewer')),
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX idx_workspace_members_workspace_user ON workspace_members (workspace_id, user_id);
CREATE INDEX idx_workspace_members_user_id ON workspace_members (user_id);

-- 5. seller_cabinets
CREATE TABLE seller_cabinets (
    id              UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id    UUID         NOT NULL REFERENCES workspaces(id),
    name            VARCHAR(255) NOT NULL,
    encrypted_token TEXT         NOT NULL,
    status          VARCHAR(20)  NOT NULL DEFAULT 'active',
    last_synced_at  TIMESTAMPTZ  NULL,
    created_at      TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ  NOT NULL DEFAULT now(),
    deleted_at      TIMESTAMPTZ  NULL
);

CREATE INDEX idx_seller_cabinets_workspace_id ON seller_cabinets (workspace_id) WHERE deleted_at IS NULL;

-- 6. campaigns
CREATE TABLE campaigns (
    id                UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id      UUID         NOT NULL REFERENCES workspaces(id),
    seller_cabinet_id UUID         NOT NULL REFERENCES seller_cabinets(id),
    wb_campaign_id    BIGINT       NOT NULL,
    name              VARCHAR(500) NOT NULL,
    status            VARCHAR(50)  NOT NULL,
    campaign_type     INT          NOT NULL DEFAULT 9,
    bid_type          VARCHAR(20)  NOT NULL,
    payment_type      VARCHAR(10)  NOT NULL,
    daily_budget      BIGINT       NULL,
    created_at        TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ  NOT NULL DEFAULT now()
);

CREATE INDEX idx_campaigns_workspace_id ON campaigns (workspace_id);
CREATE INDEX idx_campaigns_seller_cabinet_id ON campaigns (seller_cabinet_id);
CREATE UNIQUE INDEX idx_campaigns_wb_campaign_id_seller ON campaigns (wb_campaign_id, seller_cabinet_id);

-- 7. campaign_stats
CREATE TABLE campaign_stats (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    campaign_id UUID        NOT NULL REFERENCES campaigns(id),
    date        DATE        NOT NULL,
    impressions BIGINT      NOT NULL DEFAULT 0,
    clicks      BIGINT      NOT NULL DEFAULT 0,
    spend       BIGINT      NOT NULL DEFAULT 0,
    orders      BIGINT      NULL,
    revenue     BIGINT      NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX idx_campaign_stats_campaign_date ON campaign_stats (campaign_id, date);
CREATE INDEX idx_campaign_stats_date ON campaign_stats (date);

-- 8. phrases
CREATE TABLE phrases (
    id            UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    campaign_id   UUID        NOT NULL REFERENCES campaigns(id),
    workspace_id  UUID        NOT NULL REFERENCES workspaces(id),
    wb_cluster_id BIGINT      NOT NULL,
    keyword       TEXT        NOT NULL,
    count         INT         NULL,
    current_bid   BIGINT      NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_phrases_campaign_id ON phrases (campaign_id);
CREATE INDEX idx_phrases_workspace_id ON phrases (workspace_id);
CREATE UNIQUE INDEX idx_phrases_wb_cluster_campaign ON phrases (wb_cluster_id, campaign_id);

-- 9. phrase_stats
CREATE TABLE phrase_stats (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    phrase_id   UUID        NOT NULL REFERENCES phrases(id),
    date        DATE        NOT NULL,
    impressions BIGINT      NOT NULL DEFAULT 0,
    clicks      BIGINT      NOT NULL DEFAULT 0,
    spend       BIGINT      NOT NULL DEFAULT 0,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX idx_phrase_stats_phrase_date ON phrase_stats (phrase_id, date);
CREATE INDEX idx_phrase_stats_date ON phrase_stats (date);

-- 10. products
CREATE TABLE products (
    id                UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id      UUID         NOT NULL REFERENCES workspaces(id),
    seller_cabinet_id UUID         NOT NULL REFERENCES seller_cabinets(id),
    wb_product_id     BIGINT       NOT NULL,
    title             VARCHAR(500) NOT NULL,
    brand             VARCHAR(255) NULL,
    category          VARCHAR(255) NULL,
    image_url         TEXT         NULL,
    price             BIGINT       NULL,
    created_at        TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ  NOT NULL DEFAULT now()
);

CREATE INDEX idx_products_workspace_id ON products (workspace_id);
CREATE UNIQUE INDEX idx_products_wb_product_seller ON products (wb_product_id, seller_cabinet_id);

-- 11. positions
CREATE TABLE positions (
    id           UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID        NOT NULL REFERENCES workspaces(id),
    product_id   UUID        NOT NULL REFERENCES products(id),
    query        TEXT        NOT NULL,
    region       VARCHAR(50) NOT NULL,
    position     INT         NOT NULL,
    page         INT         NOT NULL,
    source       VARCHAR(20) NOT NULL DEFAULT 'parser',
    checked_at   TIMESTAMPTZ NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_positions_workspace_id ON positions (workspace_id);
CREATE INDEX idx_positions_product_query_region ON positions (product_id, query, region);
CREATE INDEX idx_positions_checked_at ON positions (checked_at);

-- 12. serp_snapshots
CREATE TABLE serp_snapshots (
    id            UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id  UUID        NOT NULL REFERENCES workspaces(id),
    query         TEXT        NOT NULL,
    region        VARCHAR(50) NOT NULL,
    total_results INT         NOT NULL,
    scanned_at    TIMESTAMPTZ NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_serp_snapshots_workspace_id ON serp_snapshots (workspace_id);
CREATE INDEX idx_serp_snapshots_query_region ON serp_snapshots (query, region);
CREATE INDEX idx_serp_snapshots_scanned_at ON serp_snapshots (scanned_at);

-- 13. serp_result_items
CREATE TABLE serp_result_items (
    id            UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    snapshot_id   UUID         NOT NULL REFERENCES serp_snapshots(id),
    position      INT          NOT NULL,
    wb_product_id BIGINT       NOT NULL,
    title         VARCHAR(500) NOT NULL,
    price         BIGINT       NULL,
    rating        NUMERIC(3,2) NULL,
    reviews_count INT          NULL,
    created_at    TIMESTAMPTZ  NOT NULL DEFAULT now()
);

CREATE INDEX idx_serp_result_items_snapshot_id ON serp_result_items (snapshot_id);
CREATE INDEX idx_serp_result_items_wb_product_id ON serp_result_items (wb_product_id);

-- 14. bid_snapshots
CREATE TABLE bid_snapshots (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    phrase_id       UUID        NOT NULL REFERENCES phrases(id),
    workspace_id    UUID        NOT NULL REFERENCES workspaces(id),
    competitive_bid BIGINT      NOT NULL,
    leadership_bid  BIGINT      NOT NULL,
    cpm_min         BIGINT      NOT NULL,
    captured_at     TIMESTAMPTZ NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_bid_snapshots_phrase_id ON bid_snapshots (phrase_id);
CREATE INDEX idx_bid_snapshots_workspace_id ON bid_snapshots (workspace_id);
CREATE INDEX idx_bid_snapshots_captured_at ON bid_snapshots (captured_at);

-- 15. recommendations
CREATE TABLE recommendations (
    id             UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id   UUID         NOT NULL REFERENCES workspaces(id),
    campaign_id    UUID         NULL REFERENCES campaigns(id),
    phrase_id      UUID         NULL REFERENCES phrases(id),
    product_id     UUID         NULL REFERENCES products(id),
    title          VARCHAR(500) NOT NULL,
    description    TEXT         NOT NULL,
    type           VARCHAR(50)  NOT NULL,
    severity       VARCHAR(20)  NOT NULL,
    confidence     NUMERIC(3,2) NOT NULL,
    source_metrics JSONB        NOT NULL,
    next_action    TEXT         NULL,
    status         VARCHAR(20)  NOT NULL DEFAULT 'active',
    created_at     TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ  NOT NULL DEFAULT now()
);

CREATE INDEX idx_recommendations_workspace_id ON recommendations (workspace_id);
CREATE INDEX idx_recommendations_type_status ON recommendations (type, status);
CREATE INDEX idx_recommendations_campaign_id ON recommendations (campaign_id);
CREATE INDEX idx_recommendations_phrase_id ON recommendations (phrase_id);

-- 16. exports
CREATE TABLE exports (
    id            UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id  UUID        NOT NULL REFERENCES workspaces(id),
    user_id       UUID        NOT NULL REFERENCES users(id),
    entity_type   VARCHAR(50) NOT NULL,
    format        VARCHAR(10) NOT NULL,
    status        VARCHAR(20) NOT NULL DEFAULT 'pending',
    file_path     TEXT        NULL,
    error_message TEXT        NULL,
    filters       JSONB       NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_exports_workspace_id ON exports (workspace_id);
CREATE INDEX idx_exports_status ON exports (status);

-- 17. extension_sessions
CREATE TABLE extension_sessions (
    id                UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id           UUID        NOT NULL REFERENCES users(id),
    workspace_id      UUID        NOT NULL REFERENCES workspaces(id),
    extension_version VARCHAR(20) NOT NULL,
    last_active_at    TIMESTAMPTZ NOT NULL,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_extension_sessions_user_workspace ON extension_sessions (user_id, workspace_id);

-- 18. audit_logs
CREATE TABLE audit_logs (
    id           UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID         NOT NULL REFERENCES workspaces(id),
    user_id      UUID         NULL,
    action       VARCHAR(100) NOT NULL,
    entity_type  VARCHAR(50)  NOT NULL,
    entity_id    UUID         NULL,
    metadata     JSONB        NULL,
    created_at   TIMESTAMPTZ  NOT NULL DEFAULT now()
);

CREATE INDEX idx_audit_logs_workspace_id ON audit_logs (workspace_id);
CREATE INDEX idx_audit_logs_entity_type_id ON audit_logs (entity_type, entity_id);
CREATE INDEX idx_audit_logs_created_at ON audit_logs (created_at);
CREATE INDEX idx_audit_logs_action ON audit_logs (action);

-- 19. job_runs
CREATE TABLE job_runs (
    id            UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id  UUID         NULL,
    task_type     VARCHAR(100) NOT NULL,
    status        VARCHAR(20)  NOT NULL,
    started_at    TIMESTAMPTZ  NOT NULL,
    finished_at   TIMESTAMPTZ  NULL,
    error_message TEXT         NULL,
    metadata      JSONB        NULL,
    created_at    TIMESTAMPTZ  NOT NULL DEFAULT now()
);

CREATE INDEX idx_job_runs_workspace_id ON job_runs (workspace_id);
CREATE INDEX idx_job_runs_task_type ON job_runs (task_type);
CREATE INDEX idx_job_runs_status ON job_runs (status);
CREATE INDEX idx_job_runs_started_at ON job_runs (started_at);
