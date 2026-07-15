ALTER TABLE wb_bid_actions
    ADD COLUMN phrase_id UUID NULL REFERENCES phrases(id) ON DELETE SET NULL;

-- Do not backfill this from phrases.updated_at: that timestamp also changes
-- when WB returns cluster stats without returning a current bid.
ALTER TABLE phrases
    ADD COLUMN current_bid_observed_at TIMESTAMPTZ NULL;

ALTER TABLE bid_decision_observations
    ADD COLUMN phrase_id UUID NULL REFERENCES phrases(id) ON DELETE CASCADE,
    ADD COLUMN norm_query TEXT NULL;

CREATE INDEX idx_wb_bid_actions_unresolved_cluster_automation
    ON wb_bid_actions (campaign_id, product_id, norm_query, created_at)
    WHERE action_type = 'strategy_bid' AND status IN ('pending', 'unknown');

CREATE INDEX idx_bid_decision_observations_phrase
    ON bid_decision_observations (phrase_id, last_seen_at DESC)
    WHERE phrase_id IS NOT NULL;
