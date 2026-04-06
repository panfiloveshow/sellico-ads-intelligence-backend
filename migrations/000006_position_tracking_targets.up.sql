CREATE TABLE position_tracking_targets (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id        UUID NOT NULL REFERENCES workspaces(id),
    product_id          UUID NOT NULL REFERENCES products(id),
    query               TEXT NOT NULL,
    region              TEXT NOT NULL,
    is_active           BOOLEAN NOT NULL DEFAULT TRUE,
    baseline_position   INT,
    baseline_checked_at TIMESTAMPTZ,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, product_id, query, region)
);

CREATE INDEX idx_position_tracking_targets_workspace_id
    ON position_tracking_targets (workspace_id);

CREATE INDEX idx_position_tracking_targets_product_query_region
    ON position_tracking_targets (product_id, query, region);
