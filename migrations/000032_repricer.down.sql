ALTER TABLE seller_cabinets
    DROP COLUMN IF EXISTS prices_scope_status,
    DROP COLUMN IF EXISTS prices_scope_checked_at;

DROP TABLE IF EXISTS price_schedule_entries;
DROP TABLE IF EXISTS price_quarantine_goods;
DROP TABLE IF EXISTS price_changes;
DROP TABLE IF EXISTS price_upload_tasks;
DROP TABLE IF EXISTS product_prices;
