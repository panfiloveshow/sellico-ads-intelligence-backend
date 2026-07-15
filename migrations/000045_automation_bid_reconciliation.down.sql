DROP INDEX IF EXISTS idx_wb_bid_actions_unresolved_automation;
DROP INDEX IF EXISTS bid_changes_automation_action_unique;

ALTER TABLE bid_changes
    DROP COLUMN IF EXISTS automation_action_id;

ALTER TABLE wb_bid_actions
    DROP COLUMN IF EXISTS strategy_id,
    DROP COLUMN IF EXISTS reconciled_at,
    DROP COLUMN IF EXISTS bid_observed_at,
    DROP COLUMN IF EXISTS placement;
