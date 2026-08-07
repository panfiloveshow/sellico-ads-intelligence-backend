-- Restore the 000050 version of strategies_type_check: ozon_price_* rows must
-- go first or the recreated constraint cannot be validated.
DELETE FROM strategies WHERE type IN ('ozon_price_margin_floor', 'ozon_price_competitor_follow');
ALTER TABLE strategies DROP CONSTRAINT IF EXISTS strategies_type_check;
ALTER TABLE strategies ADD CONSTRAINT strategies_type_check CHECK (type IN (
    'acos', 'roas', 'anti_sliv', 'dayparting', 'recommendation', 'search_playbook',
    'price_margin_floor', 'price_inventory_demand', 'price_ad_linked', 'price_peak_hours',
    'price_competitor_follow',
    'ozon_cpc_target_drr',
    'ozon_ai_autopilot'
));

ALTER TABLE ozon_product_prices
    DROP COLUMN IF EXISTS ozon_index_min_price_rub,
    DROP COLUMN IF EXISTS external_index_min_price_rub,
    DROP COLUMN IF EXISTS self_index_min_price_rub;

DROP TABLE IF EXISTS ozon_price_changes;
