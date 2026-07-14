CREATE TABLE dayparting_states (
    strategy_id      UUID        NOT NULL REFERENCES strategies(id) ON DELETE CASCADE,
    campaign_id      UUID        NOT NULL REFERENCES campaigns(id) ON DELETE CASCADE,
    product_id       UUID        REFERENCES products(id) ON DELETE CASCADE,
    scope_key        TEXT        NOT NULL,
    placement        VARCHAR(20) NOT NULL,
    baseline_bid     INT         NOT NULL CHECK (baseline_bid > 0),
    last_target_bid  INT         NOT NULL CHECK (last_target_bid > 0),
    last_slot        TEXT        NOT NULL,
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (strategy_id, campaign_id, scope_key, placement)
);

CREATE INDEX idx_dayparting_states_campaign
    ON dayparting_states (campaign_id, updated_at DESC);

ALTER TABLE wb_bid_actions
    ADD COLUMN automation_key TEXT NULL,
    ADD COLUMN automation_observation_key TEXT NULL;

CREATE UNIQUE INDEX wb_bid_actions_automation_key_unique
    ON wb_bid_actions (automation_key)
    WHERE automation_key IS NOT NULL;

CREATE UNIQUE INDEX wb_bid_actions_automation_observation_key_unique
    ON wb_bid_actions (automation_observation_key)
    WHERE automation_observation_key IS NOT NULL;

CREATE OR REPLACE FUNCTION validate_strategy_binding_scope()
RETURNS TRIGGER AS $$
DECLARE
    strategy_workspace UUID;
    strategy_cabinet UUID;
BEGIN
    SELECT workspace_id, seller_cabinet_id
      INTO strategy_workspace, strategy_cabinet
      FROM strategies WHERE id = NEW.strategy_id;

    IF NEW.campaign_id IS NOT NULL AND NOT EXISTS (
        SELECT 1 FROM campaigns
        WHERE id = NEW.campaign_id
          AND workspace_id = strategy_workspace
          AND seller_cabinet_id = strategy_cabinet
    ) THEN
        RAISE EXCEPTION 'strategy campaign binding must use the strategy seller cabinet';
    END IF;

    IF NEW.product_id IS NOT NULL AND NOT EXISTS (
        SELECT 1 FROM products
        WHERE id = NEW.product_id
          AND workspace_id = strategy_workspace
          AND seller_cabinet_id = strategy_cabinet
    ) THEN
        RAISE EXCEPTION 'strategy product binding must use the strategy seller cabinet';
    END IF;

    IF NEW.campaign_id IS NOT NULL AND NEW.product_id IS NOT NULL AND NOT EXISTS (
        SELECT 1 FROM campaign_products
        WHERE campaign_id = NEW.campaign_id
          AND product_id = NEW.product_id
          AND workspace_id = strategy_workspace
          AND seller_cabinet_id = strategy_cabinet
    ) THEN
        RAISE EXCEPTION 'strategy product binding must reference a real campaign product';
    END IF;

    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER strategy_bindings_scope_guard
BEFORE INSERT OR UPDATE ON strategy_bindings
FOR EACH ROW EXECUTE FUNCTION validate_strategy_binding_scope();
