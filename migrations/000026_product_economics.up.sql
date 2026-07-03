-- Manual/unit economics inputs for margin-aware ads decisions.
CREATE TABLE product_economics (
    id                    UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id          UUID        NOT NULL REFERENCES workspaces(id),
    wb_product_id          BIGINT      NOT NULL,
    cost_price             BIGINT      NULL,
    logistics_cost         BIGINT      NULL,
    other_costs            BIGINT      NULL,
    tax_rate_percent       DOUBLE PRECISION NULL,
    commission_percent     DOUBLE PRECISION NULL,
    target_margin_percent  DOUBLE PRECISION NULL,
    max_allowed_drr        DOUBLE PRECISION NULL,
    source                 TEXT        NOT NULL DEFAULT 'manual',
    effective_at           DATE        NULL,
    updated_by             UUID        NULL REFERENCES users(id),
    created_at             TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at             TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT product_economics_non_negative_amounts CHECK (
        (cost_price IS NULL OR cost_price >= 0) AND
        (logistics_cost IS NULL OR logistics_cost >= 0) AND
        (other_costs IS NULL OR other_costs >= 0)
    ),
    CONSTRAINT product_economics_percent_bounds CHECK (
        (tax_rate_percent IS NULL OR (tax_rate_percent >= 0 AND tax_rate_percent <= 100)) AND
        (commission_percent IS NULL OR (commission_percent >= 0 AND commission_percent <= 100)) AND
        (target_margin_percent IS NULL OR (target_margin_percent >= 0 AND target_margin_percent <= 100)) AND
        (max_allowed_drr IS NULL OR (max_allowed_drr >= 0 AND max_allowed_drr <= 100))
    )
);

CREATE UNIQUE INDEX idx_product_economics_workspace_product ON product_economics (workspace_id, wb_product_id);
CREATE INDEX idx_product_economics_workspace_id ON product_economics (workspace_id);
