-- Restore the 000049 version of ozon_bid_changes.source: 'ai' rows must go
-- first or the recreated constraint cannot be validated.
DELETE FROM ozon_bid_changes WHERE source = 'ai';
ALTER TABLE ozon_bid_changes DROP CONSTRAINT IF EXISTS ozon_bid_changes_source_check;
ALTER TABLE ozon_bid_changes ADD CONSTRAINT ozon_bid_changes_source_check
    CHECK (source IN ('strategy', 'manual'));

-- Restore the 000049 version of strategies_type_check.
DELETE FROM strategies WHERE type = 'ozon_ai_autopilot';
ALTER TABLE strategies DROP CONSTRAINT IF EXISTS strategies_type_check;
ALTER TABLE strategies ADD CONSTRAINT strategies_type_check CHECK (type IN (
    'acos', 'roas', 'anti_sliv', 'dayparting', 'recommendation', 'search_playbook',
    'price_margin_floor', 'price_inventory_demand', 'price_ad_linked', 'price_peak_hours',
    'price_competitor_follow',
    'ozon_cpc_target_drr'
));

DROP TABLE IF EXISTS ai_decisions;
DROP TABLE IF EXISTS ai_runs;
