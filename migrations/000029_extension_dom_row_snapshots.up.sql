CREATE TABLE extension_dom_row_snapshots (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    session_id          UUID NOT NULL REFERENCES extension_sessions(id),
    workspace_id        UUID NOT NULL REFERENCES workspaces(id),
    user_id             UUID NOT NULL REFERENCES users(id),
    seller_cabinet_id   UUID NULL REFERENCES seller_cabinets(id),
    campaign_id         UUID NULL REFERENCES campaigns(id),
    phrase_id           UUID NULL REFERENCES phrases(id),
    product_id          UUID NULL REFERENCES products(id),
    page_type           VARCHAR(50) NOT NULL,
    table_role          VARCHAR(50) NOT NULL,
    row_key             TEXT NOT NULL,
    query               TEXT NULL,
    region              TEXT NULL,
    visible_text        TEXT NOT NULL,
    cells               JSONB NULL,
    metadata            JSONB NULL,
    source              VARCHAR(20) NOT NULL DEFAULT 'extension',
    confidence          NUMERIC(5,2) NOT NULL DEFAULT 0.65,
    dedupe_key          TEXT NOT NULL,
    captured_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, dedupe_key)
);

CREATE INDEX idx_extension_dom_row_snapshots_workspace_id ON extension_dom_row_snapshots (workspace_id);
CREATE INDEX idx_extension_dom_row_snapshots_campaign_id ON extension_dom_row_snapshots (campaign_id);
CREATE INDEX idx_extension_dom_row_snapshots_phrase_id ON extension_dom_row_snapshots (phrase_id);
CREATE INDEX idx_extension_dom_row_snapshots_product_id ON extension_dom_row_snapshots (product_id);
CREATE INDEX idx_extension_dom_row_snapshots_page_role ON extension_dom_row_snapshots (page_type, table_role);
CREATE INDEX idx_extension_dom_row_snapshots_query_region ON extension_dom_row_snapshots (query, region);
CREATE INDEX idx_extension_dom_row_snapshots_captured_at ON extension_dom_row_snapshots (captured_at DESC);
