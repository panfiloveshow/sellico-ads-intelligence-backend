ALTER TABLE products DROP COLUMN IF EXISTS last_event_at;
ALTER TABLE products DROP COLUMN IF EXISTS content_hash;
ALTER TABLE products DROP COLUMN IF EXISTS stock_total;
ALTER TABLE products DROP COLUMN IF EXISTS reviews_count;
ALTER TABLE products DROP COLUMN IF EXISTS rating;

DROP TABLE IF EXISTS product_snapshots;
DROP TABLE IF EXISTS product_events;
