-- Restore the 000034 version of strategies_type_check. Rows with the new type
-- must go first or the recreated constraint cannot be validated.
DELETE FROM strategies WHERE type = 'ozon_cpc_target_drr';
ALTER TABLE strategies DROP CONSTRAINT IF EXISTS strategies_type_check;
ALTER TABLE strategies ADD CONSTRAINT strategies_type_check CHECK (type IN (
    'acos', 'roas', 'anti_sliv', 'dayparting', 'recommendation',
    'price_margin_floor', 'price_inventory_demand', 'price_ad_linked', 'price_peak_hours',
    'search_playbook'
));

-- Ozon-only bindings would violate the restored two-column target check.
DELETE FROM strategy_bindings
WHERE ozon_campaign_id IS NOT NULL AND campaign_id IS NULL AND product_id IS NULL;

DROP INDEX IF EXISTS idx_strategy_bindings_ozon_campaign;
ALTER TABLE strategy_bindings DROP CONSTRAINT IF EXISTS chk_binding_target;
ALTER TABLE strategy_bindings DROP COLUMN IF EXISTS ozon_campaign_id;
ALTER TABLE strategy_bindings ADD CONSTRAINT chk_binding_target
    CHECK (campaign_id IS NOT NULL OR product_id IS NOT NULL);

DROP TABLE IF EXISTS ozon_cpo_products;
DROP TABLE IF EXISTS ozon_bid_changes;
