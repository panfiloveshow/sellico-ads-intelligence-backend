DROP TABLE IF EXISTS ozon_sales_daily;
DROP TABLE IF EXISTS ozon_product_stocks;
DROP TABLE IF EXISTS ozon_price_schedule_entries;

-- Restore the 000051 constraint list.
ALTER TABLE strategies DROP CONSTRAINT IF EXISTS strategies_type_check;
ALTER TABLE strategies ADD CONSTRAINT strategies_type_check CHECK (type IN (
    'acos', 'roas', 'anti_sliv', 'dayparting', 'recommendation', 'search_playbook',
    'price_margin_floor', 'price_inventory_demand', 'price_ad_linked', 'price_peak_hours',
    'price_competitor_follow',
    'ozon_cpc_target_drr',
    'ozon_ai_autopilot',
    'ozon_price_margin_floor',
    'ozon_price_competitor_follow'
));
