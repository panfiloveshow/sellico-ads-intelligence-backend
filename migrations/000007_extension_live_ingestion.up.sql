CREATE TABLE extension_page_contexts (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    session_id          UUID NOT NULL REFERENCES extension_sessions(id),
    workspace_id        UUID NOT NULL REFERENCES workspaces(id),
    user_id             UUID NOT NULL REFERENCES users(id),
    url                 TEXT NOT NULL,
    page_type           VARCHAR(50) NOT NULL,
    seller_cabinet_id   UUID NULL REFERENCES seller_cabinets(id),
    campaign_id         UUID NULL REFERENCES campaigns(id),
    phrase_id           UUID NULL REFERENCES phrases(id),
    product_id          UUID NULL REFERENCES products(id),
    query               TEXT NULL,
    region              TEXT NULL,
    active_filters      JSONB NULL,
    metadata            JSONB NULL,
    dedupe_key          TEXT NOT NULL,
    captured_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, dedupe_key)
);

CREATE INDEX idx_extension_page_contexts_workspace_id ON extension_page_contexts (workspace_id);
CREATE INDEX idx_extension_page_contexts_page_type ON extension_page_contexts (page_type);
CREATE INDEX idx_extension_page_contexts_captured_at ON extension_page_contexts (captured_at DESC);
CREATE INDEX idx_extension_page_contexts_campaign_id ON extension_page_contexts (campaign_id);
CREATE INDEX idx_extension_page_contexts_phrase_id ON extension_page_contexts (phrase_id);
CREATE INDEX idx_extension_page_contexts_product_id ON extension_page_contexts (product_id);

CREATE TABLE extension_bid_snapshots (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    session_id          UUID NOT NULL REFERENCES extension_sessions(id),
    workspace_id        UUID NOT NULL REFERENCES workspaces(id),
    user_id             UUID NOT NULL REFERENCES users(id),
    seller_cabinet_id   UUID NULL REFERENCES seller_cabinets(id),
    campaign_id         UUID NULL REFERENCES campaigns(id),
    phrase_id           UUID NULL REFERENCES phrases(id),
    query               TEXT NULL,
    region              TEXT NULL,
    visible_bid         BIGINT NULL,
    recommended_bid     BIGINT NULL,
    competitive_bid     BIGINT NULL,
    leadership_bid      BIGINT NULL,
    cpm_min             BIGINT NULL,
    source              VARCHAR(20) NOT NULL DEFAULT 'extension',
    confidence          NUMERIC(5,2) NOT NULL DEFAULT 1.0,
    metadata            JSONB NULL,
    dedupe_key          TEXT NOT NULL,
    captured_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, dedupe_key)
);

CREATE INDEX idx_extension_bid_snapshots_workspace_id ON extension_bid_snapshots (workspace_id);
CREATE INDEX idx_extension_bid_snapshots_campaign_id ON extension_bid_snapshots (campaign_id);
CREATE INDEX idx_extension_bid_snapshots_phrase_id ON extension_bid_snapshots (phrase_id);
CREATE INDEX idx_extension_bid_snapshots_query_region ON extension_bid_snapshots (query, region);
CREATE INDEX idx_extension_bid_snapshots_captured_at ON extension_bid_snapshots (captured_at DESC);

CREATE TABLE extension_position_snapshots (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    session_id          UUID NOT NULL REFERENCES extension_sessions(id),
    workspace_id        UUID NOT NULL REFERENCES workspaces(id),
    user_id             UUID NOT NULL REFERENCES users(id),
    seller_cabinet_id   UUID NULL REFERENCES seller_cabinets(id),
    campaign_id         UUID NULL REFERENCES campaigns(id),
    phrase_id           UUID NULL REFERENCES phrases(id),
    product_id          UUID NULL REFERENCES products(id),
    query               TEXT NOT NULL,
    region              TEXT NOT NULL,
    visible_position    INT NOT NULL,
    visible_page        INT NULL,
    page_subtype        TEXT NULL,
    source              VARCHAR(20) NOT NULL DEFAULT 'extension',
    confidence          NUMERIC(5,2) NOT NULL DEFAULT 1.0,
    metadata            JSONB NULL,
    dedupe_key          TEXT NOT NULL,
    captured_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, dedupe_key)
);

