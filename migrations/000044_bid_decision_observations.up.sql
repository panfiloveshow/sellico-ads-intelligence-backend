-- Records counterfactual bid decisions for analytics/semi-auto strategies.
-- No row in this table means an external WB action was applied.
CREATE TABLE bid_decision_observations (
    id                    UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    observation_key       TEXT        NOT NULL UNIQUE,
    workspace_id          UUID        NOT NULL REFERENCES workspaces(id),
    seller_cabinet_id     UUID        NOT NULL REFERENCES seller_cabinets(id),
    strategy_id           UUID        NOT NULL REFERENCES strategies(id) ON DELETE CASCADE,
    strategy_binding_id   UUID        NOT NULL REFERENCES strategy_bindings(id) ON DELETE CASCADE,
    campaign_id           UUID        NOT NULL REFERENCES campaigns(id) ON DELETE CASCADE,
    product_id            UUID        REFERENCES products(id) ON DELETE CASCADE,
    wb_campaign_id        BIGINT      NOT NULL,
    wb_product_id         BIGINT      NOT NULL,
    placement             VARCHAR(20) NOT NULL,
    old_bid               INT         NOT NULL CHECK (old_bid > 0),
    proposed_bid          INT         NOT NULL CHECK (proposed_bid > 0),
    reason                TEXT        NOT NULL,
    metrics               JSONB       NOT NULL,
    automation_level      INT         NOT NULL CHECK (automation_level IN (1, 2)),
    bid_observed_at       TIMESTAMPTZ NOT NULL,
    first_seen_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_bid_decision_observations_workspace
    ON bid_decision_observations (workspace_id, last_seen_at DESC);
CREATE INDEX idx_bid_decision_observations_strategy
    ON bid_decision_observations (strategy_id, last_seen_at DESC);
CREATE INDEX idx_bid_decision_observations_campaign
    ON bid_decision_observations (campaign_id, last_seen_at DESC);

COMMENT ON TABLE bid_decision_observations IS
    'Shadow-mode decisions calculated from real data and never applied to WB.';
