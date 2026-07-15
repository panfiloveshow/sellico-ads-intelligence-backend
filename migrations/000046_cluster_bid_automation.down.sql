DROP INDEX IF EXISTS idx_bid_decision_observations_phrase;
DROP INDEX IF EXISTS idx_wb_bid_actions_unresolved_cluster_automation;

ALTER TABLE bid_decision_observations
    DROP COLUMN IF EXISTS norm_query,
    DROP COLUMN IF EXISTS phrase_id;

ALTER TABLE wb_bid_actions
    DROP COLUMN IF EXISTS phrase_id;

ALTER TABLE phrases
    DROP COLUMN IF EXISTS current_bid_observed_at;
