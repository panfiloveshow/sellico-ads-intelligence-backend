DROP TABLE IF EXISTS ozon_sync_states;
DROP TABLE IF EXISTS ozon_product_prices;
DROP TABLE IF EXISTS ozon_campaign_stats;
DROP TABLE IF EXISTS ozon_campaign_products;
DROP TABLE IF EXISTS ozon_campaigns;

DROP INDEX IF EXISTS idx_seller_cabinets_marketplace;

ALTER TABLE seller_cabinets
    DROP CONSTRAINT IF EXISTS seller_cabinets_marketplace_check;

ALTER TABLE seller_cabinets
    DROP COLUMN IF EXISTS encrypted_credentials,
    DROP COLUMN IF EXISTS marketplace;