CREATE INDEX idx_extension_position_snapshots_workspace_id ON extension_position_snapshots (workspace_id);
CREATE INDEX idx_extension_position_snapshots_product_id ON extension_position_snapshots (product_id);
CREATE INDEX idx_extension_position_snapshots_campaign_id ON extension_position_snapshots (campaign_id);
CREATE INDEX idx_extension_position_snapshots_phrase_id ON extension_position_snapshots (phrase_id);
CREATE INDEX idx_extension_position_snapshots_query_region ON extension_position_snapshots (query, region);
CREATE INDEX idx_extension_position_snapshots_captured_at ON extension_position_snapshots (captured_at DESC);

CREATE TABLE extension_ui_signals (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    session_id          UUID NOT NULL REFERENCES extension_sessions(id),
    workspace_id        UUID NOT NULL REFERENCES workspaces(id),
    user_id             UUID NOT NULL REFERENCES users(id),
    seller_cabinet_id   UUID NULL REFERENCES seller_cabinets(id),
    campaign_id         UUID NULL REFERENCES campaigns(id),
    phrase_id           UUID NULL REFERENCES phrases(id),
    product_id          UUID NULL REFERENCES products(id),
    query               TEXT NULL,
    region              TEXT NULL,
    signal_type         VARCHAR(50) NOT NULL,
    severity            VARCHAR(20) NOT NULL DEFAULT 'info',
    title               TEXT NOT NULL,
    message             TEXT NULL,
    confidence          NUMERIC(5,2) NOT NULL DEFAULT 1.0,
    metadata            JSONB NULL,
    dedupe_key          TEXT NOT NULL,
    captured_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, dedupe_key)
);

CREATE INDEX idx_extension_ui_signals_workspace_id ON extension_ui_signals (workspace_id);
CREATE INDEX idx_extension_ui_signals_campaign_id ON extension_ui_signals (campaign_id);
CREATE INDEX idx_extension_ui_signals_phrase_id ON extension_ui_signals (phrase_id);
CREATE INDEX idx_extension_ui_signals_product_id ON extension_ui_signals (product_id);
CREATE INDEX idx_extension_ui_signals_signal_type ON extension_ui_signals (signal_type);
CREATE INDEX idx_extension_ui_signals_captured_at ON extension_ui_signals (captured_at DESC);

CREATE TABLE extension_network_captures (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    session_id          UUID NOT NULL REFERENCES extension_sessions(id),
    workspace_id        UUID NOT NULL REFERENCES workspaces(id),
    user_id             UUID NOT NULL REFERENCES users(id),
    seller_cabinet_id   UUID NULL REFERENCES seller_cabinets(id),
    campaign_id         UUID NULL REFERENCES campaigns(id),
    phrase_id           UUID NULL REFERENCES phrases(id),
    product_id          UUID NULL REFERENCES products(id),
    page_type           VARCHAR(50) NOT NULL,
    endpoint_key        VARCHAR(100) NOT NULL,
    query               TEXT NULL,
    region              TEXT NULL,
    payload             JSONB NOT NULL,
    dedupe_key          TEXT NOT NULL,
    captured_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, dedupe_key)
);

CREATE INDEX idx_extension_network_captures_workspace_id ON extension_network_captures (workspace_id);
CREATE INDEX idx_extension_network_captures_endpoint_key ON extension_network_captures (endpoint_key);
CREATE INDEX idx_extension_network_captures_campaign_id ON extension_network_captures (campaign_id);
CREATE INDEX idx_extension_network_captures_phrase_id ON extension_network_captures (phrase_id);
CREATE INDEX idx_extension_network_captures_product_id ON extension_network_captures (product_id);
CREATE INDEX idx_extension_network_captures_captured_at ON extension_network_captures (captured_at DESC);
