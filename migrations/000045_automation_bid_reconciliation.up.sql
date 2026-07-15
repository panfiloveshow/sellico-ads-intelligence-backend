ALTER TABLE wb_bid_actions
    ADD COLUMN placement VARCHAR(20) NULL,
    ADD COLUMN bid_observed_at TIMESTAMPTZ NULL,
    ADD COLUMN reconciled_at TIMESTAMPTZ NULL,
    ADD COLUMN strategy_id UUID NULL REFERENCES strategies(id) ON DELETE SET NULL;

ALTER TABLE bid_changes
    ADD COLUMN automation_action_id UUID NULL REFERENCES wb_bid_actions(id) ON DELETE SET NULL;

-- Recover the scope of legacy pending rows created before placement was stored.
UPDATE wb_bid_actions action
SET placement = CASE campaign.bid_type
    WHEN 'unified' THEN 'combined'
    WHEN 'manual' THEN 'search'
    ELSE action.placement
END
FROM campaigns campaign
WHERE action.campaign_id = campaign.id
  AND action.action_type = 'strategy_bid'
  AND action.status IN ('pending', 'unknown')
  AND action.placement IS NULL;

CREATE UNIQUE INDEX bid_changes_automation_action_unique
    ON bid_changes (automation_action_id)
    WHERE automation_action_id IS NOT NULL;

CREATE INDEX idx_wb_bid_actions_unresolved_automation
    ON wb_bid_actions (created_at)
    WHERE action_type = 'strategy_bid' AND status IN ('pending', 'unknown');
