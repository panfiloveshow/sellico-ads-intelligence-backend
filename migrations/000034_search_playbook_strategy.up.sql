-- search_playbook: frequency-tiered, position-targeting search campaign automation.
-- Recreate the strategies.type CHECK to include search_playbook. We also fold in the
-- repricer price_* types, which the original 000011 CHECK predates, so the constraint
-- reflects every type the application actually writes.
ALTER TABLE strategies DROP CONSTRAINT IF EXISTS strategies_type_check;
ALTER TABLE strategies ADD CONSTRAINT strategies_type_check CHECK (type IN (
    'acos', 'roas', 'anti_sliv', 'dayparting', 'recommendation',
    'price_margin_floor', 'price_inventory_demand', 'price_ad_linked', 'price_peak_hours',
    'search_playbook'
));
